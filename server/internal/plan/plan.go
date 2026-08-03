// Package plan 实现 M1-12「CycleAgent / Plan-Execute」的领域层。
//
// Plan-Execute（L2 范式，见 docs/02 §「自主化能力分级」）把一次中型任务拆成
// 「planner 产出计划 → executor 逐项执行 → 每项完成后更新进度」的显式循环：
//
//	create_plan  → 计划外置（PLAN 视图：步骤清单 + 状态）
//	update_step  → 进度外置（PROGRESS 视图：已终结步骤 + 执行记录）
//	get_plan     → 随时回读，避免 LLM 凭记忆臆测还剩什么没做
//
// 「外置」是关键：计划与进度不靠模型自己在上下文里记，而是落在本包的 Store 中，
// 由框架侧扩展（internal/agent/plan.go）在每轮循环中重新渲染回灌，
// 因此上下文被截断/跨轮次也不会丢失任务状态（M1-16 会把它进一步落到 artifact 文件）。
//
// 本包只负责纯领域逻辑（状态机 + 存储 + 渲染），不依赖 trpc-agent-go 框架，
// 便于独立单测；与框架的对接在 internal/agent 层完成（与 internal/goal 同款分层）。
package plan

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// StepStatus 是单个计划步骤的生命周期状态。
type StepStatus string

const (
	// StepPending 步骤尚未开始。
	StepPending StepStatus = "pending"
	// StepInProgress 步骤正在执行中。
	StepInProgress StepStatus = "in_progress"
	// StepDone 步骤已完成。
	StepDone StepStatus = "done"
	// StepSkipped 步骤经判断无需执行（必须给出理由）。
	StepSkipped StepStatus = "skipped"
	// StepFailed 步骤执行失败且无法继续（必须给出理由）。
	StepFailed StepStatus = "failed"
)

// AllStepStatuses 返回全部合法步骤状态，供工具 schema 与错误提示复用。
func AllStepStatuses() []StepStatus {
	return []StepStatus{StepPending, StepInProgress, StepDone, StepSkipped, StepFailed}
}

// ParseStepStatus 校验并归一化步骤状态字符串。
func ParseStepStatus(s string) (StepStatus, error) {
	v := StepStatus(strings.ToLower(strings.TrimSpace(s)))
	for _, st := range AllStepStatuses() {
		if v == st {
			return v, nil
		}
	}
	return "", fmt.Errorf("invalid step status %q, must be one of: pending, in_progress, done, skipped, failed", s)
}

// IsOpen 表示该步骤仍需继续推进。
func (s StepStatus) IsOpen() bool {
	return s == StepPending || s == StepInProgress
}

// IsTerminal 表示该步骤已收敛（done/skipped/failed）。
func (s StepStatus) IsTerminal() bool { return !s.IsOpen() }

// 领域错误。
var (
	// ErrNotFound 表示当前作用域尚未建立计划。
	ErrNotFound = errors.New("plan: no plan has been created in this scope")
	// ErrEmptyTitle 表示计划标题为空。
	ErrEmptyTitle = errors.New("plan: title is required and must be non-empty")
	// ErrNoSteps 表示计划没有任何有效步骤。
	ErrNoSteps = errors.New("plan: at least one step is required")
	// ErrStepNotFound 表示按 id 找不到步骤。
	ErrStepNotFound = errors.New("plan: step not found")
	// ErrTerminalNoNote 表示把步骤置为 skipped/failed 却没写明理由。
	ErrTerminalNoNote = errors.New("plan: note is required when a step is marked skipped or failed")
)

// Step 是计划中的一个步骤。Note 承载「执行记录」，即 PROGRESS 的最小单元。
type Step struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Detail    string     `json:"detail,omitempty"`
	Status    StepStatus `json:"status"`
	Note      string     `json:"note,omitempty"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// StepSpec 是新增步骤时的输入（尚未分配 id）。
type StepSpec struct {
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
}

// Plan 是一份外置的执行计划。
type Plan struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Steps     []Step    `json:"steps"`
	Revision  int       `json:"revision"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// stepSeq 保证步骤 id 单调递增，避免删除/追加后 id 复用造成指代歧义。
	stepSeq int
}

// Clone 返回深拷贝，避免调用方拿到 Store 内部指针后并发改写。
func (p *Plan) Clone() *Plan {
	if p == nil {
		return nil
	}
	cp := *p
	if len(p.Steps) > 0 {
		cp.Steps = append([]Step(nil), p.Steps...)
	}
	return &cp
}

// Counts 汇总各状态的步骤数量。
type Counts struct {
	Total      int `json:"total"`
	Done       int `json:"done"`
	Skipped    int `json:"skipped"`
	Failed     int `json:"failed"`
	InProgress int `json:"in_progress"`
	Pending    int `json:"pending"`
}

// Open 返回尚未收敛的步骤数（pending + in_progress）。
func (c Counts) Open() int { return c.Pending + c.InProgress }

// Counts 统计计划中各状态步骤数量。
func (p *Plan) Counts() Counts {
	var c Counts
	if p == nil {
		return c
	}
	c.Total = len(p.Steps)
	for i := range p.Steps {
		switch p.Steps[i].Status {
		case StepDone:
			c.Done++
		case StepSkipped:
			c.Skipped++
		case StepFailed:
			c.Failed++
		case StepInProgress:
			c.InProgress++
		default:
			c.Pending++
		}
	}
	return c
}

// IsOpen 表示计划中仍有未收敛的步骤（即循环不应结束）。
func (p *Plan) IsOpen() bool {
	if p == nil {
		return false
	}
	for i := range p.Steps {
		if p.Steps[i].Status.IsOpen() {
			return true
		}
	}
	return false
}

// Next 返回下一个应执行的步骤：优先「正在执行中」的，其次第一个 pending。
// 全部收敛时返回 nil。
func (p *Plan) Next() *Step {
	if p == nil {
		return nil
	}
	for i := range p.Steps {
		if p.Steps[i].Status == StepInProgress {
			s := p.Steps[i]
			return &s
		}
	}
	for i := range p.Steps {
		if p.Steps[i].Status == StepPending {
			s := p.Steps[i]
			return &s
		}
	}
	return nil
}

// Find 按 id 查找步骤（id 容错：s1 / S1 / 1 等价）。
func (p *Plan) Find(id string) (*Step, bool) {
	if p == nil {
		return nil, false
	}
	want := NormalizeStepID(id)
	if want == "" {
		return nil, false
	}
	for i := range p.Steps {
		if p.Steps[i].ID == want {
			s := p.Steps[i]
			return &s, true
		}
	}
	return nil, false
}

// statusMark 返回渲染用的状态标记。
func statusMark(s StepStatus) string {
	switch s {
	case StepDone:
		return "[x]"
	case StepSkipped:
		return "[-]"
	case StepFailed:
		return "[!]"
	case StepInProgress:
		return "[~]"
	default:
		return "[ ]"
	}
}

// Render 渲染 PLAN 视图（完整步骤清单 + 状态 + 进度概览），供注入 prompt。
func (p *Plan) Render() string {
	if p == nil {
		return "（当前没有执行计划）"
	}
	c := p.Counts()
	var b strings.Builder
	fmt.Fprintf(&b, "计划[%s] %s\n进度: %d/%d 已完成", p.ID, p.Title, c.Done, c.Total)
	if c.Skipped > 0 {
		fmt.Fprintf(&b, "，%d 跳过", c.Skipped)
	}
	if c.Failed > 0 {
		fmt.Fprintf(&b, "，%d 失败", c.Failed)
	}
	if c.Open() > 0 {
		fmt.Fprintf(&b, "，%d 待办", c.Open())
	}
	b.WriteString("\n步骤:")
	for i := range p.Steps {
		s := p.Steps[i]
		fmt.Fprintf(&b, "\n  %s %s %s", statusMark(s.Status), s.ID, s.Title)
		if s.Detail != "" {
			fmt.Fprintf(&b, "（%s）", s.Detail)
		}
		if s.Note != "" {
			fmt.Fprintf(&b, " — %s", s.Note)
		}
	}
	return b.String()
}

// RenderProgress 渲染 PROGRESS 视图（只含已收敛步骤及其执行记录）。
func (p *Plan) RenderProgress() string {
	if p == nil {
		return "（当前没有执行计划）"
	}
	var lines []string
	for i := range p.Steps {
		s := p.Steps[i]
		if s.Status.IsOpen() {
			continue
		}
		line := fmt.Sprintf("  %s %s %s [%s]", statusMark(s.Status), s.ID, s.Title, s.Status)
		if s.Note != "" {
			line += " — " + s.Note
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return "进展: 尚无已完成的步骤。"
	}
	return "进展:\n" + strings.Join(lines, "\n")
}

// StepPatch 描述一次步骤更新，nil 字段表示不改。
type StepPatch struct {
	Status *StepStatus
	Note   *string
	Title  *string
	Detail *string
}

// NormalizeStepID 归一化步骤 id：去空白、转小写，纯数字自动补 "s" 前缀。
// 这样 LLM 写 "1"、"S2"、" s3 " 都能命中，减少无谓的工具调用失败。
func NormalizeStepID(id string) string {
	v := strings.ToLower(strings.TrimSpace(id))
	if v == "" {
		return ""
	}
	if _, err := strconv.Atoi(v); err == nil {
		return "s" + v
	}
	return v
}

// DefaultMaxScopes 是 Store 默认保留的作用域上限。
// 服务是 7x24 常驻的，必须有界，超出时淘汰最久未更新的作用域。
const DefaultMaxScopes = 512

// Store 是按作用域（一般为 session id，退化为 invocation id）隔离的计划存储。
// 并发安全：同一 session 的并发 invocation 共享同一份计划。
type Store struct {
	mu        sync.RWMutex
	plans     map[string]*Plan
	maxScopes int
	seq       int64
	now       func() time.Time
}

// NewStore 创建计划存储。maxScopes <= 0 时取 DefaultMaxScopes。
func NewStore(maxScopes int) *Store {
	if maxScopes <= 0 {
		maxScopes = DefaultMaxScopes
	}
	return &Store{
		plans:     make(map[string]*Plan),
		maxScopes: maxScopes,
		now:       time.Now,
	}
}

// Create 在 scope 下建立（或覆盖）执行计划。
// 覆盖是刻意允许的：同一 session 的下一轮任务需要重新做计划。
func (s *Store) Create(scope, title string, steps []StepSpec) (*Plan, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, ErrEmptyTitle
	}
	specs := normalizeSpecs(steps)
	if len(specs) == 0 {
		return nil, ErrNoSteps
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	s.seq++
	p := &Plan{
		ID:        fmt.Sprintf("p%d", s.seq),
		Title:     title,
		Revision:  1,
		CreatedAt: now,
		UpdatedAt: now,
	}
	p.appendSteps(specs, now)
	s.plans[scope] = p
	s.evictLocked()
	return p.Clone(), nil
}

// Get 读取 scope 下的计划，不存在时返回 ErrNotFound。
func (s *Store) Get(scope string) (*Plan, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.plans[scope]
	if !ok {
		return nil, ErrNotFound
	}
	return p.Clone(), nil
}

// Has 判断 scope 下是否存在计划。
func (s *Store) Has(scope string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.plans[scope]
	return ok
}

// IsOpen 判断 scope 下是否存在「仍有未完成步骤」的计划。
// 无计划时返回 false —— 没立计划不应该把 Agent 锁死（是否强制立计划由上层决定）。
func (s *Store) IsOpen(scope string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.plans[scope]
	return ok && p.IsOpen()
}

// AddSteps 向既有计划追加步骤（执行中发现新工作时的计划修订）。
func (s *Store) AddSteps(scope string, steps []StepSpec) (*Plan, error) {
	specs := normalizeSpecs(steps)
	if len(specs) == 0 {
		return nil, ErrNoSteps
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.plans[scope]
	if !ok {
		return nil, ErrNotFound
	}
	next := p.Clone()
	now := s.now()
	next.appendSteps(specs, now)
	next.Revision = p.Revision + 1
	next.UpdatedAt = now
	s.plans[scope] = next
	return next.Clone(), nil
}

// UpdateStep 按 patch 更新指定步骤。
// 置为 skipped/failed 时必须给出 note 说明理由（避免「静默跳过」掩盖未完成的工作）。
func (s *Store) UpdateStep(scope, stepID string, patch StepPatch) (*Plan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.plans[scope]
	if !ok {
		return nil, ErrNotFound
	}
	want := NormalizeStepID(stepID)
	if want == "" {
		return nil, ErrStepNotFound
	}
	next := p.Clone()
	idx := -1
	for i := range next.Steps {
		if next.Steps[i].ID == want {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, fmt.Errorf("%w: %s", ErrStepNotFound, want)
	}

	st := next.Steps[idx]
	if patch.Title != nil {
		t := strings.TrimSpace(*patch.Title)
		if t == "" {
			return nil, ErrEmptyTitle
		}
		st.Title = t
	}
	if patch.Detail != nil {
		st.Detail = strings.TrimSpace(*patch.Detail)
	}
	if patch.Note != nil {
		st.Note = strings.TrimSpace(*patch.Note)
	}
	if patch.Status != nil {
		st.Status = *patch.Status
	}
	if (st.Status == StepSkipped || st.Status == StepFailed) && st.Note == "" {
		return nil, ErrTerminalNoNote
	}
	now := s.now()
	st.UpdatedAt = now
	next.Steps[idx] = st
	next.Revision = p.Revision + 1
	next.UpdatedAt = now
	s.plans[scope] = next
	return next.Clone(), nil
}

// Delete 移除 scope 下的计划。
func (s *Store) Delete(scope string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.plans, scope)
}

// Len 返回当前作用域数量（测试与可观测用）。
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.plans)
}

// appendSteps 追加步骤并分配单调递增 id。调用方需持有写锁（或对象尚未共享）。
func (p *Plan) appendSteps(specs []StepSpec, now time.Time) {
	for _, spec := range specs {
		p.stepSeq++
		p.Steps = append(p.Steps, Step{
			ID:        fmt.Sprintf("s%d", p.stepSeq),
			Title:     spec.Title,
			Detail:    spec.Detail,
			Status:    StepPending,
			UpdatedAt: now,
		})
	}
}

// evictLocked 在超出上限时淘汰最久未更新的作用域。调用方需持有写锁。
func (s *Store) evictLocked() {
	for len(s.plans) > s.maxScopes {
		var oldestKey string
		var oldestAt time.Time
		first := true
		for k, v := range s.plans {
			if first || v.UpdatedAt.Before(oldestAt) {
				oldestKey, oldestAt, first = k, v.UpdatedAt, false
			}
		}
		if first {
			return
		}
		delete(s.plans, oldestKey)
	}
}

// normalizeSpecs 清洗步骤输入：去空白、丢弃空标题。
func normalizeSpecs(in []StepSpec) []StepSpec {
	if len(in) == 0 {
		return nil
	}
	out := make([]StepSpec, 0, len(in))
	for _, s := range in {
		title := strings.TrimSpace(s.Title)
		if title == "" {
			continue
		}
		out = append(out, StepSpec{Title: title, Detail: strings.TrimSpace(s.Detail)})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
