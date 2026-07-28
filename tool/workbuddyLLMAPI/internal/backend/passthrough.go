package backend

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"workbuddyllmapi/internal/config"
	"workbuddyllmapi/internal/openai"
)

// Passthrough 将请求原样转发到任意 OpenAI 兼容的上游 base URL，
// 并把响应（含 SSE 流）直接回写给客户端。这是对接真实模型（或本机 vLLM 等）的通用代理模式。
type Passthrough struct {
	cfg *config.Config
}

func NewPassthrough(cfg *config.Config) *Passthrough { return &Passthrough{cfg: cfg} }

func (p *Passthrough) Name() string { return "passthrough" }

func (p *Passthrough) Chat(w http.ResponseWriter, r *http.Request, req *openai.ChatCompletionRequest) {
	body, err := json.Marshal(req)
	if err != nil {
		writeError(w, err)
		return
	}

	upstream := strings.TrimRight(p.cfg.BaseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, upstream, bytes.NewReader(body))
	if err != nil {
		writeError(w, err)
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if p.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	}

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		writeError(w, err)
		return
	}
	defer resp.Body.Close()

	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}
