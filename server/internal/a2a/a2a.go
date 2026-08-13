// Package a2a 定义 GoMultiAgentV2 作为 A2A（Agent-to-Agent，Google 规范 0.2.x）
// server 对外暴露能力的协议类型。本包无框架依赖，可被 api 层与测试直接复用。
package a2a

import "time"

// ProtocolVersion 是本项目实现的 A2A 协议版本（对齐 Google A2A 规范 0.2.x）。
const ProtocolVersion = "0.2.0"

// A2A 规范定义的方法名。
const (
	MethodMessageSend   = "message/send"
	MethodTasksSend     = "tasks/send"
	MethodMessageStream = "message/stream" // 预留：当前服务端仅实现非流式 message/send
)

// Part 是消息/产物的一个内容块（A2A 规范支持 text/file/data）。
// 最小实现仅支持文本（Kind 缺省按 text 处理）。
type Part struct {
	Kind string `json:"kind,omitempty"` // "text" | "file" | "data"
	Text string `json:"text,omitempty"`
}

// Message 是 A2A 的一条消息（role: user/agent）。
type Message struct {
	Role  string `json:"role"` // "user" | "agent"
	Parts []Part `json:"parts"`
}

// Text 从消息中提取首个文本块内容，便于业务层取用户输入。
func (m Message) Text() string {
	for _, p := range m.Parts {
		if p.Text != "" {
			return p.Text
		}
	}
	return ""
}

// AgentCard 描述本平台作为 A2A server 暴露的能力（.well-known/agent.json）。
type AgentCard struct {
	ProtocolVersion    string         `json:"protocolVersion"`
	Name               string         `json:"name"`
	Description        string         `json:"description"`
	URL                string         `json:"url"` // A2A tasks/send 端点
	Version            string         `json:"version"`
	Capabilities       Capabilities   `json:"capabilities"`
	Skills             []Skill        `json:"skills"`
	DefaultInputModes  []string       `json:"defaultInputModes"`
	DefaultOutputModes []string       `json:"defaultOutputModes"`
	Authentication     Authentication `json:"authentication"`
}

// Capabilities 描述 server 支持的能力开关。
type Capabilities struct {
	Streaming              bool `json:"streaming"`
	PushNotifications      bool `json:"pushNotifications"`
	StateTransitionHistory bool `json:"stateTransitionHistory"`
}

// Skill 是 server 暴露的一项能力（供外部 client 在 Agent Card 中发现并选择）。
type Skill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags,omitempty"`
	Examples    []string `json:"examples,omitempty"`
	InputModes  []string `json:"inputModes,omitempty"`
	OutputModes []string `json:"outputModes,omitempty"`
}

// Authentication 描述 server 接受的鉴权方案。
type Authentication struct {
	Schemes []string `json:"schemes"` // 如 ["apiKey"]
}

// Task 是 message/send 的结果对象（含状态、历史与产物）。
type Task struct {
	ID        string         `json:"id"`
	SessionID string         `json:"sessionId"`
	Status    TaskStatus     `json:"status"`
	History   []Message      `json:"history,omitempty"`
	Artifacts []Artifact     `json:"artifacts,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// TaskStatus 描述任务生命周期状态。
type TaskStatus struct {
	State     string   `json:"state"` // submitted|working|input-required|completed|failed|canceled
	Timestamp string   `json:"timestamp,omitempty"`
	Message   *Message `json:"message,omitempty"`
}

// Artifact 是任务产出的附加内容块（如生成的文件/报告）。
type Artifact struct {
	ArtifactID string `json:"artifactId,omitempty"`
	Name       string `json:"name,omitempty"`
	Parts      []Part `json:"parts"`
}

// TaskSendParams 是 message/send 的请求参数。
type TaskSendParams struct {
	ID        string         `json:"id,omitempty"`      // 任务 id（作为会话标识，支持多轮）
	SessionID string         `json:"sessionId,omitempty"`
	Message   Message        `json:"message"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// JSONRPCRequest 是 A2A 任务入口使用的 JSON-RPC 2.0 请求信封。
type JSONRPCRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id,omitempty"`
	Method  string         `json:"method"`
	Params  TaskSendParams `json:"params,omitempty"`
}

// JSONRPCResponse 是 JSON-RPC 2.0 响应信封。
type JSONRPCResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *RPCError `json:"error,omitempty"`
}

// RPCError 是 JSON-RPC 2.0 错误对象。
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// JSONRPCError 构造一个错误响应信封（code 采用 JSON-RPC 预留区间 -32000..-32099）。
func JSONRPCError(id any, code int, msg string) JSONRPCResponse {
	return JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &RPCError{Code: code, Message: msg},
	}
}

// NowRFC3339 返回当前 UTC 时间戳（供 TaskStatus.Timestamp 使用）。
func NowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}
