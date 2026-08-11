package api

import "time"

// 以下类型与 server 端 REST+SSE 响应契约一一对应（见 server/internal/api/* 与 web/src/api/*）。

// User 是用户视图（对齐 server internal/api/auth.go 的 userView）。
type User struct {
	ID          uint   `json:"id"`
	Username    string `json:"username"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	RoleID      uint   `json:"role_id"`
	Role        string `json:"role"`
	Status      string `json:"status"`
}

// AuthResponse 是登录/注册返回（{token, user}）。
type AuthResponse struct {
	Token string `json:"token"`
	User  User   `json:"user"`
}

// Session 是会话列表视图（对齐 sessionView）。
type Session struct {
	ID         uint   `json:"id"`
	UserID     uint   `json:"user_id"`
	SessionKey string `json:"session_key"`
	Title      string `json:"title"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

// Message 是会话中的单条消息（对齐 messageView）。
type Message struct {
	ID        uint   `json:"id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
}

// SessionDetail 是带历史消息的会话详情（对齐 sessionDetailView）。
type SessionDetail struct {
	Session
	Messages []Message `json:"messages"`
}

// TaskRun 是后台任务视图（对齐框架 trpc-agent-go/agent/taskrun.Run 的 JSON 字段）。
type TaskRun struct {
	ID              string    `json:"id"`
	OwnerUserID     string    `json:"owner_user_id"`
	ParentSessionID string    `json:"parent_session_id"`
	AppName         string    `json:"app_name"`
	AgentName       string    `json:"agent_name"`
	Task            string    `json:"task"`
	Status          string    `json:"status"`
	Summary         string    `json:"summary"`
	Result          string    `json:"result"`
	Error           string    `json:"error"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// AGUIEvent 是 AG-UI SSE 事件的子集（对齐 web/src/api/chat.ts 的 AGUIEvent）。
type AGUIEvent struct {
	Type         string `json:"type"`
	MessageID    string `json:"messageId"`
	Delta        string `json:"delta"`
	ThreadID     string `json:"threadId"`
	RunID        string `json:"runId"`
	ToolCallID   string `json:"toolCallId"`
	ToolCallName string `json:"toolCallName"`
	Message      string `json:"message"`
}
