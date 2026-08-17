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

	"github.com/ayanmw/multiagent2/server/internal/executor"
	codectool "github.com/ayanmw/multiagent2/server/internal/tool"
)

// mockOpenAIServer 模拟 OpenAI 兼容的 /chat/completions 流式端点（SSE）。
// 引擎自 M0-19 起始终以流式模式运行（agent.WithStream(true)），故桩服务必须返回 SSE。
func mockOpenAIServer(t *testing.T, reply string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		// 把 reply 切成两片，模拟 token 级增量；结尾 finish_reason=stop + [DONE]。
		chunks := []string{
			`data: {"id":"test-cmpl","object":"chat.completion.chunk","created":1699200000,"model":"mock-model","choices":[{"index":0,"delta":{"role":"assistant","content":"你好"},"finish_reason":null}]}`,
			`data: {"id":"test-cmpl","object":"chat.completion.chunk","created":1699200000,"model":"mock-model","choices":[{"index":0,"delta":{"content":"` + reply + `"},"finish_reason":null}]}`,
			`data: {"id":"test-cmpl","object":"chat.completion.chunk","created":1699200000,"model":"mock-model","choices":[{"index":0,"delta":{"content":""},"finish_reason":"stop"}]}`,
			`data: [DONE]`,
		}
		for _, ch := range chunks {
			fmt.Fprintf(w, "%s\n\n", ch)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
}

func TestEngine_Chat(t *testing.T) {
	srv := mockOpenAIServer(t, "你好，这是一个 mock 回复")
	defer srv.Close()

	eng, err := New(ModelConfig{
		ModelID:  "mock-model",
		BaseURL:  srv.URL, // 框架在 baseURL 后追加 /chat/completions
		APIKey:   "test-key",
		Protocol: "openai",
	})
	if err != nil {
		t.Fatalf("New engine failed: %v", err)
	}
	defer eng.Close()

	reply, err := eng.Chat(context.Background(), "sess-1", "你好", nil)
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}
	if !strings.Contains(reply, "mock 回复") {
		t.Fatalf("unexpected reply: %q", reply)
	}
}

func TestEngine_NonOpenAIProtocolRejected(t *testing.T) {
	_, err := New(ModelConfig{
		ModelID:  "m",
		BaseURL:  "http://localhost/v1",
		Protocol: "anthropic",
	})
	if err == nil {
		t.Fatal("expected error for non-openai protocol in M0-10")
	}
}

// TestEngine_WithCodeActTools 验证 M1-06：把 CodeAct 工具集注册进引擎后，
// 引擎仍可正常对话（mock LLM 不触发工具调用，仅证明工具装配不破坏对话链路）。
func TestEngine_WithCodeActTools(t *testing.T) {
	srv := mockOpenAIServer(t, "工具已就绪")
	defer srv.Close()

	workdir := t.TempDir()
	tools, err := codectool.NewCodeAct(workdir, nil, nil, executor.ModeUnattended)
	if err != nil {
		t.Fatalf("NewCodeAct failed: %v", err)
	}
	if len(tools) != 4 {
		t.Fatalf("CodeAct 工具数量应为 4，实际 %d", len(tools))
	}

	eng, err := New(ModelConfig{
		ModelID:  "mock-model",
		BaseURL:  srv.URL,
		APIKey:   "test-key",
		Protocol: "openai",
		Tools:    tools,
	})
	if err != nil {
		t.Fatalf("New engine with tools failed: %v", err)
	}
	defer eng.Close()

	reply, err := eng.Chat(context.Background(), "sess-tools", "你好", nil)
	if err != nil {
		t.Fatalf("Chat with tools failed: %v", err)
	}
	if !strings.Contains(reply, "工具已就绪") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	t.Logf("✅ CodeAct 工具注册进引擎后对话正常：reply=%q", reply)
}

// TestEngine_MultiTurnHistory 验证多轮记忆回灌：引擎把历史消息作为初始消息
// seed 进 Runner，使模型在请求体中能看到前文（M0.5-01 修复的核心能力）。
func TestEngine_MultiTurnHistory(t *testing.T) {
	var lastMessages []map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		// 记录投递给 LLM 的 messages，便于断言历史是否回灌。
		var body map[string]any
		if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
			_ = json.Unmarshal(raw, &body)
		}
		if msgs, ok := body["messages"].([]any); ok {
			lastMessages = make([]map[string]any, 0, len(msgs))
			for _, m := range msgs {
				if mm, ok := m.(map[string]any); ok {
					lastMessages = append(lastMessages, mm)
				}
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		chunks := []string{
			`data: {"id":"t","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"收到"},"finish_reason":null}]}`,
			`data: {"id":"t","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"content":""},"finish_reason":"stop"}]}`,
			`data: [DONE]`,
		}
		for _, ch := range chunks {
			fmt.Fprintf(w, "%s\n\n", ch)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	defer srv.Close()

	cfg := ModelConfig{ModelID: "mock-model", BaseURL: srv.URL, APIKey: "k", Protocol: "openai"}

	// 第一轮：无历史。
	eng1, _ := New(cfg)
	if _, err := eng1.Chat(context.Background(), "sess-h", "我叫小明", nil); err != nil {
		t.Fatalf("turn1 Chat failed: %v", err)
	}
	eng1.Close()

	// 第二轮：携带第一轮历史（引擎层 ChatMessage DTO，M6-02）。
	history := []ChatMessage{
		{Role: "user", Content: "我叫小明"},
		{Role: "assistant", Content: "收到"},
	}
	eng2, _ := New(cfg)
	if _, err := eng2.Chat(context.Background(), "sess-h", "我刚说了什么名字？", history); err != nil {
		t.Fatalf("turn2 Chat failed: %v", err)
	}
	eng2.Close()

	if len(lastMessages) == 0 {
		t.Fatal("mock 未收到任何 messages")
	}
	// 首条 user 消息（跳过框架自动注入的 system 指令）应来自回灌的历史。
	var firstUser, lastUser string
	for _, m := range lastMessages {
		if m["role"] == "user" {
			if firstUser == "" {
				firstUser, _ = m["content"].(string)
			}
			lastUser, _ = m["content"].(string)
		}
	}
	if firstUser != "我叫小明" {
		t.Fatalf("历史未回灌：首条 user 消息应为『我叫小明』，实际 %q (messages=%v)", firstUser, lastMessages)
	}
	if lastUser != "我刚说了什么名字？" {
		t.Fatalf("当前问题未出现在末尾 user：实际 %q", lastUser)
	}
	t.Logf("✅ 多轮记忆回灌校验通过：LLM 请求 messages=%v", lastMessages)
}
