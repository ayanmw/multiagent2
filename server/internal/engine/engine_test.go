package engine

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

	reply, err := eng.Chat(context.Background(), "sess-1", "你好")
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
