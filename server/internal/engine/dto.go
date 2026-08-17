package engine

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// ChatMessage 是 api 层与引擎之间传递的对话消息 DTO（M6-02）。
// 把框架 model.Message 的耦合收敛到 engine 层：api 只持有字符串角色/内容，
// 由 ToFramework 在边界处完成「字符串 → 框架 Role 枚举」映射。
// 这样 api 包无需 import 框架 model 包即可参与多轮记忆回灌。
type ChatMessage struct {
	Role    string
	Content string
}

// ToFramework 转换为框架 model.Message（角色映射 user/assistant/system/tool，未知按 user）。
func (m ChatMessage) ToFramework() model.Message {
	var role model.Role
	switch m.Role {
	case "assistant":
		role = model.RoleAssistant
	case "system":
		role = model.RoleSystem
	case "tool":
		role = model.RoleTool
	default:
		role = model.RoleUser
	}
	return model.Message{Role: role, Content: m.Content}
}

// ToFrameworkMessages 批量转换。
func ToFrameworkMessages(msgs []ChatMessage) []model.Message {
	out := make([]model.Message, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.ToFramework())
	}
	return out
}

// StreamEvent 是引擎经 AG-UI 协议向下游（api/SSE）暴露的归一化事件 DTO（M6-02）。
// 把框架 *event.Event 的耦合收敛到 engine 层：api 仅消费本 DTO 完成 AG-UI 转换，
// 不再直接 import 框架 event 包。仅保留 AG-UI 转换所需的字段（文本增量/整块、工具调用、错误/熔断标记）。
type StreamEvent struct {
	IsError      bool
	ErrorMsg     string
	CircuitBreak bool
	Choices      []StreamChoice
}

// StreamChoice 对应框架 event 中单条 Choice 的文本与工具调用。
type StreamChoice struct {
	DeltaContent   string
	MessageContent string
	ToolCalls      []StreamToolCall
}

// StreamToolCall 对应单条工具调用（id/名称/参数 JSON 字符串）。
type StreamToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// toStreamEvent 把框架事件转换为 StreamEvent DTO（含护栏熔断标记）。
func toStreamEvent(ev *event.Event) StreamEvent {
	se := StreamEvent{}
	if ev == nil || ev.Response == nil {
		return se
	}
	if ev.IsError() {
		msg := "agent error"
		if ev.Response.Error != nil {
			msg = ev.Response.Error.Message
		}
		se.IsError = true
		se.ErrorMsg = msg
		se.CircuitBreak = IsCircuitBreakEvent(ev)
		return se
	}
	for i := range ev.Response.Choices {
		ch := ev.Response.Choices[i]
		sc := StreamChoice{
			DeltaContent:   ch.Delta.Content,
			MessageContent: ch.Message.Content,
		}
		tcs := ch.Delta.ToolCalls
		if len(tcs) == 0 {
			tcs = ch.Message.ToolCalls
		}
		for j := range tcs {
			tc := tcs[j]
			sc.ToolCalls = append(sc.ToolCalls, StreamToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: string(tc.Function.Arguments),
			})
		}
		se.Choices = append(se.Choices, sc)
	}
	return se
}

// SessionService 是框架 session.Service 的引擎层抽象（M6-02：api 不直接依赖框架 session 包）。
//   - GetSessionEvents 透传子会话事件供 transcript 回查（以 []any 返回，由框架类型自身 JSON
//     序列化，保持既有前端契约不变）；
//   - Framework 仅引擎内部使用，回传底层框架 session.Service 给 taskrun 工具。
//
// api 层仅依赖 GetSessionEvents（返回的 []any 元素由框架类型自行序列化），不接触框架 session/event 类型。
type SessionService interface {
	GetSessionEvents(ctx context.Context, appName, userID, sessionID string, eventNum int) ([]any, error)
	Framework() session.Service
}

type sessionServiceAdapter struct {
	svc session.Service
}

// NewSessionService 用框架 session.Service 构造引擎层抽象，供 api 层持有。
func NewSessionService(svc session.Service) SessionService {
	return &sessionServiceAdapter{svc: svc}
}

// Framework 回传底层框架 session.Service（仅引擎内部使用）。
func (a *sessionServiceAdapter) Framework() session.Service { return a.svc }

// GetSessionEvents 读取指定子会话的事件用于 transcript 回查。
// 事件以 []any 返回（元素为框架 *event.Event），保持与直接返回框架事件一致的 JSON 线格式，
// 既不改变前端契约，又使 api 层无需 import 框架 session/event 包。
func (a *sessionServiceAdapter) GetSessionEvents(ctx context.Context, appName, userID, sessionID string, eventNum int) ([]any, error) {
	if a.svc == nil {
		return []any{}, nil
	}
	sess, err := a.svc.GetSession(ctx, session.Key{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
	}, session.WithEventNum(eventNum))
	if err != nil {
		return nil, err
	}
	if sess == nil {
		return []any{}, nil
	}
	events := sess.GetEvents()
	out := make([]any, 0, len(events))
	for _, e := range events {
		out = append(out, e)
	}
	return out, nil
}
