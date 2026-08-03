package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/crypto"
	"github.com/ayanmw/multiagent2/server/internal/engine"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	codectool "github.com/ayanmw/multiagent2/server/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/event"
	framework "trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// streamChatRequest 是 SSE 流式对话的请求体（M0.5-06：message 由 GET query 改为 POST body）。
type streamChatRequest struct {
	Message      string `json:"message"`
	ModelID      uint   `json:"model_id"`
	WorkspaceKey string `json:"workspace_key"` // 可选：绑定到某 workspace（M1-07），Executor 在其目录执行
}

// StreamChatHandler handles POST /api/chat/:session_id/stream.
// 将 Agent 事件流转换为 AG-UI 协议的 SSE 事件并逐条推送，会话与消息持久化到 DB。
//
// 请求参数：
//   - 路径 :session_id  会话标识（为空时服务端新建）
//   - 请求体 message    用户消息（必填）
//   - 请求体 model_id   指定托管模型 id（可选，缺省用默认启用模型）
//
// 注意：message 走 POST body 而非 GET query，避免明文进入访问日志（M0.5-06）。
//
// engineTimeout 为单次对话流式超时（由配置 ENGINE_TIMEOUT_SECONDS 注入，M0.5-05）。
// workspaceRoot 为用户工作区根目录（M1-06 CodeAct 工具的执行根，按 <root>/<uid> 隔离）。
// team 为 CodeTeam 编排配置（M1-08/M1-09）：EnableSubAgents=true 启用子代理委托
// （Orchestrator→Coder），叠加 EnableReviewer=true 时加入只读 Reviewer 形成审阅回环。
func StreamChatHandler(db *gorm.DB, encKey []byte, engineTimeout time.Duration, workspaceRoot string, team engine.TeamConfig) gin.HandlerFunc {
	enableSubAgents := team.EnableSubAgents
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

		// 解析本次对话要使用的模型与 Provider。
		m, p, err := resolveChatModel(db, uid, modelID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		// 解密 Provider 的 APIKey（AES-GCM）。
		apiKey := ""
		if p.APIKeyEnc != "" {
			dec, derr := crypto.Decrypt(p.APIKeyEnc, encKey)
			if derr != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "解密 provider key 失败"})
				return
			}
			apiKey = dec
		}

		// 获取或创建会话并持久化用户消息。
		sess, err := repo.GetOrCreateSession(db, uid, sessionKey)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "创建会话失败"})
			return
		}
		if aerr := repo.AppendMessage(db, sess.ID, "user", message); aerr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "写入用户消息失败"})
			return
		}

		// 解析对话绑定的工作目录（M1-07）：指定 workspace_key 切到该目录，否则复用已绑定目录。
		wsLocalDir, werr := resolveWorkspaceLocalDir(db, uid, req.WorkspaceKey, sess)
		if werr != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "workspace not found"})
			return
		}

		// 准备 SSE 响应头。
		c.Writer.Header().Set("Content-Type", "text/event-stream")
		c.Writer.Header().Set("Cache-Control", "no-cache")
		c.Writer.Header().Set("Connection", "keep-alive")
		c.Writer.Header().Set("X-Accel-Buffering", "no")

		// emit 向客户端推送一条 AG-UI SSE 事件。
		emit := func(evtType string, data gin.H) {
			data["type"] = evtType
			b, _ := json.Marshal(data)
			_, _ = c.Writer.WriteString("data: " + string(b) + "\n\n")
			c.Writer.Flush()
		}

		runID := "run-" + uuid.NewString()[:8]
		emit("RUN_STARTED", gin.H{
			"threadId": sess.SessionKey,
			"runId":    runID,
		})

		// 构造引擎并启动流式对话。M1-06：为当前用户装配 CodeAct 工具（工作目录隔离在
		// 本次解析的 workspace 目录内，命令经危险命令策略包装）。
		// M1-08：子代理委托模式下改由 Coder 子代理持有 CodeAct 工具，根 Agent 不装配写工具。
		workdir, dErr := ensureWorkdir(workspaceRoot, uid, wsLocalDir)
		if dErr != nil {
			emit("RUN_ERROR", gin.H{"message": dErr.Error()})
			emit("RUN_FINISHED", gin.H{"threadId": sess.SessionKey, "runId": runID})
			return
		}
		var tools []tool.Tool
		if !enableSubAgents {
			var tErr error
			tools, tErr = codectool.NewCodeAct(workdir)
			if tErr != nil {
				emit("RUN_ERROR", gin.H{"message": "构建代码执行工具失败: " + tErr.Error()})
				emit("RUN_FINISHED", gin.H{"threadId": sess.SessionKey, "runId": runID})
				return
			}
		}
		eng, err := engine.New(engine.ModelConfig{
			ModelID:  m.ModelID,
			BaseURL:  p.BaseURL,
			APIKey:   apiKey,
			Protocol: string(p.Protocol),
			Timeout:  engineTimeout,
			Tools:    tools,
			Team:     team,
			Workdir:  workdir,
		})
		if err != nil {
			emit("RUN_ERROR", gin.H{"message": err.Error()})
			emit("RUN_FINISHED", gin.H{"threadId": sess.SessionKey, "runId": runID})
			return
		}
		defer eng.Close()

		ch, rerr := eng.Stream(c.Request.Context(), sess.SessionKey, message,
			// 多轮记忆（M0.5-01）：从 DB 加载历史（排除刚写入的当前 user 消息）回灌引擎。
			loadChatHistory(db, sess.ID, 1))
		if rerr != nil {
			emit("RUN_ERROR", gin.H{"message": rerr.Error()})
			emit("RUN_FINISHED", gin.H{"threadId": sess.SessionKey, "runId": runID})
			return
		}

		// 转换 Agent 事件流为 AG-UI 事件并推送；返回累积的助手文本。
		conv := newAGUIConverter()
		text, convErr := conv.Convert(ch, func(t string, d gin.H) {
			// 客户端断开则停止推送。
			if c.Request.Context().Err() != nil {
				return
			}
			emit(t, d)
		})

		// 仅在正常结束或护栏熔断（partial 结果）时落库助手消息（M1-13 运行级兜底：
		// circuitBroken 时保留已产出的部分结果）；客户端中途断开不写脏数据。
		if convErr == nil || conv.circuitBroken {
			if perr := repo.AppendMessage(db, sess.ID, "assistant", text); perr != nil {
				emit("RUN_ERROR", gin.H{"message": "写入助手消息失败: " + perr.Error()})
			}
		}
		emit("RUN_FINISHED", gin.H{"threadId": sess.SessionKey, "runId": runID})
	}
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

// Convert 读取 Agent 事件流，通过 emit 输出 AG-UI 事件，返回累积的助手文本。
// 事件流携带错误（IsError）时返回非 nil 错误（RUN_ERROR 已先行上报）。
func (cv *aguiConverter) Convert(ch <-chan *event.Event, emit func(string, gin.H)) (string, error) {
	var sb strings.Builder
	for ev := range ch {
		if ev == nil || ev.Response == nil {
			continue
		}
		if ev.IsError() {
			msg := "agent error"
			if ev.Response.Error != nil {
				msg = ev.Response.Error.Message
			}
			if engine.IsCircuitBreakEvent(ev) {
				// 运行级兜底（M1-13）：护栏熔断是优雅终止，partial 文本已在前面
				// 以 TEXT_MESSAGE_CONTENT 增量推送给客户端。这里追加一条明确的熔断
				// 提示，并标记 circuitBroken，使 handler 仍把 partial 文本落库，
				// 而非当作运行错误丢弃。
				cv.circuitBroken = true
				emit("RUN_ERROR", gin.H{"message": engine.CircuitBreakNotice()})
				cv.closeOpenCalls(emit)
				return sb.String(), fmt.Errorf("%s", msg)
			}
			emit("RUN_ERROR", gin.H{"message": msg})
			cv.closeOpenCalls(emit)
			return sb.String(), fmt.Errorf("%s", msg)
		}
		for i := range ev.Response.Choices {
			choice := ev.Response.Choices[i]
			// 文本去重复用 engine.DeltaState 的同一规则：优先流式增量 Delta.Content，
			// 仅当整轮未出现任何增量时才回退到非流式整块 Message.Content（终帧
			// 重复文本会被跳过，避免重复一倍）。两处行为由 M0.5-04 统一保证。
			if t := cv.ds.Text(choice.Delta.Content, choice.Message.Content); t != "" {
				emit("TEXT_MESSAGE_CONTENT", gin.H{
					"messageId": cv.msgID,
					"delta":     t,
				})
				sb.WriteString(t)
			}
			// 工具调用：流式走 Delta.ToolCalls，非流式走 Message.ToolCalls。
			tcs := choice.Delta.ToolCalls
			if len(tcs) == 0 {
				tcs = choice.Message.ToolCalls
			}
			cv.onToolCalls(tcs, emit)
		}
	}
	// 流正常结束，关闭任何仍处于打开状态的工具调用。
	cv.closeOpenCalls(emit)
	return sb.String(), nil
}

// onToolCalls 处理一批工具调用事件，输出 TOOL_CALL_START / TOOL_CALL_ARGS。
func (cv *aguiConverter) onToolCalls(tcs []framework.ToolCall, emit func(string, gin.H)) {
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
			name := tc.Function.Name
			emit("TOOL_CALL_START", gin.H{
				"toolCallId":      id,
				"toolCallName":    name,
				"parentMessageId": cv.msgID,
			})
			cv.openCalls[id] = &aguiToolCall{name: name}
		}
		if len(tc.Function.Arguments) > 0 {
			emit("TOOL_CALL_ARGS", gin.H{
				"toolCallId": id,
				"delta":      string(tc.Function.Arguments),
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
