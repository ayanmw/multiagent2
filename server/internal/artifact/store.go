// Package artifact 实现 M1-16「工作状态外置」的 artifact 存储层。
//
// 长任务（如 Goal 循环 / Plan-Execute / 24h 自主推进 Loop）维护三种状态文件：
//
//	PLAN.md      —— 计划 / 目标
//	PROGRESS.md  —— 进展日志
//	LEARNINGS.md —— 踩坑与约定沉淀
//
// 这些文件按「每次 run / 每个 session」的作用域隔离，落到磁盘后，
// 即便进程重启或被中断，下一次 run 也能把它们读回来续跑。
//
// 关键边界：本包的 artifact 是**每次 run 下的状态文件**，与仓库根目录
// docs/loop/PLAN.md 等「LOOP 循环控制文件」**完全无关**，Agent 写入时
// 不得触碰 docs/loop/（见 PLAN.md「M1-16 命名消歧」）。
//
// 设计：Store 接口与具体后端解耦，当前提供 FileStore（落盘，跨重启续跑）
// 与 MemoryStore（纯内存，作为安全默认 / 测试用）。本包不依赖任何框架。
package artifact

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// 三种状态文件的规范名（artifact，每次 run 维护）。
const (
	PlanArtifact     = "PLAN.md"
	ProgressArtifact = "PROGRESS.md"
	LearningsArtifact = "LEARNINGS.md"
)

// Store 是 artifact 的存储接口：按 key（通常为 session 作用域）隔离，按 name（文件名）存取。
// 实现必须保证并发安全。
type Store interface {
	// Write 把 named artifact 的内容写入指定 key 作用域（覆盖写）。
	Write(key, name, content string) error
	// Read 读取 named artifact；存在时 ok=true，不存在 ok=false 且 err=nil。
	Read(key, name string) (content string, ok bool, err error)
	// Exists 报告 named artifact 是否存在。
	Exists(key, name string) (bool, error)
	// List 列出某 key 作用域下的全部 artifact 名。
	List(key string) ([]string, error)
	// Remove 删除单个 named artifact。
	Remove(key, name string) error
	// RemoveAll 删除某 key 作用域下的全部 artifact（清理整个 run 的状态）。
	RemoveAll(key string) error
	// Snapshot 同时读取三种状态文件，便于「续跑前先读状态」。
	Snapshot(key string) (Snapshot, error)
}

// Snapshot 是一次对三种状态文件的同时读取结果。
type Snapshot struct {
	Plan      string
	Progress  string
	Learnings string
	// Any 报告是否任一项存在（用于「是否有可续跑的状态」判断）。
	Any bool
}

// stateNames 是 Snapshot 需要同时读取的三个 artifact 名。
var stateNames = []string{PlanArtifact, ProgressArtifact, LearningsArtifact}

// FileStore 是基于文件系统的 Store 实现：root/<sanitizedKey>/<name>。
// 它把状态文件落到磁盘，使进程重启 / 中断后续跑时仍能读回（M1-16 验收）。
type FileStore struct {
	root string
	mu   sync.RWMutex
}

// NewFileStore 构造落盘存储，root 为状态文件根目录（不存在则创建）。
func NewFileStore(root string) (*FileStore, error) {
	if root == "" {
		return nil, fmt.Errorf("artifact: root 不能为空")
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		return nil, fmt.Errorf("artifact: 创建根目录失败: %w", err)
	}
	return &FileStore{root: root}, nil
}

// Root 返回状态文件根目录（只读）。
func (s *FileStore) Root() string { return s.root }

func (s *FileStore) dir(key string) (string, error) {
	k := sanitizeKey(key)
	if k == "" {
		return "", fmt.Errorf("artifact: key 非法")
	}
	d := filepath.Join(s.root, k)
	if !withinRoot(s.root, d) {
		return "", fmt.Errorf("artifact: key 越界")
	}
	return d, nil
}

// Write 实现 Store。
func (s *FileStore) Write(key, name, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.dir(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(d, 0755); err != nil {
		return err
	}
	fp, err := safePath(d, name)
	if err != nil {
		return err
	}
	return os.WriteFile(fp, []byte(content), 0644)
}

// Read 实现 Store。
func (s *FileStore) Read(key, name string) (string, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, err := s.dir(key)
	if err != nil {
		return "", false, err
	}
	fp, err := safePath(d, name)
	if err != nil {
		return "", false, err
	}
	data, err := os.ReadFile(fp)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	return string(data), true, nil
}

// Exists 实现 Store。
func (s *FileStore) Exists(key, name string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, err := s.dir(key)
	if err != nil {
		return false, err
	}
	fp, err := safePath(d, name)
	if err != nil {
		return false, err
	}
	info, err := os.Stat(fp)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return !info.IsDir(), nil
}

// List 实现 Store。
func (s *FileStore) List(key string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, err := s.dir(key)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(d)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// Remove 实现 Store。
func (s *FileStore) Remove(key, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.dir(key)
	if err != nil {
		return err
	}
	fp, err := safePath(d, name)
	if err != nil {
		return err
	}
	if err := os.Remove(fp); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return nil
}

// RemoveAll 实现 Store。
func (s *FileStore) RemoveAll(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.dir(key)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(d); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return nil
}

// Snapshot 实现 Store：同时读取三种状态文件。
func (s *FileStore) Snapshot(key string) (Snapshot, error) {
	var snap Snapshot
	for _, name := range stateNames {
		c, ok, err := s.Read(key, name)
		if err != nil {
			return snap, err
		}
		if !ok {
			continue
		}
		switch name {
		case PlanArtifact:
			snap.Plan = c
		case ProgressArtifact:
			snap.Progress = c
		case LearningsArtifact:
			snap.Learnings = c
		}
		snap.Any = true
	}
	return snap, nil
}

// ---------------------------------------------------------------------------
// 内存后端（安全默认 / 测试用，不落盘）
// ---------------------------------------------------------------------------

// MemoryStore 是纯内存 Store 实现，进程退出即丢失，仅作为
// 「未配置落盘根目录」时的安全默认，以及单元测试使用。
type MemoryStore struct {
	mu sync.RWMutex
	m  map[string]map[string]string // key -> name -> content
}

// NewMemoryStore 构造内存存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{m: make(map[string]map[string]string)}
}

func (s *MemoryStore) write(key, name, content string) error {
	if s.m[key] == nil {
		s.m[key] = make(map[string]string)
	}
	s.m[key][name] = content
	return nil
}

// Write 实现 Store。
func (s *MemoryStore) Write(key, name, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.write(key, name, content)
}

// Read 实现 Store。
func (s *MemoryStore) Read(key, name string) (string, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.m[key] == nil {
		return "", false, nil
	}
	c, ok := s.m[key][name]
	return c, ok, nil
}

// Exists 实现 Store。
func (s *MemoryStore) Exists(key, name string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.m[key] == nil {
		return false, nil
	}
	_, ok := s.m[key][name]
	return ok, nil
}

// List 实现 Store。
func (s *MemoryStore) List(key string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.m[key] == nil {
		return nil, nil
	}
	names := make([]string, 0, len(s.m[key]))
	for n := range s.m[key] {
		names = append(names, n)
	}
	return names, nil
}

// Remove 实现 Store。
func (s *MemoryStore) Remove(key, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m[key] != nil {
		delete(s.m[key], name)
	}
	return nil
}

// RemoveAll 实现 Store。
func (s *MemoryStore) RemoveAll(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, key)
	return nil
}

// Snapshot 实现 Store。
func (s *MemoryStore) Snapshot(key string) (Snapshot, error) {
	var snap Snapshot
	for _, name := range stateNames {
		c, ok, _ := s.Read(key, name)
		if !ok {
			continue
		}
		switch name {
		case PlanArtifact:
			snap.Plan = c
		case ProgressArtifact:
			snap.Progress = c
		case LearningsArtifact:
			snap.Learnings = c
		}
		snap.Any = true
	}
	return snap, nil
}

// ---------------------------------------------------------------------------
// 安全辅助：文件名 / 作用域键规范化，杜绝路径穿越
// ---------------------------------------------------------------------------

// sanitizeKey 把作用域键规范为安全的目录名：
// 仅保留字母数字与 - _，其余（含 : / \ 等）一律置为 _。
// 例：sess:abc123 → sess_abc123（Windows 不接受文件名中的 :）。
func sanitizeKey(k string) string {
	k = strings.TrimSpace(k)
	if k == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range k {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// sanitizeName 把文件名规范为单段文件名，禁止任何形式的路径穿越。
// 仅保留字母数字与 - _ .，其余一律拒绝（如中文文件名、空格均报错），
// 以换取确定的安全性。
func sanitizeName(n string) (string, error) {
	n = strings.TrimSpace(n)
	if n == "" {
		return "", fmt.Errorf("artifact: 文件名不能为空")
	}
	if strings.ContainsAny(n, "/\\") || n == ".." || strings.HasPrefix(n, "..") {
		return "", fmt.Errorf("artifact: 文件名非法: %q", n)
	}
	var b strings.Builder
	for _, r := range n {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			return "", fmt.Errorf("artifact: 文件名含非法字符: %q", n)
		}
	}
	if b.Len() == 0 {
		return "", fmt.Errorf("artifact: 文件名非法: %q", n)
	}
	return b.String(), nil
}

// safePath 在目录 d 内拼出 name 的最终路径，并确保不越出 d。
func safePath(d, name string) (string, error) {
	clean, err := sanitizeName(name)
	if err != nil {
		return "", err
	}
	fp := filepath.Join(d, clean)
	if !withinRoot(d, fp) {
		return "", fmt.Errorf("artifact: 路径越界")
	}
	return fp, nil
}

// withinRoot 报告 target 是否位于 root 之内（不允许用 .. 逃逸）。
func withinRoot(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}
