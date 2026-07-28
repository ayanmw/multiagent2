package backend

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"workbuddyllmapi/internal/openai"
)

func timeNow() int64 { return time.Now().Unix() }

func ptr(s string) *string { return &s }

// splitMessages 将 system 与对话消息拆分：system 合并为字符串，其余转为 role/content map 列表。
func splitMessages(msgs []openai.ChatMessage) (string, []map[string]string) {
	var system strings.Builder
	var conv []map[string]string
	for _, m := range msgs {
		if m.Role == "system" {
			system.WriteString(m.Content)
			system.WriteString("\n")
			continue
		}
		conv = append(conv, map[string]string{"role": m.Role, "content": m.Content})
	}
	return system.String(), conv
}

func writeSSE(w http.ResponseWriter, v any) {
	b, _ := json.Marshal(v)
	fmt.Fprintf(w, "data: %s\n\n", b)
}

// writeChatCompletion 写出非流式 OpenAI 响应。usage 为粗略估算（codebuddy 模式下以 sidecar 返回为准）。
func writeChatCompletion(w http.ResponseWriter, model, content string) {
	resp := openai.ChatCompletionResponse{
		ID:      "wb-" + model,
		Object:  "chat.completion",
		Created: timeNow(),
		Model:   model,
		Choices: []openai.ChatCompletionChoice{{
			Index:        0,
			Message:      openai.ChatMessage{Role: "assistant", Content: content},
			FinishReason: "stop",
		}},
		Usage: openai.Usage{
			PromptTokens:     estimateTokens(content),
			CompletionTokens: estimateTokens(content),
			TotalTokens:      estimateTokens(content) * 2,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

// estimateTokens 粗估 token 数（约 4 字符/token），仅用于占位统计。
func estimateTokens(s string) int {
	if s == "" {
		return 0
	}
	return (len([]rune(s)) + 3) / 4
}

func writeError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadGateway)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"message": err.Error(),
			"type":    "wb_backend_error",
		},
	})
}
