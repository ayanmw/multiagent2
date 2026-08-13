// Package skillrepo 实现「用户归属的技能仓库」管理与「技能 warm-start」注入
// （M2-03 Skills 仓库 & warm-start）。
//
// 设计要点：
//   - 复用框架 trpc.group/trpc-go/trpc-agent-go/skill 的 FSRepository：技能是「含 SKILL.md
//     的目录」，SKILL.md 含可选 YAML front matter（name/description）+ Markdown 正文。
//   - 两套根目录（对齐 docs/loop PLAN.md M2-03）：
//     1) 共享技能根 sharedRoot（如仓库内 skills/，通常由内置/管理员提供，只读）；
//     2) 用户私有技能根 dataDir/<uid>/（经 API 增删改，owner 隔离，不串户）。
//   - 管理 API（列/读/建/更新/删）全部落在文件系统，owner 隔离：私有技能写在
//     dataDir/<uid>/<name>/SKILL.md，共享技能只能经文件/管理员维护，API 不可改写。
//   - warm-start：会话开始时把 [sharedRoot, 用户私有根] 交给 FSRepository 扫描，按关键词
//     检索相关技能并渲染成系统上下文片段注入根 Agent；「控长」由 maxChars 上限保证，
//     避免技能数膨胀撑爆上下文。
//
// 本包不依赖 DB / 框架 engine，可独立单元测试（不需 CGO）。
package skillrepo

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/skill"
)

// DefaultWarmStartMaxChars 是 warm-start 注入系统上下文的默认长度上限（控长）。
const DefaultWarmStartMaxChars = 6000

// ScopeShared / ScopePrivate 标记技能来源。
const (
	ScopeShared  = "shared"
	ScopePrivate = "private"
)

// skillNameRe 限制技能名仅含字母数字下划线连字符，杜绝路径穿越。
var skillNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// Summary 是技能列表项的精简视图（比框架 skill.Summary 多一个来源/只读标记）。
type Summary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Scope       string `json:"scope"`
	ReadOnly    bool   `json:"read_only"` // 共享技能不可经 API 改写
}

// Detail 是技能完整内容（含 SKILL.md body）。
type Detail struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Scope       string `json:"scope"`
	ReadOnly    bool   `json:"read_only"`
	Body        string `json:"body"`
}

// Manager 管理用户归属的技能仓库（文件系统后端）。
type Manager struct {
	sharedRoot string
	dataDir    string
}

// NewManager 构造技能管理器。sharedRoot 为共享技能根（只读），
// dataDir 为用户私有技能根（其下按 <uid> 分子目录）。
func NewManager(sharedRoot, dataDir string) *Manager {
	return &Manager{sharedRoot: sharedRoot, dataDir: dataDir}
}

// userDir 返回某用户的私有技能根目录。
func (m *Manager) userDir(uid string) string {
	return filepath.Join(m.dataDir, sanitizeSegment(uid))
}

// ensureDir 确保目录存在（warm-start 扫描要求根存在，否则 FSRepository 报错）。
func ensureDir(dir string) error {
	if dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}

// privateRepo 构造仅含该用户私有技能的 FSRepository。
func (m *Manager) privateRepo(uid string) (*skill.FSRepository, error) {
	if err := ensureDir(m.userDir(uid)); err != nil {
		return nil, err
	}
	return skill.NewFSRepository(m.userDir(uid))
}

// sharedRepo 构造仅含共享技能的 FSRepository（根不存在时降级为空仓库）。
func (m *Manager) sharedRepo() (*skill.FSRepository, error) {
	if err := ensureDir(m.sharedRoot); err != nil {
		return nil, err
	}
	return skill.NewFSRepository(m.sharedRoot)
}

// List 返回当前用户可见的全部技能（共享 + 私有），共享为只读。
func (m *Manager) List(uid string) ([]Summary, error) {
	var out []Summary
	if sr, err := m.sharedRepo(); err == nil {
		for _, s := range sr.Summaries() {
			out = append(out, Summary{Name: s.Name, Description: s.Description, Scope: ScopeShared, ReadOnly: true})
		}
	}
	if pr, err := m.privateRepo(uid); err == nil {
		for _, s := range pr.Summaries() {
			out = append(out, Summary{Name: s.Name, Description: s.Description, Scope: ScopePrivate, ReadOnly: false})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Scope != out[j].Scope {
			// 私有（用户自己的）排在共享前面，更贴合「我的技能优先」。
			return out[i].Scope == ScopePrivate
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// Get 读取技能完整内容：私有优先，其次共享；都不存在报错。
func (m *Manager) Get(uid, name string) (*Detail, error) {
	if !ValidSkillName(name) {
		return nil, fmt.Errorf("invalid skill name %q", name)
	}
	if pr, err := m.privateRepo(uid); err == nil {
		if sk, gerr := pr.Get(name); gerr == nil {
			return &Detail{Name: sk.Summary.Name, Description: sk.Summary.Description, Scope: ScopePrivate, ReadOnly: false, Body: sk.Body}, nil
		}
	}
	if sr, err := m.sharedRepo(); err == nil {
		if sk, gerr := sr.Get(name); gerr == nil {
			return &Detail{Name: sk.Summary.Name, Description: sk.Summary.Description, Scope: ScopeShared, ReadOnly: true, Body: sk.Body}, nil
		}
	}
	return nil, fmt.Errorf("skill %q not found", name)
}

// PublishShared 把一份技能「发布」到共享技能库（只读、warm-start 可见）。
//
// 用于 evolution 飞轮审批通过后，把候选技能固化为托管技能（写 <sharedRoot>/<name>/SKILL.md）。
// 已存在的同名共享技能会被覆盖（发布即替换，属于管理员的明确决策）；name 须合法
// （仅 [A-Za-z0-9_-]，防路径穿越）；sharedRoot 未配置时返回错误。
func (m *Manager) PublishShared(name, body string) error {
	if !ValidSkillName(name) {
		return fmt.Errorf("invalid skill name %q", name)
	}
	if m.sharedRoot == "" {
		return fmt.Errorf("shared skills root not configured")
	}
	dir := filepath.Join(m.sharedRoot, sanitizeSegment(name))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, skill.SkillFile), []byte(body), 0o644)
}

// RemoveShared 从共享技能库移除一份已发布的技能（M5-08 回归门禁回滚用）。
// 不存在时返回 nil（幂等，便于回滚安全调用）；sharedRoot 未配置时返回错误。
func (m *Manager) RemoveShared(name string) error {
	if !ValidSkillName(name) {
		return fmt.Errorf("invalid skill name %q", name)
	}
	if m.sharedRoot == "" {
		return fmt.Errorf("shared skills root not configured")
	}
	dir := filepath.Join(m.sharedRoot, sanitizeSegment(name))
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return os.RemoveAll(dir)
}

// Create 在用户私有目录新建/覆盖技能。body 即 SKILL.md 全部内容（允许为空占位）。
// 共享技能不可经 API 创建/覆盖（只读）。
func (m *Manager) Create(uid, name, body string) error {
	if !ValidSkillName(name) {
		return fmt.Errorf("invalid skill name %q", name)
	}
	dir := filepath.Join(m.userDir(uid), sanitizeSegment(name))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, skill.SkillFile), []byte(body), 0o644)
}

// Update 更新用户私有技能（必须已存在，否则 404）。
func (m *Manager) Update(uid, name, body string) error {
	if !ValidSkillName(name) {
		return fmt.Errorf("invalid skill name %q", name)
	}
	dir := filepath.Join(m.userDir(uid), sanitizeSegment(name))
	if _, err := os.Stat(filepath.Join(dir, skill.SkillFile)); err != nil {
		return fmt.Errorf("skill %q not found", name)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, skill.SkillFile), []byte(body), 0o644)
}

// Delete 删除用户私有技能（仅私有；共享不可经 API 删除）。不存在返回错误。
func (m *Manager) Delete(uid, name string) error {
	if !ValidSkillName(name) {
		return fmt.Errorf("invalid skill name %q", name)
	}
	dir := filepath.Join(m.userDir(uid), sanitizeSegment(name))
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("skill %q not found", name)
	}
	return os.RemoveAll(dir)
}

// WarmStartBlock 生成 warm-start 系统上下文片段（按 uid 计算私有根后扫描）。
// 详见 WarmStartBlockRoots：本方法先把 [sharedRoot, 用户私有根] 作为 roots 传入。
func (m *Manager) WarmStartBlock(uid string, keywords []string, maxChars int) (string, error) {
	roots := []string{}
	if err := ensureDir(m.sharedRoot); err == nil {
		roots = append(roots, m.sharedRoot)
	}
	if err := ensureDir(m.userDir(uid)); err == nil {
		roots = append(roots, m.userDir(uid))
	}
	if len(roots) == 0 {
		return "", nil
	}
	return WarmStartBlockRoots(roots, keywords, maxChars)
}

// WarmStartBlockRoots 是 warm-start 的核心：扫描给定 roots，列出全部可用技能，
// 若提供关键词则仅保留名称/描述命中者；然后按长度上限注入命中技能的完整内容。
// 返回空串表示无可用技能（调用方据此跳过注入，不污染系统上下文）。
func WarmStartBlockRoots(roots []string, keywords []string, maxChars int) (string, error) {
	if maxChars <= 0 {
		maxChars = DefaultWarmStartMaxChars
	}
	// 确保各根存在：FSRepository 在根缺失时会报 walk 错误，这里用 MkdirAll 兜底。
	clean := make([]string, 0, len(roots))
	for _, r := range roots {
		if r == "" {
			continue
		}
		if err := ensureDir(r); err != nil {
			continue
		}
		clean = append(clean, r)
	}
	if len(clean) == 0 {
		return "", nil
	}
	repo, err := skill.NewFSRepository(clean...)
	if err != nil {
		return "", err
	}
	summaries := repo.Summaries()
	if len(summaries) == 0 {
		return "", nil
	}
	selected := filterByKeywords(summaries, keywords)

	// 紧凑索引（全部可用技能，限制条数防极端膨胀）。
	var idx strings.Builder
	const idxLimit = 100
	for i, s := range summaries {
		if i >= idxLimit {
			break
		}
		idx.WriteString(fmt.Sprintf("- %s: %s\n", s.Name, s.Description))
	}

	// 详细内容（仅 selected），按长度上限裁剪（控长）。
	var details strings.Builder
	for _, s := range selected {
		sk, gerr := repo.Get(s.Name)
		if gerr != nil {
			continue
		}
		block := fmt.Sprintf("### Skill: %s\n%s\n\n%s\n", s.Name, s.Description, sk.Body)
		if details.Len()+len(block) > maxChars {
			details.WriteString("\n（更多相关技能因上下文长度上限未展开。）\n")
			break
		}
		details.WriteString(block)
	}

	var b strings.Builder
	b.WriteString("\n\n【可用技能 Skills（warm-start）】\n")
	b.WriteString("以下技能可用于辅助本次任务；需要其完整说明时请依据名称参考，不要臆测技能内容。\n")
	b.WriteString("\n## 全部可用技能索引\n")
	b.WriteString(idx.String())
	if details.Len() > 0 {
		b.WriteString("\n## 与本次任务相关的技能详情\n")
		b.WriteString(details.String())
	}
	return b.String(), nil
}

// filterByKeywords 按关键词（名称/描述子串，大小写不敏感）过滤技能摘要；
// 无关键词时返回全部（由调用方/长度上限兜底控制注入量）。
func filterByKeywords(summaries []skill.Summary, keywords []string) []skill.Summary {
	if len(keywords) == 0 {
		return summaries
	}
	var out []skill.Summary
	for _, s := range summaries {
		hay := strings.ToLower(s.Name + " " + s.Description)
		for _, kw := range keywords {
			if kw == "" {
				continue
			}
			if strings.Contains(hay, strings.ToLower(kw)) {
				out = append(out, s)
				break
			}
		}
	}
	return out
}

// ValidSkillName 校验技能名是否合法（仅 [A-Za-z0-9_-]，杜绝路径穿越）。
func ValidSkillName(name string) bool {
	return skillNameRe.MatchString(name)
}

// sanitizeSegment 清洗路径片段（uid/name），仅保留安全字符，杜绝穿越。
// 技能名已用 ValidSkillName 校验，这里对 uid 等做二次兜底。
func sanitizeSegment(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "..", "")
	s = strings.ReplaceAll(s, "/", "")
	s = strings.ReplaceAll(s, "\\", "")
	if s == "" {
		return "_"
	}
	return s
}
