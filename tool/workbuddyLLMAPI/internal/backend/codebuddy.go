package backend

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"workbuddyllmapi/internal/config"
	"workbuddyllmapi/internal/openai"
)

// CodeBuddy 后端通过**本地 CodeBuddy/WorkBuddy 守护进程**的 ACP 协议（HTTP JSON-RPC over SSE）
// 驱动 LLM 推理，从而消耗本机已登录的 WorkBuddy/CodeBuddy 账号积分。
//
// 与早期「Python sidecar + codebuddy-agent-sdk + CODEBUDDY_API_KEY」方案相比：
//   - 不需要明文 API Key（直接使用守护进程已登录态）；
//   - 不依赖外部 codebuddy-agent-sdk 安装；
//   - 经由 ACP `session/new` 建立**独立会话**，规避 /api/v1/runs 共享会话被「卡死」导致新请求
//     永远排队的问题；
//   - 文本增量通过持久 SSE 流以 `session/update(agent_message_chunk)` 事件返回，干净可靠。
//
// 流程（每个请求）：connect -> initialize -> session/new -> [可选 set model] -> session/prompt
//   -> 读 SSE 收集 agent_message_chunk -> session_end 结束 -> session/close -> disconnect。
//
// 守护进程同一时刻只执行一个 agent 任务，因此这里用 mutex 串行化提示词，避免堆积导致卡死。
type CodeBuddy struct {
	cfg  *config.Config
	cli  *http.Client
	mu   sync.Mutex // 串行化对守护进程的 prompts（单 agent 槽位）
	acpH http.Header
}

func NewCodeBuddy(cfg *config.Config) *CodeBuddy {
	return &CodeBuddy{
		cfg: cfg,
		cli: &http.Client{Timeout: 0}, // 单个请求的超时由 context 控制
	}
}

func (c *CodeBuddy) Name() string { return "codebuddy" }

// ---------- ACP 事件结构 ----------

type acpEvent struct {
	Method string `json:"method"`
	Params struct {
		SessionID string `json:"sessionId"`
		Update    struct {
			SessionUpdate string `json:"sessionUpdate"`
			Content       struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			StopReason string `json:"stopReason"`
		} `json:"update"`
	} `json:"params"`
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

type acpConnectResp struct {
	ConnectionID string `json:"connectionId"`
	SessionToken string `json:"sessionToken"`
}

// ---------- 低层 HTTP 助手 ----------

// post 发送 ACP JSON-RPC POST。hdr 为 nil 时使用 c.acpH；返回原始响应体
// （守护进程常返回 ":ok" 或内嵌 SSE）。调用方应传入已克隆的 hdr 以避免并发覆盖。
func (c *CodeBuddy) post(ctx context.Context, path string, body any, hdr http.Header, out *[]byte) ([]byte, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.CodeBuddyDaemonURL+path, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CodeBuddy-Request", "1")
	if hdr != nil {
		for k, vv := range hdr {
			if len(vv) > 0 {
				req.Header.Set(k, vv[0])
			}
		}
	}
	resp, err := c.cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return data, fmt.Errorf("acp POST %s -> %d: %s", path, resp.StatusCode, string(data))
	}
	if out != nil {
		*out = data
	}
	return data, nil
}

// openSSE 打开持久 SSE 通知流（响应以 session/update 事件流式返回）。
func (c *CodeBuddy) openSSE(ctx context.Context, connID string) (*http.Response, error) {
	u := c.cfg.CodeBuddyDaemonURL + "/api/v1/acp?acp-connection-id=" + connID
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-CodeBuddy-Request", "1")
	req.Header.Set("Accept", "text/event-stream")
	return c.cli.Do(req)
}

// extractSessionID 从 session/new 的响应体（内嵌 SSE）中解析 sessionId。
func extractSessionID(raw []byte) string {
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		p := strings.TrimSpace(line[len("data:"):])
		var o struct {
			ID     int `json:"id"`
			Result struct {
				SessionID string `json:"sessionId"`
			} `json:"result"`
		}
		if json.Unmarshal([]byte(p), &o) == nil && o.ID == 2 {
			return o.Result.SessionID
		}
	}
	return ""
}

// ---------- 核心：一次性完成补全（流式增量通过 onDelta 回调给出） ----------

func (c *CodeBuddy) completeOnce(ctx context.Context, model, prompt string, onDelta func(string)) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 0) connect —— 响应为扁平的 {connectionId, sessionToken}（偶尔包在 data 里）
	connRaw, err := c.post(ctx, "/api/v1/acp/connect", map[string]any{}, nil, nil)
	if err != nil {
		return "", fmt.Errorf("acp connect: %w", err)
	}
	var conn acpConnectResp
	if err := json.Unmarshal(connRaw, &conn); err != nil || conn.ConnectionID == "" {
		var wrapped struct {
			Data acpConnectResp `json:"data"`
		}
		if json.Unmarshal(connRaw, &wrapped) == nil {
			conn = wrapped.Data
		}
	}
	if conn.ConnectionID == "" {
		return "", fmt.Errorf("acp connect: 无法获取 connectionId（守护进程是否运行？%s）", c.cfg.CodeBuddyDaemonURL)
	}
	defer func() {
		// disconnect 释放连接（best-effort，使用本连接的头，不触碰共享 c.acpH）
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodDelete,
			c.cfg.CodeBuddyDaemonURL+"/api/v1/acp", nil)
		req.Header.Set("X-CodeBuddy-Request", "1")
		req.Header.Set("acp-connection-id", conn.ConnectionID)
		req.Header.Set("acp-session-token", conn.SessionToken)
		if resp, e := c.cli.Do(req); e == nil {
			resp.Body.Close()
		}
	}()

	c.acpH = http.Header{
		"acp-connection-id": {conn.ConnectionID},
		"acp-session-token": {conn.SessionToken},
		"Accept":            {"application/json, text/event-stream"},
	}

	// 1) 打开 SSE 流（必须在发 prompt 之前）
	sse, err := c.openSSE(ctx, conn.ConnectionID)
	if err != nil {
		return "", fmt.Errorf("acp open SSE: %w", err)
	}
	defer sse.Body.Close()

	// 2) initialize
	_, _ = c.post(ctx, "/api/v1/acp", map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": 1, "capabilities": map[string]any{},
			"clientInfo": map[string]any{"name": "workbuddy-llm-api", "version": "0.1"},
		},
	}, c.acpH, nil)

	// 3) session/new
	sessRaw, err := c.post(ctx, "/api/v1/acp", map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "session/new",
		"params": map[string]any{"cwd": c.cfg.CodeBuddyCWD, "mcpServers": []any{}},
	}, c.acpH, nil)
	if err != nil {
		return "", fmt.Errorf("acp session/new: %w", err)
	}
	sid := extractSessionID(sessRaw)
	if sid == "" {
		return "", fmt.Errorf("acp session/new: 无法获取 sessionId")
	}

	// 3.5) 设置模型（model 在此处已是具体 id：默认 hy3 或回退 deepseek-v4-pro）
	if model != "" {
		_, _ = c.post(ctx, "/api/v1/acp", map[string]any{
			"jsonrpc": "2.0", "id": 5, "method": "session/set_config_option",
			"params": map[string]any{"sessionId": sid, "configId": "model", "value": model},
		}, c.acpH, nil)
	}

	// 4) session/prompt（fire-and-forget，文本经 SSE 返回）
	promptHdr := c.acpH.Clone()
	promptErr := make(chan error, 1)
	go func() {
		_, e := c.post(context.Background(), "/api/v1/acp", map[string]any{
			"jsonrpc": "2.0", "id": 3, "method": "session/prompt",
			"params": map[string]any{
				"sessionId": sid,
				"prompt": []any{map[string]any{"type": "text", "text": prompt}},
			},
		}, promptHdr, nil)
		promptErr <- e
	}()

	// 5) 读 SSE 收集增量，直到 session_end
	var sb strings.Builder
	stopped := false
	sc := bufio.NewScanner(sse.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(line[len("data:"):])
		var ev acpEvent
		if json.Unmarshal([]byte(payload), &ev) != nil {
			continue
		}
		if ev.Method == "session/update" {
			u := ev.Params.Update
			switch u.SessionUpdate {
			case "agent_message_chunk":
				if u.Content.Type == "text" && u.Content.Text != "" {
					sb.WriteString(u.Content.Text)
					if onDelta != nil {
						onDelta(u.Content.Text)
					}
				}
			case "session_end":
				stopped = true
			}
		}
		if stopped {
			break
		}
	}
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		// 流异常但非主动取消
		if sb.Len() == 0 {
			return "", fmt.Errorf("acp SSE read error: %w", err)
		}
	}

	// 5.5) 若没有任何文本产出，尝试回收 prompt 异步错误（模型不可用常表现于此）
	if sb.Len() == 0 {
		select {
		case e := <-promptErr:
			if e != nil {
				return "", fmt.Errorf("acp session/prompt 失败: %w", e)
			}
		default:
		}
	}

	// 6) session/close（best-effort）
	_, _ = c.post(context.Background(), "/api/v1/acp", map[string]any{
		"jsonrpc": "2.0", "id": 4, "method": "session/close",
		"params": map[string]any{"sessionId": sid},
	}, c.acpH, nil)

	return sb.String(), nil
}

// modelPlan 决定本次请求使用的模型顺序。
//   - 调用方显式指定了模型（且非占位名 codebuddy-default）→ 只用该模型，不做回退；
//   - 未指定 → 主模型用配置默认（WB_DAEMON_MODEL，默认 hy3），回退模型用
//     WB_DAEMON_FALLBACK_MODEL（默认 deepseek-v4-pro）。
func (c *CodeBuddy) modelPlan(reqModel string) (primary, fallback string) {
	if reqModel != "" && reqModel != "codebuddy-default" {
		return reqModel, ""
	}
	primary = c.cfg.CodeBuddyModel
	if primary == "" {
		primary = "auto"
	}
	return primary, c.cfg.CodeBuddyFallbackModel
}

// modelList 把 modelPlan 展开成有序候选列表。
func (c *CodeBuddy) modelList(reqModel string) []string {
	p, f := c.modelPlan(reqModel)
	if f != "" && f != p {
		return []string{p, f}
	}
	return []string{p}
}

// generate 按顺序尝试模型列表：主模型失败（网络/HTTP 错误/返回空）时自动回退到下一个。
// 回退仅在所有候选都失败时返回错误；若某模型已开始产出增量则不再切换（避免串流）。
// 返回最终文本、实际使用的模型名、以及错误（若有）。
func (c *CodeBuddy) generate(ctx context.Context, models []string, prompt string, onDelta func(string)) (string, string, error) {
	var lastErr error
	for i, m := range models {
		var emitted int
		text, err := c.completeOnce(ctx, m, prompt, func(d string) {
			emitted++
			if onDelta != nil {
				onDelta(d)
			}
		})
		if err == nil && text != "" {
			return text, m, nil
		}
		lastErr = err
		if emitted > 0 {
			// 已经开始输出，不再切换，直接返回当前结果（可能不完整）
			if err == nil {
				err = fmt.Errorf("model %s 中途失败", m)
			}
			return text, m, err
		}
		if i < len(models)-1 {
			// 还有回退模型可用，且本模型未产出任何增量
			continue
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("所有候选模型均返回空结果")
	}
	return "", "", lastErr
}

// ---------- Backend 接口：Chat ----------

func (c *CodeBuddy) Chat(w http.ResponseWriter, r *http.Request, req *openai.ChatCompletionRequest) {
	system, conv := splitMessages(req.Messages)
	prompt := buildPrompt(system, conv)

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()

	models := c.modelList(req.Model)
	modelOut := req.Model
	if len(models) > 0 {
		modelOut = models[0]
	}

	if req.Stream {
		flusher, _ := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		writeSSE(w, openai.ChatCompletionStreamResponse{
			ID: "wb-" + modelOut, Object: "chat.completion.chunk", Created: timeNow(), Model: modelOut,
			Choices: []openai.StreamChoice{{Index: 0, Delta: openai.ChatMessage{Role: "assistant"}}},
		})
		if flusher != nil {
			flusher.Flush()
		}

		_, usedModel, err := c.generate(ctx, models, prompt, func(delta string) {
			writeSSE(w, openai.ChatCompletionStreamResponse{
				ID: "wb-" + modelOut, Object: "chat.completion.chunk", Created: timeNow(), Model: modelOut,
				Choices: []openai.StreamChoice{{Index: 0, Delta: openai.ChatMessage{Content: delta}}},
			})
			if flusher != nil {
				flusher.Flush()
			}
		})
		if usedModel != "" {
			modelOut = usedModel
		}
		if err != nil {
			writeSSE(w, openai.ChatCompletionStreamResponse{
				ID: "wb-" + modelOut, Object: "chat.completion.chunk", Created: timeNow(), Model: modelOut,
				Choices: []openai.StreamChoice{{Index: 0, Delta: openai.ChatMessage{Content: "[codebuddy daemon error] " + err.Error()}}},
			})
			if flusher != nil {
				flusher.Flush()
			}
		}
		writeSSE(w, openai.ChatCompletionStreamResponse{
			ID: "wb-" + modelOut, Object: "chat.completion.chunk", Created: timeNow(), Model: modelOut,
			Choices: []openai.StreamChoice{{Index: 0, FinishReason: ptr("stop")}},
		})
		fmt.Fprint(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		return
	}

	text, usedModel, err := c.generate(ctx, models, prompt, nil)
	if usedModel != "" {
		modelOut = usedModel
	}
	if err != nil {
		writeError(w, err)
		return
	}
	writeChatCompletion(w, modelOut, text)
}

// buildPrompt 将 system + 多轮对话拼成单段提示词。
// 守护进程跑的是「Agent」而非裸 LLM，这里加一句指令让它直接回答、尽量少用工具，
// 更接近 OpenAI 文本补全的体验。
func buildPrompt(system string, conv []map[string]string) string {
	var b strings.Builder
	if system != "" {
		b.WriteString(system)
		b.WriteString("\n\n")
	}
	b.WriteString("请直接给出回答，不要调用任何工具或执行命令，也不要复述要求。\n\n")
	for _, m := range conv {
		role := m["role"]
		if role == "" {
			role = "user"
		}
		b.WriteString(role + ": " + m["content"] + "\n")
	}
	return b.String()
}
