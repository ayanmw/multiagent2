package toolsearch

import (
	"context"
	"encoding/json"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// echoTool 是一个可被真实调用的测试工具，按 input 回显。
type echoTool struct {
	name string
	desc string
}

func (e echoTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: e.name, Description: e.desc}
}

func (e echoTool) Call(_ context.Context, jsonArgs []byte) (any, error) {
	var m map[string]any
	_ = json.Unmarshal(jsonArgs, &m)
	return "ECHO:" + e.name + ":" + toString(m["input"]), nil
}

func toString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// TestNewToolSearch 验证 tool_search 检索并只返回「完整命名 + 描述」JSON。
func TestNewToolSearch(t *testing.T) {
	box := NewToolbox()
	box.Add(MCPNamespace("demo"), []tool.Tool{
		echoTool{name: "add", desc: "加法"},
		echoTool{name: "sub", desc: "减法"},
	})
	search := NewToolSearch(box)
	ct, ok := search.(tool.CallableTool)
	if !ok {
		t.Fatalf("tool_search 必须实现 CallableTool")
	}
	// 关键字检索。
	out, err := ct.Call(context.Background(), mustJSONBytes(map[string]any{"query": "add", "limit": 0}))
	if err != nil {
		t.Fatalf("tool_search 调用失败：%v", err)
	}
	var items []searchResultItem
	if err := json.Unmarshal([]byte(out.(string)), &items); err != nil {
		t.Fatalf("tool_search 返回非预期 JSON：%v / %v", out, err)
	}
	if len(items) != 1 || items[0].Name != "mcp__demo__add" {
		t.Fatalf("tool_search 检索结果不符：%#v", items)
	}
	// 空查询返回全部。
	out, _ = ct.Call(context.Background(), mustJSONBytes(map[string]any{"query": "", "limit": 0}))
	var all []searchResultItem
	_ = json.Unmarshal([]byte(out.(string)), &all)
	if len(all) != 2 {
		t.Fatalf("空查询应返回 2 条，got=%d", len(all))
	}
}

// TestNewCallTool 验证 call_tool 按完整命名取出真实工具并执行，返回结果为字符串。
func TestNewCallTool(t *testing.T) {
	box := NewToolbox()
	box.Add(MCPNamespace("demo"), []tool.Tool{echoTool{name: "add", desc: "加法"}})
	call := NewCallTool(box)
	ct, ok := call.(tool.CallableTool)
	if !ok {
		t.Fatalf("call_tool 必须实现 CallableTool")
	}
	// 正常调用。
	if out, err := ct.Call(context.Background(), mustJSONBytes(map[string]any{"name": "mcp__demo__add", "arguments": `{"input":"42"}`})); err != nil {
		t.Fatalf("call_tool 调用失败：%v", err)
	} else if s, _ := out.(string); s != "ECHO:add:42" {
		t.Fatalf("call_tool 返回不符：got=%q", s)
	}
	// 未找到的工具应报错（提示先检索）。
	if _, err := ct.Call(context.Background(), mustJSONBytes(map[string]any{"name": "mcp__nope__x", "arguments": "{}"})); err == nil {
		t.Fatalf("调用不存在的工具应报错")
	}
}

// mustJSONBytes 把任意值序列化为 JSON 字节（用作工具调用的参数）。
func mustJSONBytes(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
