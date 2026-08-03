package toolsearch

import (
	"context"
	"io"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// fakeTool 是测试用桩工具，实现 tool.Tool + tool.CallableTool。
type fakeTool struct {
	name string
	desc string
}

func (f fakeTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        f.name,
		Description: f.desc,
		InputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"input": {Type: "string", Description: "输入"},
			},
		},
	}
}

func (f fakeTool) Call(_ context.Context, _ []byte) (any, error) { return "ok", nil }

// closerFunc 把函数适配成 io.Closer，便于测试 AddCloser/Merge/Close。
type closerFunc func() error

func (c closerFunc) Close() error { return c() }

// TestNamespacedName 验证命名空间拼接与 MCPNamespace 前缀。
func TestNamespacedName(t *testing.T) {
	if got := NamespacedName("mcp__demo", "add"); got != "mcp__demo__add" {
		t.Fatalf("NamespacedName 不符：got=%q", got)
	}
	if got := NamespacedName("", "echo"); got != "echo" {
		t.Fatalf("空命名空间应直接返回 name：got=%q", got)
	}
	if got := MCPNamespace("demo"); got != "mcp__demo" {
		t.Fatalf("MCPNamespace 不符：got=%q", got)
	}
}

// TestToolbox_AddSearchGet 验证注册、检索（关键字/空查询）、按名取回。
func TestToolbox_AddSearchGet(t *testing.T) {
	box := NewToolbox()
	box.Add(MCPNamespace("demo"), []tool.Tool{
		fakeTool{name: "add", desc: "加法运算"},
		fakeTool{name: "search_github", desc: "在 GitHub 检索代码"},
	})

	if box.Len() != 2 {
		t.Fatalf("Len 应为 2，got=%d", box.Len())
	}
	if _, ok := box.Get("mcp__demo__add"); !ok {
		t.Fatalf("按完整命名取回 mcp__demo__add 失败")
	}
	if _, ok := box.Get("add"); ok {
		t.Fatalf("裸名不应命中（必须命名空间前缀）")
	}

	// 关键字命中（命中描述中的 github）。
	hits := box.Search("github", 0)
	if len(hits) != 1 || hits[0].Name != "mcp__demo__search_github" {
		t.Fatalf("关键字 github 检索失败：%#v", hits)
	}
	// 名称命中。
	hits = box.Search("add", 0)
	if len(hits) != 1 || hits[0].Name != "mcp__demo__add" {
		t.Fatalf("名称 add 检索失败：%#v", hits)
	}
	// 空查询返回全部。
	if len(box.Search("", 0)) != 2 {
		t.Fatalf("空查询应返回全部")
	}
	// limit 截断。
	if len(box.Search("", 1)) != 1 {
		t.Fatalf("limit=1 应截断为 1")
	}
}

// TestToolbox_Merge 验证聚合多个服务器工具箱（条目 + 资源释放器）。
func TestToolbox_Merge(t *testing.T) {
	a := NewToolbox()
	a.Add(MCPNamespace("a"), []tool.Tool{fakeTool{name: "x", desc: "x"}})
	b := NewToolbox()
	b.Add(MCPNamespace("b"), []tool.Tool{fakeTool{name: "y", desc: "y"}})
	b.AddCloser(closerFunc(func() error { return nil }))

	a.Merge(b)
	if a.Len() != 2 {
		t.Fatalf("Merge 后应为 2 条，got=%d", a.Len())
	}
	if _, ok := a.Get("mcp__b__y"); !ok {
		t.Fatalf("Merge 后未包含 mcp__b__y")
	}
}

// TestToolbox_Close 验证资源释放器被调用。
func TestToolbox_Close(t *testing.T) {
	box := NewToolbox()
	called := false
	box.AddCloser(closerFunc(func() error { called = true; return nil }))
	if err := box.Close(); err != nil {
		t.Fatalf("Close 不应报错：%v", err)
	}
	if !called {
		t.Fatalf("Close 未调用已注册的资源释放器")
	}
	// 幂等：再次 Close 不应再次调用（closers 已清空）。
	called = false
	_ = box.Close()
	if called {
		t.Fatalf("Close 不应幂等重复调用释放器")
	}
}

// ensure io import used（io.Closer 在 Toolbox 内部使用，测试包需保持一致）。
var _ io.Closer = closerFunc(nil)
