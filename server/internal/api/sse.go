package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/ayanmw/multiagent2/server/internal/engine"
	"github.com/ayanmw/multiagent2/server/internal/repo"
)

// streamChatRequest 是 SSE 流式对话的请求体（M0.5-06：message 由 GET query 改为 POST body）。
type streamChatRequest struct {
	Message      string `json:"message"`
	ModelID      uint   `json:"model_id"`
	WorkspaceKey string `json:"workspace_key"` // 可选：绑定到某 workspace（M1-07），Executor 在其目录执行
}

// StreamChatHandler handles POST /api/chat/:session_id/stream.
// 将 Agent 事件流转换为 AG-UI 协议的 SSE 事件并逐条推送。本 handler 负责鉴权、参数解析、
// 预算护栏前置与 SSE 帧封装；真正的事件流由统一 Gateway（M4-04）经 gw.Stream 产出，
// 使 SSE 与 Web 非流式、定时、Webhook 共用同一引擎管道与会话串行锁。
func StreamChatHandler(gw *Gateway) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}

		sessionKey := c.Param("session_id")
		if sessionKey == "" {
			sessionKey = repo.NewSessionKey()
		}

		var req streamChatRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体解析失败"})
			return
		}
		message := req.Message
		if strings.TrimSpace(message) == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "message 不能为空"})
			return
		}
		modelID := req.ModelID

		// 预算护栏（M3-04 / M4-06）已集中到 Gateway.prepareRun：发起 LLM 调用前统一评估，
		// 全部 Channel 共用同一拦截。此处仅在 gw.Stream 返回后识别预算耗尽错误，
		// 转换为 SSE RUN_ERROR + RUN_FINISHED（审计已由 Gateway 写入）。

		// 准备 SSE 响应头。
		c.Writer.Header().Set("Content-Type", "text/event-stream")
		c.Writer.Header().Set("Cache-Control", "no-cache")
		c.Writer.Header().Set("Connection", "keep-alive")
		c.Writer.Header().Set("X-Accel-Buffering", "no")

		// emit 向客户端推送一条 AG-UI SSE 事件（含客户端断线检测）。
		emit := func(evtType string, data gin.H) {
			if c.Request.Context().Err() != nil {
				return
			}
			writeSSEEvent(c, evtType, data)
		}

		runID := "run-" + uuid.NewString()[:8]
		emit("RUN_STARTED", gin.H{
			"threadId": sessionKey,
			"runId":    runID,
		})

		res, runErr := gw.Stream(c.Request.Context(), Request{
			Channel:      ChannelWeb,
			UserID:       uid,
			SessionKey:   sessionKey,
			Message:      message,
			ModelID:      modelID,
			WorkspaceKey: req.WorkspaceKey,
		}, emit)
		if runErr != nil {
			var be *BudgetExhaustedError
			if errors.As(runErr, &be) {
				// 预算耗尽：转换为明确的 RUN_ERROR + RUN_FINISHED（审计已由 Gateway 写入）。
				emit("RUN_ERROR", gin.H{"message": "预算耗尽，待恢复（该用户/会话的平台级预算已用尽，请管理员提额后重试）"})
				emit("RUN_FINISHED", gin.H{"threadId": sessionKey, "runId": "run-blocked"})
				return
			}
			// 流式初始化失败（转换期错误已在 emit 内上报），此处补发 RUN_ERROR。
			emit("RUN_ERROR", gin.H{"message": runErr.Error()})
		}
		emit("RUN_FINISHED", gin.H{"threadId": sessionKey, "runId": runID})
		_ = res
	}
}

// writeSSEEvent 向客户端推送一条 AG-UI SSE 事件（data: <json>\n\n 并 flush）。
// 抽为包级函数，便于在 SSE 响应头就绪前的早期拦截（如预算护栏）复用，
// 而不必依赖仅在响应头设置后才定义的局部 emit 闭包。
func writeSSEEvent(c *gin.Context, evtType string, data gin.H) {
	data["type"] = evtType
	b, _ := json.Marshal(data)
	_, _ = c.Writer.WriteString("data: " + string(b) + "\n\n")
	c.Writer.Flush()
}

// aguiConverter 将 Agent 事件流转换为 AG-UI 协议 SSE 事件。
// 设计为不依赖 gin.Context 的纯函数，便于单元测试（见 sse_test.go）。
type aguiConverter struct {
	openCalls map[string]*aguiToolCall
	autoInc   int
	msgID     string
	ds        *engine.DeltaState // 文本去重状态（优先 Delta，未出现增量才回退 Message，见 M0.5-04）
	// circuitBroken 标记本轮是否因护栏熔断（预算耗尽）提前结束（M1-13 运行级兜底）。
	// 命中时 partial 文本已通过 TEXT_MESSAGE_CONTENT 增量推送，handler 仍应将其落库。
	circuitBroken bool
}

type aguiToolCall struct {
	name string
}

func newAGUIConverter() *aguiConverter {
	return &aguiConverter{
		openCalls: map[string]*aguiToolCall{},
		msgID:     "msg-" + uuid.NewString()[:8],
		ds:        engine.NewDeltaState(),
	}
}

// Convert 读取 Agent 事件流（引擎归一化后的 StreamEvent DTO），通过 emit 输出 AG-UI 事件，
// 返回累积的助手文本。事件流携带错误（IsError）时返回非 nil 错误（RUN_ERROR 已先行上报）。
// 入参事件已为引擎层 StreamEvent DTO（M6-02：api 不再直接依赖框架 event 包）。
func (cv *aguiConverter) Convert(ch <-chan engine.StreamEvent, emit func(string, gin.H)) (string, error) {
	var sb strings.Builder
	for ev := range ch {
		if ev.IsError {
			if ev.CircuitBreak {
				// 运行级兜底（M1-13）：护栏熔断是优雅终止，partial 文本已在前面
				// 以 TEXT_MESSAGE_CONTENT 增量推送给客户端。这里追加一条明确的熔断
				// 提示，并标记 circuitBroken，使 handler 仍把 partial 文本落库，
				// 而非当作运行错误丢弃。
				cv.circuitBroken = true
				emit("RUN_ERROR", gin.H{"message": engine.CircuitBreakNotice()})
				cv.closeOpenCalls(emit)
				return sb.String(), fmt.Errorf("%s", ev.ErrorMsg)
			}
			emit("RUN_ERROR", gin.H{"message": ev.ErrorMsg})
			cv.closeOpenCalls(emit)
			return sb.String(), fmt.Errorf("%s", ev.ErrorMsg)
		}
		for i := range ev.Choices {
			choice := ev.Choices[i]
			// 文本去重复用 engine.DeltaState 的同一规则：优先流式增量 DeltaContent，
			// 仅当整轮未出现任何增量时才回退到非流式整块 MessageContent（终帧
			// 重复文本会被跳过，避免重复一倍）。两处行为由 M0.5-04 统一保证。
			if t := cv.ds.Text(choice.DeltaContent, choice.MessageContent); t != "" {
				emit("TEXT_MESSAGE_CONTENT", gin.H{
					"messageId": cv.msgID,
					"delta":     t,
				})
				sb.WriteString(t)
			}
			// 工具调用：流式走 Delta.ToolCalls，非流式走 Message.ToolCalls（已由引擎归一化到同一切片）。
			cv.onToolCalls(choice.ToolCalls, emit)
		}
	}
	// 流正常结束，关闭任何仍处于打开状态的工具调用。
	cv.closeOpenCalls(emit)
	return sb.String(), nil
}

// onToolCalls 处理一批工具调用事件，输出 TOOL_CALL_START / TOOL_CALL_ARGS。
func (cv *aguiConverter) onToolCalls(tcs []engine.StreamToolCall, emit func(string, gin.H)) {
	for i := range tcs {
		tc := tcs[i]
		id := tc.ID
		if id == "" {
			cv.autoInc++
			id = fmt.Sprintf("tool-%d", cv.autoInc)
		}
		if _, ok := cv.openCalls[id]; !ok {
			// 新工具调用开始：先关闭已打开的调用（流式同一时刻只处理一个）。
			cv.closeOpenCalls(emit)
			name := tc.Name
			emit("TOOL_CALL_START", gin.H{
				"toolCallId":      id,
				"toolCallName":    name,
				"parentMessageId": cv.msgID,
			})
			cv.openCalls[id] = &aguiToolCall{name: name}
		}
		if len(tc.Arguments) > 0 {
			emit("TOOL_CALL_ARGS", gin.H{
				"toolCallId": id,
				"delta":      tc.Arguments,
			})
		}
	}
}

// closeOpenCalls 输出 TOOL_CALL_END 并清空打开状态（幂等）。
func (cv *aguiConverter) closeOpenCalls(emit func(string, gin.H)) {
	for id := range cv.openCalls {
		emit("TOOL_CALL_END", gin.H{"toolCallId": id})
		delete(cv.openCalls, id)
	}
}
