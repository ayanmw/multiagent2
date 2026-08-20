package a2a

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Client 是平台作为 A2A client 的轻量实现（M8-01，无框架依赖）：
// 向外部 A2A Agent 发起任务（message/send）或订阅任务进度流（message/stream），
// 并支持先拉取对方 Agent Card（/.well-known/agent.json）发现其能力。
//
// 与自身服务端对齐的约定：
//   - 端点固定为 BaseURL + "/api/a2a"（与 server 端 A2AHandler 注册路径一致）；
//   - 鉴权头默认按 apiKey 方案发 X-API-Key，可经 Headers 覆盖/追加
//     （如设 Headers: {"Authorization": "Bearer <jwt>"} 对接自身平台）。
type Client struct {
	// BaseURL 是外部 Agent 的根地址，如 "http://agent.example.com:8080"。
	BaseURL string
	// APIKey 是外部 Agent 接受的密钥（Agent Card Authentication 声明 apiKey 时填写）。
	APIKey string
	// Headers 是附加到每个请求的额外请求头（如 Authorization），可覆盖默认 X-API-Key。
	Headers map[string]string
	// HTTP 可自定义底层客户端（超时/传输）；nil 时使用 http.DefaultClient。
	HTTP *http.Client
}

// NewClient 构造指向某外部 Agent 的 A2A client。
func NewClient(baseURL, apiKey string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), APIKey: apiKey}
}

// endpoint 返回外部 Agent 的 JSON-RPC 任务入口（与 server 端 /api/a2a 对齐）。
func (c *Client) endpoint() string { return c.BaseURL + "/api/a2a" }

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// newRequest 构造带鉴权/附加头的 JSON-RPC POST 请求。
func (c *Client) newRequest(ctx context.Context, method string, params TaskSendParams) (*http.Request, error) {
	body, err := json.Marshal(JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      "a2a-client",
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("X-API-Key", c.APIKey)
	}
	for k, v := range c.Headers {
		req.Header.Set(k, v)
	}
	return req, nil
}

// FetchAgentCard 拉取外部 Agent 的 Agent Card（GET {BaseURL}/.well-known/agent.json），
// 供调用方在发起任务前发现其能力（协议版本/是否流式/技能/鉴权方案）。
func (c *Client) FetchAgentCard(ctx context.Context) (*AgentCard, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/.well-known/agent.json", nil)
	if err != nil {
		return nil, err
	}
	if c.APIKey != "" {
		req.Header.Set("X-API-Key", c.APIKey)
	}
	for k, v := range c.Headers {
		req.Header.Set(k, v)
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("fetch agent card: http %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var card AgentCard
	if err := json.NewDecoder(resp.Body).Decode(&card); err != nil {
		return nil, fmt.Errorf("decode agent card: %w", err)
	}
	return &card, nil
}

// SendMessage 向外部 Agent 发起一次 message/send（非流式），返回最终 Task。
// params.ID 可作为任务/会话标识支持多轮；返回的 Task 含状态与 History。
func (c *Client) SendMessage(ctx context.Context, params TaskSendParams) (*Task, error) {
	req, err := c.newRequest(ctx, MethodMessageSend, params)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	var rpc JSONRPCResponse
	if err := json.Unmarshal(body, &rpc); err != nil {
		return nil, fmt.Errorf("send message: http %d: 非 JSON-RPC 响应: %w", resp.StatusCode, err)
	}
	if rpc.Error != nil {
		return nil, fmt.Errorf("send message: 外部 Agent 返回错误 code=%d msg=%s", rpc.Error.Code, rpc.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("send message: http %d", resp.StatusCode)
	}
	task, err := taskFromResult(rpc.Result)
	if err != nil {
		return nil, err
	}
	return task, nil
}

// StreamMessage 向外部 Agent 发起 message/stream，订阅任务进度流（SSE）直到结束。
// 约定（与自身服务端一致，见 a2a.go Event* 常量）：
//   - 首帧是 JSON-RPC 响应信封（result=Task，初始状态）；
//   - 其后是 event: task.status_update（中间帧带增量进度文本）与 event: task.artifact_update；
//   - 流结束后返回最终 Task（状态取最后一次 status_update）。
//
// onStatus/onArtifact 可选（nil 忽略），用于调用方实时消费进度。
func (c *Client) StreamMessage(ctx context.Context, params TaskSendParams, onStatus func(TaskStatus), onArtifact func(Artifact)) (*Task, error) {
	req, err := c.newRequest(ctx, MethodMessageStream, params)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		var rpc JSONRPCResponse
		_ = json.Unmarshal(body, &rpc)
		if rpc.Error != nil {
			return nil, fmt.Errorf("stream message: http %d code=%d msg=%s", resp.StatusCode, rpc.Error.Code, rpc.Error.Message)
		}
		return nil, fmt.Errorf("stream message: http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var (
		initialTask  *Task
		latestStatus *TaskStatus
		latestID     string
	)
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	var evt string
	var dataLines []string
	flushFrame := func() {
		if len(dataLines) == 0 {
			return
		}
		payload := strings.Join(dataLines, "\n")
		dataLines = nil
		switch evt {
		case EventTaskStatusUpdate:
			var ev TaskStatusUpdateEvent
			if err := json.Unmarshal([]byte(payload), &ev); err == nil {
				latestID = ev.ID
				s := ev.Status
				latestStatus = &s
				if onStatus != nil {
					onStatus(s)
				}
			}
		case EventTaskArtifactUpdate:
			var ev TaskArtifactUpdateEvent
			if err := json.Unmarshal([]byte(payload), &ev); err == nil {
				latestID = ev.ID
				if onArtifact != nil {
					onArtifact(ev.Artifact)
				}
			}
		default:
			// 无 event 行：首帧 JSON-RPC 响应（result=Task 初始状态）。
			// 注意：首帧不是 task.status_update 事件，不触发 onStatus——
			// 进度回调只对应真正的状态事件（与协议语义一致）。
			var rpc JSONRPCResponse
			if err := json.Unmarshal([]byte(payload), &rpc); err == nil && rpc.Result != nil {
				if t, terr := taskFromResult(rpc.Result); terr == nil {
					initialTask = t
					latestID = t.ID
					s := t.Status
					latestStatus = &s
				}
			}
		}
		evt = ""
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event:"):
			evt = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimPrefix(line, "data:"))
		case line == "":
			flushFrame()
		}
	}
	flushFrame()
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("stream message: 读取 SSE 失败: %w", err)
	}

	final := &Task{}
	if initialTask != nil {
		*final = *initialTask
	}
	if latestStatus != nil {
		final.Status = *latestStatus
	}
	if latestID != "" {
		final.ID = latestID
	}
	return final, nil
}

// taskFromResult 把 JSON-RPC 的 result（any）转换为 Task。
func taskFromResult(result any) (*Task, error) {
	if result == nil {
		return nil, fmt.Errorf("响应缺少 result(Task)")
	}
	b, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("序列化 result 失败: %w", err)
	}
	var t Task
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, fmt.Errorf("解析 result(Task) 失败: %w", err)
	}
	return &t, nil
}
