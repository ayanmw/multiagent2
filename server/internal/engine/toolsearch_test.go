package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"

	"github.com/ayanmw/multiagent2/server/internal/toolsearch"
)

// mockAddTool 是一个可被真实调用的「MCP 风格」工具（模拟经 MCP 加载的 add 工具）。
// 它本身不直接暴露给模型，而是经延迟工具箱（mcp__demo__add）由 call_tool 按需调用。
type mockAddToolInput struct {
	A int `json:"a"`
	B int `json:"b"`
}

func mockAddTool() tool.Tool {
	return function.NewFunctionTool(
		func(_ context.Context, in mockAddToolInput) (string, error) {
			return fmt.Sprintf("RESULT:%d", in.A+in.B), nil
		},
		function.WithName("add"),
		function.WithDescription("两个数相加"),
	)
}

// mockToolSearchServer 模拟 OpenAI 流式端点，纯脚本化驱动
// 「tool_search 检索 → call_tool 调用 → 结果返回」三跳链路，不调用真实 LLM。
// 用调用次数确定性分支（与框架回传给 LLM 的消息格式无关），避免脆弱的消息解析。
func mockToolSearchServer() (*httptest.Server, *bool) {
	var calls int
	// sawDirectTool 记录模型请求里是否出现过被延迟的真实 MCP 工具（mcp__demo__add）
	// 直接挂载——验收要求它不应直接出现在工具清单（否则上下文随工具数线性膨胀）。
	var sawDirectTool bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var req struct {
			Messages []map[string]any `json:"messages"`
			Tools    []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}
		_ = json.Unmarshal(raw, &req)
		for _, tl := range req.Tools {
			if tl.Function.Name == toolsearch.MCPNamespace("demo")+"__add" {
				sawDirectTool = true
			}
		}
		calls++

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		f, _ := w.(http.Flusher)

		switch {
		case calls == 1:
			// 首轮：模型调用 tool_search 检索可用工具。
			writeSSEToolCall(w, f, "tool_search", mustJSON(map[string]any{"query": "add", "limit": 0}))
		case calls == 2:
			// 第二轮：根据第一轮检索结果，调用 call_tool 执行 mcp__demo__add。
			writeSSEToolCall(w, f, "call_tool",
				mustJSON(map[string]any{"name": toolsearch.MCPNamespace("demo") + "__add", "arguments": `{"a":2,"b":3}`}))
		default:
			// 第三轮：拿到 call_tool 结果，给出最终回复（把 RESULT 回灌以证明链路打通）。
			writeSSEText(w, f, "调用结果："+lastToolResult(req.Messages))
		}
	}))
	return srv, &sawDirectTool
}

// lastToolResult 返回最后一条 tool 消息的内容（即 call_tool 的真实返回结果）。
func lastToolResult(msgs []map[string]any) string {
	last := ""
	for _, m := range msgs {
		if m["role"] != "tool" {
			continue
		}
		if c, ok := m["content"].(string); ok {
			last = c
		}
	}
	return last
}

// TestEngine_ToolSearch_LazyInvoke 验证 M2-06 核心验收：
// 延迟工具箱开启后，引擎只挂载 tool_search/call_tool 双控制工具（不把 MCP 工具声明灌进上下文），
// 模型先 tool_search 检索到 mcp__demo__add，再 call_tool 按需执行，最终拿到真实结果。
// 全程不调用真实 LLM，用 mockToolSearchServer 脚本化驱动工具调用序列。
func TestEngine_ToolSearch_LazyInvoke(t *testing.T) {
	srv, sawDirectTool := mockToolSearchServer()
	defer srv.Close()

	// provider 返回一个含 MCP 风格工具（mcp__demo__add）的工具箱；真实接入时由
	// toolsearch.LoadMCPServerTools 连接 MCP 服务器填充，这里用 mock 工具等价代替。
	provider := func(_ context.Context, _ uint) (*toolsearch.Toolbox, error) {
		box := toolsearch.NewToolbox()
		box.Add(toolsearch.MCPNamespace("demo"), []tool.Tool{mockAddTool()})
		return box, nil
	}

	eng, err := New(ModelConfig{
		ModelID:            "mock-model",
		BaseURL:            srv.URL,
		APIKey:             "test-key",
		Protocol:           "openai",
		ToolSearchEnabled:  true,
		ToolSearchProvider: provider,
		ToolSearchUserID:   1,
	})
	if err != nil {
		t.Fatalf("New engine with tool search failed: %v", err)
	}
	defer eng.Close()

	reply, err := eng.Chat(context.Background(), "sess-toolsearch", "请使用工具计算 2+3", nil)
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}
	// call_tool 的真实结果 RESULT:5 应经由 mock LLM 的最终回复回灌，证明链路打通。
	if !strings.Contains(reply, "RESULT:5") {
		t.Fatalf("call_tool 未返回预期结果（应为 RESULT:5）；reply=%q", reply)
	}
	// 验收「context 不随 MCP 工具数线性膨胀」：模型全程只看到 tool_search/call_tool
	// 两个控制工具，被延迟的真实 MCP 工具（mcp__demo__add）从不直接进入工具清单。
	if *sawDirectTool {
		t.Fatalf("被延迟的 MCP 工具 mcp__demo__add 被直接挂载进模型上下文（应仅经控制工具按需调用）")
	}
	t.Logf("✅ 延迟工具箱链路打通：tool_search 找到 mcp__demo__add → call_tool 执行 → 返回 %q；真实 MCP 工具未直接进上下文", reply)
}

// TestEngine_ToolSearch_DisabledMountsNothing 验证：关闭 ToolSearch 时引擎不挂载
// tool_search/call_tool 双控制工具（避免无谓的上下文占用），provider 不被调用。
func TestEngine_ToolSearch_DisabledMountsNothing(t *testing.T) {
	srv, _ := mockToolSearchServer()
	defer srv.Close()

	called := false
	provider := func(_ context.Context, _ uint) (*toolsearch.Toolbox, error) {
		called = true
		box := toolsearch.NewToolbox()
		box.Add(toolsearch.MCPNamespace("demo"), []tool.Tool{mockAddTool()})
		return box, nil
	}

	eng, err := New(ModelConfig{
		ModelID:            "mock-model",
		BaseURL:            srv.URL,
		APIKey:             "test-key",
		Protocol:           "openai",
		ToolSearchEnabled:  false, // 关闭
		ToolSearchProvider: provider,
		ToolSearchUserID:   1,
	})
	if err != nil {
		t.Fatalf("New engine failed: %v", err)
	}
	defer eng.Close()

	if called {
		t.Fatalf("ToolSearch 关闭时不应调用 provider")
	}
	if eng.toolbox != nil {
		t.Fatalf("ToolSearch 关闭时引擎不应持有 toolbox")
	}
	t.Log("✅ ToolSearch 关闭时引擎不挂载双控制工具、不调用 provider")
}
