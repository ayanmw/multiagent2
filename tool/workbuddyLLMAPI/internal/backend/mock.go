package backend

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"workbuddyllmapi/internal/config"
	"workbuddyllmapi/internal/openai"
)

// Mock 是一个本地可用的回显后端，便于在没有真实模型/积分时开发联调。
// 支持流式与非流式，按词切片输出，方便前端验证 SSE。
type Mock struct {
	cfg *config.Config
}

func NewMock(cfg *config.Config) *Mock { return &Mock{cfg: cfg} }

func (m *Mock) Name() string { return "mock" }

func (m *Mock) Chat(w http.ResponseWriter, r *http.Request, req *openai.ChatCompletionRequest) {
	lastUser := ""
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			lastUser = req.Messages[i].Content
			break
		}
	}
	reply := fmt.Sprintf("[MOCK] echo of last user message (model=%s): %s", req.Model, lastUser)

	if req.Stream {
		flusher, _ := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		writeSSE(w, openai.ChatCompletionStreamResponse{
			ID: "wb-mock", Object: "chat.completion.chunk", Created: timeNow(), Model: req.Model,
			Choices: []openai.StreamChoice{{Index: 0, Delta: openai.ChatMessage{Role: "assistant"}}},
		})
		if flusher != nil {
			flusher.Flush()
		}

		for _, wd := range strings.Fields(reply) {
			writeSSE(w, openai.ChatCompletionStreamResponse{
				ID: "wb-mock", Object: "chat.completion.chunk", Created: timeNow(), Model: req.Model,
				Choices: []openai.StreamChoice{{Index: 0, Delta: openai.ChatMessage{Content: wd + " "}}},
			})
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(20 * time.Millisecond)
		}

		writeSSE(w, openai.ChatCompletionStreamResponse{
			ID: "wb-mock", Object: "chat.completion.chunk", Created: timeNow(), Model: req.Model,
			Choices: []openai.StreamChoice{{Index: 0, FinishReason: ptr("stop")}},
		})
		fmt.Fprint(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		return
	}

	writeChatCompletion(w, req.Model, reply)
}
