package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockOpenAIServer 模拟 OpenAI 兼容的 /chat/completions 端点（非流式）。
func mockOpenAIServer(t *testing.T, reply string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "test-cmpl",
			"object": "chat.completion",
			"created": 1699200000,
			"model": "mock-model",
			"choices": [
				{
					"index": 0,
					"message": {"role": "assistant", "content": "` + reply + `"},
					"finish_reason": "stop"
				}
			],
			"usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}
		}`))
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
