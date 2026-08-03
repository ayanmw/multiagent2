// Package goal 实现 M1-11「目标契约」的领域层。
//
// 目标契约（Goal Contract）是让 Orchestrator 具备「不达目标不收工」能力的核心数据结构：
// LLM 在开工前用 create_goal 写下本轮要达成的目标与验收标准，过程中用 update_goal
// 汇报进展，只有当目标进入 complete（达成）或 blocked（客观受阻）时，才允许给出最终答复。
//
// 本包只负责纯领域逻辑（状态机 + 存储 + 渲染），不依赖 trpc-agent-go 框架，
// 便于独立单测；与框架的对接在 internal/agent 层完成。
package goal

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Status 是目标的生命周期状态。
type Status string

const (
	// StatusPending 目标已登记但尚未开工。
	StatusPending Status = "pending"
	// StatusInProgress 目标正在推进中。
	StatusInProgress Status = "in_progress"
	// StatusComplete 目标已达成，允许结束本轮。
	StatusComplete Status = "complete"
	// StatusBlocked 目标被客观外部因素阻塞，允许结束本轮并向用户说明。
	StatusBlocked Status = "blocked"
)

// AllStatuses 返回全部合法状态，供工具 schema 与错误提示复用。
func AllStatuses() []Status {
	return []Status{StatusPending, StatusInProgress, StatusComplete, StatusBlocked}
}

// ParseStatus 校验并归一化状态字符串。
func ParseStatus(s string) (Status, error) {
	v := Status(strings.ToLower(strings.TrimSpace(s)))
	for _, st := range AllStatuses() {
		if v == st {
			return v, nil
		}
	}
	return "", fmt.Errorf("invalid status %q, must be one of: pending, in_progress, complete, blocked", s)
}

// IsOpen 表示该状态仍需继续推进（不允许给最终答复）。
func (s Status) IsOpen() bool {
	return s == StatusPending || s == StatusInProgress
}

// IsTerminal 表示该状态已收敛（允许给最终答复）。
func (s Status) IsTerminal() bool { return !s.IsOpen() }

// 领域错误。
var (
	// ErrNotFound 表示当前作用域尚未登记目标。
	ErrNotFound = errors.New("goal: no goal has been created in this scope")
	// ErrEmptyTitle 表示目标标题为空。
	ErrEmptyTitle = errors.New("goal: title is required and must be non-empty")
	// ErrBlockedNoReason 表示置为 blocked 但未给出阻塞原因。
	ErrBlockedNoReason = errors.New("goal: blocker reason is required when status is blocked")
)

// Goal 是一份目标契约。
type Goal struct {
	ID                 string    `json:"id"`
	Title              string    `json:"title"`
	Description        string    `json:"description,omitempty"`
	AcceptanceCriteria []string  `json:"acceptance_criteria,omitempty"`
	Status             Status    `json:"status"`
	Progress           string    `json:"progress,omitempty"`
	Blocker            string    `json:"blocker,omitempty"`
	Revision           int       `json:"revision"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// IsOpen 表示目标尚未收敛。
func (g *Goal) IsOpen() bool {
	return g != nil && g.Status.IsOpen()
}

// Clone 返回深拷贝，避免调用方拿到 Store 内部指针后并发改写。
func (g *Goal) Clone() *Goal {
	if g == nil {
		return nil
	}
	cp := *g
	if len(g.AcceptanceCriteria) > 0 {
		cp.AcceptanceCriteria = append([]string(nil), g.AcceptanceCriteria...)
	}
	return &cp
}

// Render 渲染为供 LLM 阅读的紧凑文本（注入 prompt 用）。
func (g *Goal) Render() string {
	if g == nil {
		return "（当前没有目标契约）"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "目标[%s] %s\n状态: %s", g.ID, g.Title, g.Status)
	if g.Description != "" {
		fmt.Fprintf(&b, "\n描述: %s", g.Description)
	}
	if len(g.AcceptanceCriteria) > 0 {
		b.WriteString("\n验收标准:")
		for i, c := range g.AcceptanceCriteria {
			fmt.Fprintf(&b, "\n  %d. %s", i+1, c)
		}
	}
	if g.Progress != "" {
		fmt.Fprintf(&b, "\n进展: %s", g.Progress)
	}
	if g.Blocker != "" {
		fmt.Fprintf(&b, "\n阻塞: %s", g.Blocker)
	}
	return b.String()
}

// Patch 描述一次 update_goal 的增量修改，nil 字段表示不改。
type Patch struct {
	Status             *Status
	Progress           *string
	Blocker            *string
	Title              *string
	Description        *string
	AcceptanceCriteria *[]string
}

// DefaultMaxScopes 是 Store 默认保留的作用域上限。
// 服务是 7x24 常驻的，必须有界，超出时淘汰最久未更新的作用域。
const DefaultMaxScopes = 512

// Store 是按作用域（一般为 session id，退化为 invocation id）隔离的目标存储。
// 并发安全：同一 session 的并发 invocation 共享同一份目标。
type Store struct {
	mu        sync.RWMutex
	goals     map[string]*Goal
	maxScopes int
	seq       int64
	now       func() time.Time
}

// NewStore 创建目标存储。maxScopes <= 0 时取 DefaultMaxScopes。
func NewStore(maxScopes int) *Store {
	if maxScopes <= 0 {
		maxScopes = DefaultMaxScopes
	}
	return &Store{
		goals:     make(map[string]*Goal),
		maxScopes: maxScopes,
		now:       time.Now,
	}
}

// Create 在 scope 下登记（或覆盖）目标契约。
// 覆盖是刻意允许的：同一 session 的下一轮任务需要重新立目标。
func (s *Store) Create(scope, title, description string, criteria []string) (*Goal, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, ErrEmptyTitle
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	s.seq++
	g := &Goal{
		ID:                 fmt.Sprintf("g%d", s.seq),
		Title:              title,
		Description:        strings.TrimSpace(description),
		AcceptanceCriteria: normalizeCriteria(criteria),
		Status:             StatusInProgress,
		Revision:           1,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	s.goals[scope] = g
	s.evictLocked()
	return g.Clone(), nil
}

// Get 读取 scope 下的目标，不存在时返回 ErrNotFound。
func (s *Store) Get(scope string) (*Goal, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	g, ok := s.goals[scope]
	if !ok {
		return nil, ErrNotFound
	}
	return g.Clone(), nil
}

// Has 判断 scope 下是否存在目标。
func (s *Store) Has(scope string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.goals[scope]
	return ok
}

// IsOpen 判断 scope 下是否存在「尚未收敛」的目标。
// 无目标时返回 false —— 未立目标不应该把 Agent 锁死。
func (s *Store) IsOpen(scope string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	g, ok := s.goals[scope]
	return ok && g.IsOpen()
}

// Update 按 patch 更新目标。状态置为 blocked 时必须给出 blocker 原因。
func (s *Store) Update(scope string, p Patch) (*Goal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	g, ok := s.goals[scope]
	if !ok {
		return nil, ErrNotFound
	}
	next := g.Clone()
	if p.Title != nil {
		t := strings.TrimSpace(*p.Title)
		if t == "" {
			return nil, ErrEmptyTitle
		}
		next.Title = t
	}
	if p.Description != nil {
		next.Description = strings.TrimSpace(*p.Description)
	}
	if p.AcceptanceCriteria != nil {
		next.AcceptanceCriteria = normalizeCriteria(*p.AcceptanceCriteria)
	}
	if p.Progress != nil {
		next.Progress = strings.TrimSpace(*p.Progress)
	}
	if p.Blocker != nil {
		next.Blocker = strings.TrimSpace(*p.Blocker)
	}
	if p.Status != nil {
		next.Status = *p.Status
	}
	if next.Status == StatusBlocked && next.Blocker == "" {
		return nil, ErrBlockedNoReason
	}
	// 离开 blocked 状态时清理阻塞原因，避免陈旧信息误导 LLM。
	if next.Status != StatusBlocked && p.Blocker == nil {
		next.Blocker = ""
	}
	next.Revision = g.Revision + 1
	next.UpdatedAt = s.now()
	s.goals[scope] = next
	return next.Clone(), nil
}

// Delete 移除 scope 下的目标。
func (s *Store) Delete(scope string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.goals, scope)
}

// Len 返回当前作用域数量（测试与可观测用）。
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.goals)
}

// evictLocked 在超出上限时淘汰最久未更新的作用域。调用方需持有写锁。
func (s *Store) evictLocked() {
	for len(s.goals) > s.maxScopes {
		var oldestKey string
		var oldestAt time.Time
		first := true
		for k, v := range s.goals {
			if first || v.UpdatedAt.Before(oldestAt) {
				oldestKey, oldestAt, first = k, v.UpdatedAt, false
			}
		}
		if first {
			return
		}
		delete(s.goals, oldestKey)
	}
}

func normalizeCriteria(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, c := range in {
		c = strings.TrimSpace(c)
		if c != "" {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
