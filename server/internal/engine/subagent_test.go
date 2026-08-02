package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mockDelegationServer 模拟 OpenAI 流式端点，按「请求里的工具清单」判定当前是
// Orchestrator（含 coder 工具）还是 Coder（含 file_write 工具）在调用 LLM：
//   - 若对话里已出现 role=tool 的消息（上一轮已经调用过工具）→ 返回纯文本总结，结束本轮；
//   - Orchestrator 首轮：返回对 coder 工具的 tool_call，request 描述「创建 hello.txt」；
//   - Coder 首轮：返回对 file_write 工具的 tool_call，真正在工作目录写入文件。
//
// 全程不依赖真实 LLM，纯脚本化驱动（见 docs/loop/LEARNINGS「M1 集成测试指引」）。
func mockDelegationServer(t *testing.T, workdir string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)

		msgs, _ := req["messages"].([]any)
		tools, _ := req["tools"].([]any)

		// 已经调用过工具（消息里出现 tool 角色）→ 本轮给出纯文本总结，避免死循环。
		hasToolResult := false
		for _, m := range msgs {
			if mm, ok := m.(map[string]any); ok && mm["role"] == "tool" {
				hasToolResult = true
				break
			}
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		f, _ := w.(http.Flusher)

		if hasToolResult {
			writeSSEText(w, f, "已完成委托，文件已写入。")
			return
		}

		// 汇总工具名，用于区分当前是哪个代理在调用。
		names := map[string]bool{}
		for _, tlv := range tools {
			if tt, ok := tlv.(map[string]any); ok {
				if fn, ok := tt["function"].(map[string]any); ok {
					if n, ok := fn["name"].(string); ok {
						names[n] = true
					}
				}
			}
		}

		switch {
		case names["file_write"]:
			// Coder 首轮：真正写文件。
			writeSSEToolCall(w, f, "file_write", `{"path":"hello.txt","content":"hello from coder"}`)
		case names["coder"]:
			// Orchestrator 首轮：委托 Coder 创建文件。
			writeSSEToolCall(w, f, "coder",
				`{"request":"请在受限工作目录下创建 hello.txt，内容写入 hello"}`)
		default:
			writeSSEText(w, f, "无可用工具，结束。")
		}
	}))
}

// writeSSEText 写出一段纯文本流式响应（finish_reason=stop）。
func writeSSEText(w http.ResponseWriter, f http.Flusher, text string) {
	for _, ch := range []string{
		fmt.Sprintf(`data: {"id":"t","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":%s},"finish_reason":null}]}`, mustJSON(text)),
		`data: {"id":"t","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"content":""},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	} {
		fmt.Fprintf(w, "%s\n\n", ch)
		if f != nil {
			f.Flush()
		}
	}
}

// writeSSEToolCall 写出一个 tool_call 流式响应（finish_reason=tool_calls）。
func writeSSEToolCall(w http.ResponseWriter, f http.Flusher, name, args string) {
	for _, ch := range []string{
		fmt.Sprintf(`data: {"id":"t","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":%s,"arguments":%s}}]},"finish_reason":null}]}`, mustJSON(name), mustJSON(args)),
		`data: {"id":"t","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"content":""},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	} {
		fmt.Fprintf(w, "%s\n\n", ch)
		if f != nil {
			f.Flush()
		}
	}
}

// mustJSON 把任意值序列化为合法 JSON（作为字面量嵌入更外层 JSON 时自动转义）。
func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `""`
	}
	return string(b)
}

// TestEngine_SubAgentDelegation_WritesFile 验证 M1-08 核心验收：
// Orchestrator 委托 Coder 子代理，Coder 通过 CodeAct 工具集成功在工作目录写入文件。
// 不调用真实 LLM，用 mockDelegationServer 脚本化驱动「Orchestrator→coder→file_write」调用链。
func TestEngine_SubAgentDelegation_WritesFile(t *testing.T) {
	workdir := t.TempDir()
	srv := mockDelegationServer(t, workdir)
	defer srv.Close()

	eng, err := New(ModelConfig{
		ModelID:  "mock-model",
		BaseURL:  srv.URL,
		APIKey:   "test-key",
		Protocol: "openai",
		Team:     TeamConfig{EnableSubAgents: true},
		Workdir:  workdir,
	})
	if err != nil {
		t.Fatalf("New engine with sub-agents failed: %v", err)
	}
	defer eng.Close()

	reply, err := eng.Chat(context.Background(), "sess-deleg", "请创建 hello.txt", nil)
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(workdir, "hello.txt"))
	if err != nil {
		t.Fatalf("Coder 未写入文件（%v）；Orchestrator 回复=%q", err, reply)
	}
	if string(got) != "hello from coder" {
		t.Fatalf("文件内容不符：got=%q", string(got))
	}
	t.Logf("✅ Orchestrator 委托 Coder 写文件成功：content=%q, reply=%q", string(got), reply)
}
