package backend

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"workbuddyllmapi/internal/config"
	"workbuddyllmapi/internal/openai"
)

// CodeBuddy 后端通过 Python sidecar 调用 CodeBuddy Agent SDK（codebuddy-agent-sdk），
// 使用 CODEBUDDY_API_KEY（即 WorkBuddy/CodeBuddy 账号积分）完成推理。
//
// 协议：Go 进程向 sidecar 的 stdin 写入一行 JSON 请求；sidecar 将文本增量以 NDJSON
// 流式写回 stdout：{"type":"delta","text":...} / {"type":"done","usage":...} / {"type":"error","message":...}。
// Go 侧再把 delta 转换成 OpenAI 的 SSE 流（或非流式聚合）。
type CodeBuddy struct {
	cfg *config.Config
}

func NewCodeBuddy(cfg *config.Config) *CodeBuddy { return &CodeBuddy{cfg: cfg} }

func (c *CodeBuddy) Name() string { return "codebuddy" }

func (c *CodeBuddy) Chat(w http.ResponseWriter, r *http.Request, req *openai.ChatCompletionRequest) {
	system, conv := splitMessages(req.Messages)
	payload := map[string]any{
		"system":   system,
		"messages": conv,
		"model":    req.Model,
		"stream":   req.Stream,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		writeError(w, err)
		return
	}

	cmd := exec.CommandContext(r.Context(), c.cfg.SidecarPython, c.cfg.CodeBuddySidecar)
	cmd.Env = append(os.Environ(),
		"CODEBUDDY_API_KEY="+c.cfg.CodeBuddyAPIKey,
		"CODEBUDDY_INTERNET_ENVIRONMENT="+c.cfg.CodeBuddyEnv,
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		writeError(w, err)
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		writeError(w, err)
		return
	}
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		writeError(w, fmt.Errorf("codebuddy sidecar 不可用（请确认 python 与 %s 可用，且已安装 codebuddy-agent-sdk）: %w", c.cfg.CodeBuddySidecar, err))
		return
	}

	_, _ = stdin.Write(append(payloadBytes, '\n'))
	_ = stdin.Close()

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	if req.Stream {
		flusher, _ := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		writeSSE(w, openai.ChatCompletionStreamResponse{
			ID: "wb-" + req.Model, Object: "chat.completion.chunk", Created: timeNow(), Model: req.Model,
			Choices: []openai.StreamChoice{{Index: 0, Delta: openai.ChatMessage{Role: "assistant"}}},
		})
		if flusher != nil {
			flusher.Flush()
		}

		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			var ev map[string]any
			if err := json.Unmarshal([]byte(line), &ev); err != nil {
				continue
			}
			switch ev["type"] {
			case "delta":
				text, _ := ev["text"].(string)
				writeSSE(w, openai.ChatCompletionStreamResponse{
					ID: "wb-" + req.Model, Object: "chat.completion.chunk", Created: timeNow(), Model: req.Model,
					Choices: []openai.StreamChoice{{Index: 0, Delta: openai.ChatMessage{Content: text}}},
				})
			case "done":
				writeSSE(w, openai.ChatCompletionStreamResponse{
					ID: "wb-" + req.Model, Object: "chat.completion.chunk", Created: timeNow(), Model: req.Model,
					Choices: []openai.StreamChoice{{Index: 0, FinishReason: ptr("stop")}},
				})
				fmt.Fprint(w, "data: [DONE]\n\n")
			case "error":
				msg, _ := ev["message"].(string)
				writeSSE(w, openai.ChatCompletionStreamResponse{
					ID: "wb-" + req.Model, Object: "chat.completion.chunk", Created: timeNow(), Model: req.Model,
					Choices: []openai.StreamChoice{{Index: 0, Delta: openai.ChatMessage{Content: "[codebuddy error] " + msg}}},
				})
				fmt.Fprint(w, "data: [DONE]\n\n")
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		_ = cmd.Wait()
		return
	}

	// 非流式：聚合所有 delta
	var content strings.Builder
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		switch ev["type"] {
		case "delta":
			text, _ := ev["text"].(string)
			content.WriteString(text)
		case "error":
			msg, _ := ev["message"].(string)
			writeError(w, fmt.Errorf("codebuddy: %s", msg))
			_ = cmd.Wait()
			return
		}
	}
	_ = cmd.Wait()
	writeChatCompletion(w, req.Model, content.String())
}
