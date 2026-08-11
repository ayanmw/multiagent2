// Package api 是与 GM-Agent 后端复用同一 REST+SSE 协议的轻量 HTTP 客户端。
// 所有请求自动附加 JWT；SSE 端点（/api/chat/:key/stream）以 fetch 等价方式手动解析 AG-UI 帧。
package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Client 封装后端基地址与鉴权令牌。
type Client struct {
	BaseURL string
	Token   string
}

// NewClient 构造客户端。
func NewClient(baseURL, token string) *Client {
	return &Client{BaseURL: baseURL, Token: token}
}

// do 发送 JSON 请求并（可选）解析响应体到 out。非 2xx 时返回人类可读错误。
func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+"/api"+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := fmt.Sprintf("请求失败 (%d)", resp.StatusCode)
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			msg = e.Error
		}
		return fmt.Errorf("%s", msg)
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// Login 调 POST /api/auth/login（account 可为用户名或邮箱）。
func (c *Client) Login(ctx context.Context, account, password string) (*AuthResponse, error) {
	var r AuthResponse
	err := c.do(ctx, http.MethodPost, "/auth/login", map[string]string{
		"account":  account,
		"password": password,
	}, &r)
	return &r, err
}

// Register 调 POST /api/auth/register。
func (c *Client) Register(ctx context.Context, username, email, password, displayName string) (*AuthResponse, error) {
	var r AuthResponse
	err := c.do(ctx, http.MethodPost, "/auth/register", map[string]any{
		"username":     username,
		"email":        email,
		"password":     password,
		"display_name": displayName,
	}, &r)
	return &r, err
}

// Me 调 GET /api/me。
func (c *Client) Me(ctx context.Context) (*User, error) {
	var r struct {
		User User `json:"user"`
	}
	if err := c.do(ctx, http.MethodGet, "/me", nil, &r); err != nil {
		return nil, err
	}
	return &r.User, nil
}

// CreateSession 调 POST /api/sessions（title 可空，后端默认「新对话」）。
func (c *Client) CreateSession(ctx context.Context, title string) (*Session, error) {
	var s Session
	err := c.do(ctx, http.MethodPost, "/sessions", map[string]string{"title": title}, &s)
	return &s, err
}

// ListSessions 调 GET /api/sessions。
func (c *Client) ListSessions(ctx context.Context) ([]Session, error) {
	var r struct {
		Sessions []Session `json:"sessions"`
		Total    int       `json:"total"`
	}
	if err := c.do(ctx, http.MethodGet, "/sessions", nil, &r); err != nil {
		return nil, err
	}
	return r.Sessions, nil
}

// GetSession 调 GET /api/sessions/:key（含历史消息）。
func (c *Client) GetSession(ctx context.Context, key string) (*SessionDetail, error) {
	var s SessionDetail
	err := c.do(ctx, http.MethodGet, "/sessions/"+url.PathEscape(key), nil, &s)
	return &s, err
}

// ListTaskRuns 调 GET /api/taskruns（owner 隔离的后台任务列表）。
func (c *Client) ListTaskRuns(ctx context.Context) ([]TaskRun, error) {
	var r struct {
		Runs []TaskRun `json:"runs"`
	}
	if err := c.do(ctx, http.MethodGet, "/taskruns", nil, &r); err != nil {
		return nil, err
	}
	return r.Runs, nil
}

// StreamChat 调 POST /api/chat/:key/stream，逐帧解析 AG-UI SSE 事件并回调 onEvent。
// 这是与前端 web/src/api/chat.ts streamChat 等价的命令行实现：fetch + ReadableStream
// 手动按 "\n\n" 切帧，仅处理 data: 前缀行。
func (c *Client) StreamChat(ctx context.Context, sessionKey, message string, modelID uint, workspaceKey string, onEvent func(AGUIEvent)) error {
	body := map[string]any{"message": message}
	if modelID != 0 {
		body["model_id"] = modelID
	}
	if workspaceKey != "" {
		body["workspace_key"] = workspaceKey
	}
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	endpoint := c.BaseURL + "/api/chat/" + url.PathEscape(sessionKey) + "/stream"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		msg := fmt.Sprintf("流式请求失败 (%d)", resp.StatusCode)
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			msg = e.Error
		}
		return fmt.Errorf("%s", msg)
	}

	reader := bufio.NewReader(resp.Body)
	var buf strings.Builder
	flush := func() {
		for {
			s := buf.String()
			i := strings.Index(s, "\n\n")
			if i < 0 {
				break
			}
			frame := s[:i]
			buf.Reset()
			buf.WriteString(s[i+2:])
			parseFrame(frame, onEvent)
		}
	}
	for {
		line, rerr := reader.ReadString('\n')
		buf.WriteString(line)
		flush()
		if rerr != nil {
			if rerr == io.EOF {
				flush()
				break
			}
			return rerr
		}
	}
	return nil
}

// parseFrame 解析单个 SSE 帧：仅处理 data: 前缀行，解析为 AGUIEvent 并回调。
func parseFrame(frame string, onEvent func(AGUIEvent)) {
	for _, line := range strings.Split(frame, "\n") {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "data:") {
			continue
		}
		payload := strings.TrimSpace(t[len("data:"):])
		if payload == "" {
			continue
		}
		var ev AGUIEvent
		if err := json.Unmarshal([]byte(payload), &ev); err == nil {
			onEvent(ev)
		}
	}
}
