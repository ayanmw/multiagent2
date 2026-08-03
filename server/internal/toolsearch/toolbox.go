// Package toolsearch 实现「延迟工具箱」（M2-06）：把 MCP 服务器提供的工具、内置工具等
// 注册为命名空间工具箱，但默认不把它们直接暴露给 Agent；而是额外挂两个控制工具
// tool_search（按名称/关键字检索）与 call_tool（按需调用），让模型「先检索、再调用」。
// 这样既保留了海量工具的可达性，又避免把所有工具的声明一次性灌进模型上下文导致 token
// 随工具数量线性膨胀（用户 2026-08-03 确认：命名空间/关键字检索，非语义嵌入）。
package toolsearch

import (
	"io"
	"sort"
	"strings"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// NamespaceSeparator 是命名空间与工具名之间的分隔符。
// 例如 MCP 服务器 demo 的工具 add 命名为 "mcp__demo__add"。
const NamespaceSeparator = "__"

// MCPNamespacePrefix 是 MCP 工具的命名空间前缀，配合服务器名构成 "mcp__<server>"。
const MCPNamespacePrefix = "mcp"

// NamespacedName 拼接命名空间前缀得到完整工具名；namespace 为空则直接返回 name。
func NamespacedName(namespace, name string) string {
	if namespace == "" {
		return name
	}
	return namespace + NamespaceSeparator + name
}

// MCPNamespace 返回某个 MCP 服务器的命名空间（mcp__<server>）。
func MCPNamespace(serverName string) string {
	return NamespacedName(MCPNamespacePrefix, serverName)
}

// ToolEntry 是工具箱中注册的一条工具记录。
type ToolEntry struct {
	Namespace   string    `json:"namespace"`   // 命名空间，如 mcp__demo
	Name        string    `json:"name"`        // 完整命名（含命名空间），call_tool 用此名
	ToolName    string    `json:"tool_name"`   // 工具在源服务器上的原始名
	Description string    `json:"description"` // 工具描述（检索匹配用）
	Tool        tool.Tool `json:"-"`           // 真实工具实例（不序列化）
}

// Toolbox 是延迟工具箱：持有真实工具实例，支持按名称/关键字检索与按需调用，
// 工具本身不直接注册到 Agent（避免上下文随工具数线性膨胀）。
type Toolbox struct {
	mu      sync.RWMutex
	entries map[string]ToolEntry
	closers []io.Closer
}

// NewToolbox 创建一个空工具箱。
func NewToolbox() *Toolbox {
	return &Toolbox{entries: make(map[string]ToolEntry)}
}

// Add 把一批工具注册到指定命名空间（如 "mcp__demo"）。nil 或无声明的工具会被跳过。
func (b *Toolbox) Add(namespace string, tools []tool.Tool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, t := range tools {
		if t == nil || t.Declaration() == nil {
			continue
		}
		decl := t.Declaration()
		name := NamespacedName(namespace, decl.Name)
		b.entries[name] = ToolEntry{
			Namespace:   namespace,
			Name:        name,
			ToolName:    decl.Name,
			Description: decl.Description,
			Tool:        t,
		}
	}
}

// AddCloser 注册一个资源释放器（如 MCP ToolSet 连接），在 Close 时统一调用。
func (b *Toolbox) AddCloser(c io.Closer) {
	if c == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closers = append(b.closers, c)
}

// Merge 把另一个工具箱的全部条目与资源释放器并入自身（用于聚合多个 MCP 服务器的工具）。
func (b *Toolbox) Merge(other *Toolbox) {
	if other == nil {
		return
	}
	other.mu.RLock()
	defer other.mu.RUnlock()
	b.mu.Lock()
	defer b.mu.Unlock()
	for k, e := range other.entries {
		b.entries[k] = e
	}
	b.closers = append(b.closers, other.closers...)
}

// Get 按完整命名取回工具实例；未找到返回 (nil, false)。
func (b *Toolbox) Get(namespacedName string) (tool.Tool, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	e, ok := b.entries[namespacedName]
	if !ok {
		return nil, false
	}
	return e.Tool, true
}

// Len 返回当前注册的工具数量。
func (b *Toolbox) Len() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.entries)
}

// List 返回全部工具条目（拷贝，避免外部并发修改内部 map）。
func (b *Toolbox) List() []ToolEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]ToolEntry, 0, len(b.entries))
	for _, e := range b.entries {
		out = append(out, e)
	}
	return out
}

// Search 按名称或描述（含命名空间前缀）模糊匹配；query 为空则返回全部。
// limit<=0 表示不限制；结果按 Name 稳定排序，便于模型稳定消费。
func (b *Toolbox) Search(query string, limit int) []ToolEntry {
	q := strings.ToLower(strings.TrimSpace(query))
	all := b.List()
	matched := make([]ToolEntry, 0, len(all))
	for _, e := range all {
		if q == "" {
			matched = append(matched, e)
			continue
		}
		if strings.Contains(strings.ToLower(e.Name), q) || strings.Contains(strings.ToLower(e.Description), q) {
			matched = append(matched, e)
		}
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].Name < matched[j].Name })
	if limit > 0 && len(matched) > limit {
		matched = matched[:limit]
	}
	return matched
}

// Close 释放所有已注册的资源（如 MCP 连接），幂等。
func (b *Toolbox) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	var firstErr error
	for _, c := range b.closers {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	b.closers = nil
	return firstErr
}
