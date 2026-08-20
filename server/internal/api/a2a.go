package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/ayanmw/multiagent2/server/internal/a2a"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/gin-gonic/gin"
)

// AgentCardHandler 暴露本平台的 A2A Agent Card（公开、无需鉴权），供外部 A2A client 发现能力。
// 协议：GET /.well-known/agent.json，返回 AgentCard JSON（对齐 Google A2A 规范）。
// url 字段按当前请求 Host 动态拼接，使 card 始终指向真实可达的 /api/a2a 端点。
func AgentCardHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		scheme := "http"
		if c.Request.TLS != nil {
			scheme = "https"
		}
		baseURL := scheme + "://" + c.Request.Host
		card := a2a.AgentCard{
			ProtocolVersion: a2a.ProtocolVersion,
			Name:            "GoMultiAgentV2",
			Description:     "企业级多 Agent 协作 CodeAgent 平台：代码生成、文件编辑、Shell 执行与自主 Loop",
			URL:             baseURL + "/api/a2a",
			Version:         "0.15.1",
			Capabilities: a2a.Capabilities{
				// M8-01：服务端已实现 message/stream（SSE 进度流），对外声明流式能力，
				// 外部 client 可据此选择 message/stream 而非 message/send。
				Streaming:              true,
				PushNotifications:      false,
				StateTransitionHistory: true,
			},
			Skills: []a2a.Skill{
				{
					ID:          "code-agent",
					Name:        "代码智能体",
					Description: "执行代码生成、文件读写与编辑、Shell 命令执行等任务",
					Tags:        []string{"coding", "agent", "shell"},
					Examples:    []string{"帮我写一个 Go HTTP 服务", "在 workspace 里创建一个 README"},
					InputModes:  []string{"text"},
					OutputModes: []string{"text"},
				},
			},
			DefaultInputModes:  []string{"text"},
			DefaultOutputModes: []string{"text"},
			Authentication:     a2a.Authentication{Schemes: []string{"apiKey"}},
		}
		c.JSON(http.StatusOK, card)
	}
}

// A2AHandler 实现 A2A server 的 JSON-RPC 任务入口（外部 Agent 经 API Key 调用）。
// 协议：POST /api/a2a，请求体为 JSON-RPC 2.0 信封；按 method 分发：
//   - message/send、tasks/send → 非流式任务（handleA2ASend）；
//   - message/stream → 流式任务（handleA2AStream，SSE 事件流，M8-01）。
//
// 均经统一 Gateway（M4-04）跑引擎，复用会话串行锁、多轮记忆、预算护栏与用量计量；
// 外部任务以 ChannelA2A 标记，与 Web/CLI/定时/Webhook 收敛到同一套 Runner。
func A2AHandler(gw *Gateway) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, a2a.JSONRPCError(nil, -32001, "未认证"))
			return
		}

		req, taskID, code, errResp := parseA2ARPC(c)
		if errResp != nil {
			c.JSON(code, *errResp)
			return
		}

		switch req.Method {
		case a2a.MethodMessageSend, a2a.MethodTasksSend:
			handleA2ASend(c, gw, uid, req, taskID)
		case a2a.MethodMessageStream:
			handleA2AStream(c, gw, uid, req, taskID)
		}
	}
}

// parseA2ARPC 解析并校验 A2A JSON-RPC 请求信封：
// jsonrpc=2.0（-32600）、方法白名单（-32601）、message 文本非空（-32602）。
// 成功时返回规范化请求与任务 id（缺省自动生成）；失败时返回 HTTP 状态码与错误信封。
func parseA2ARPC(c *gin.Context) (req a2a.JSONRPCRequest, taskID string, code int, errResp *a2a.JSONRPCResponse) {
	if err := c.ShouldBindJSON(&req); err != nil {
		r := a2a.JSONRPCError(nil, -32700, "无效的 JSON-RPC 请求: "+err.Error())
		return req, "", http.StatusBadRequest, &r
	}
	if req.JSONRPC != "2.0" {
		r := a2a.JSONRPCError(req.ID, -32600, "jsonrpc 字段必须为 2.0")
		return req, "", http.StatusBadRequest, &r
	}
	switch req.Method {
	case a2a.MethodMessageSend, a2a.MethodTasksSend, a2a.MethodMessageStream:
	default:
		r := a2a.JSONRPCError(req.ID, -32601, "不支持的方法: "+req.Method)
		return req, "", http.StatusNotFound, &r
	}
	if strings.TrimSpace(req.Params.Message.Text()) == "" {
		r := a2a.JSONRPCError(req.ID, -32602, "message 中缺少文本内容")
		return req, "", http.StatusBadRequest, &r
	}
	// 任务 id 复用为会话 key：同一 id 的后续调用在该会话内多轮续聊。
	taskID = req.Params.ID
	if taskID == "" {
		taskID = repo.NewSessionKey()
	}
	return req, taskID, 0, nil
}

// handleA2ASend 实现非流式 message/send：调用引擎后一次性返回最终 Task。
func handleA2ASend(c *gin.Context, gw *Gateway, uid uint, req a2a.JSONRPCRequest, taskID string) {
	text := req.Params.Message.Text()

	res, err := gw.Run(c.Request.Context(), Request{
		Channel:    ChannelA2A,
		UserID:     uid,
		SessionKey: taskID,
		Message:    text,
	})
	if err != nil {
		var be *BudgetExhaustedError
		if errors.As(err, &be) {
			c.JSON(http.StatusTooManyRequests, a2a.JSONRPCError(req.ID, -32003, "预算耗尽，待恢复"))
			return
		}
		c.JSON(http.StatusBadGateway, a2a.JSONRPCError(req.ID, -32002, "调用模型失败: "+err.Error()))
		return
	}

	task := a2a.Task{
		ID:        taskID,
		SessionID: res.SessionKey,
		Status:    a2a.TaskStatus{State: "completed", Timestamp: a2a.NowRFC3339()},
		History: []a2a.Message{
			{Role: "user", Parts: []a2a.Part{{Text: text}}},
			{Role: "agent", Parts: []a2a.Part{{Text: res.Reply}}},
		},
		Artifacts: []a2a.Artifact{},
	}
	c.JSON(http.StatusOK, a2a.JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: task})
}

// handleA2AStream 实现流式 message/stream（M8-01）：以 SSE 事件流返回任务进度。
// 帧序列（对齐 Google A2A 规范 0.2.x）：
//  1. 首帧：JSON-RPC 响应信封（result=Task，状态 working），客户端先拿到任务引用；
//  2. 中间帧：event: task.status_update（state=working，Message 携带文本增量=进度流）；
//  3. 终帧：event: task.status_update（state=completed/failed，Message 携带完整回复/错误）；
//  4. 补帧：event: task.artifact_update（完整回复作为 reply.txt 产物，外部 client 可展示）。
//
// 引擎事件经统一 Gateway 的 gw.Stream 桥接（AG-UI 事件 → A2A 状态事件），
// 因此外部 client 与 Web SSE 看到同一份增量文本，进度逐段可见。
func handleA2AStream(c *gin.Context, gw *Gateway, uid uint, req a2a.JSONRPCRequest, taskID string) {
	text := req.Params.Message.Text()

	// 准备 SSE 响应头。
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")

	// ① 首帧：JSON-RPC 响应信封，result=Task（状态 working）。
	initial := a2a.Task{
		ID:        taskID,
		SessionID: taskID,
		Status:    a2a.TaskStatus{State: "working", Timestamp: a2a.NowRFC3339()},
	}
	writeA2ASSE(c, "", a2a.JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: initial})

	// ② 进度帧：AG-UI 文本增量 → task.status_update working（增量即进度流）。
	var sb strings.Builder
	emit := func(evtType string, data gin.H) {
		if c.Request.Context().Err() != nil {
			return // 客户端断开，停止推送（引擎侧 ctx 已取消）。
		}
		if evtType == "TEXT_MESSAGE_CONTENT" {
			if d, _ := data["delta"].(string); d != "" {
				sb.WriteString(d)
				writeA2ASSE(c, a2a.EventTaskStatusUpdate, a2a.TaskStatusUpdateEvent{
					ID: taskID,
					Status: a2a.TaskStatus{
						State:     "working",
						Timestamp: a2a.NowRFC3339(),
						Message:   &a2a.Message{Role: "agent", Parts: []a2a.Part{{Text: d}}},
					},
				})
			}
		}
	}

	res, runErr := gw.Stream(c.Request.Context(), Request{
		Channel:    ChannelA2A,
		UserID:     uid,
		SessionKey: taskID,
		Message:    text,
	}, emit)

	// ③ 终帧：completed / failed。
	if runErr != nil {
		msg := "模型调用失败: " + runErr.Error()
		var be *BudgetExhaustedError
		if errors.As(runErr, &be) {
			msg = "预算耗尽，待恢复"
		}
		writeA2ASSE(c, a2a.EventTaskStatusUpdate, a2a.TaskStatusUpdateEvent{
			ID: taskID,
			Status: a2a.TaskStatus{
				State:     "failed",
				Timestamp: a2a.NowRFC3339(),
				Message:   &a2a.Message{Role: "agent", Parts: []a2a.Part{{Text: msg}}},
			},
		})
		return
	}

	finalText := res.Reply
	if finalText == "" {
		finalText = sb.String() // 兜底：流中已累积的增量文本。
	}
	writeA2ASSE(c, a2a.EventTaskStatusUpdate, a2a.TaskStatusUpdateEvent{
		ID: taskID,
		Status: a2a.TaskStatus{
			State:     "completed",
			Timestamp: a2a.NowRFC3339(),
			Message:   &a2a.Message{Role: "agent", Parts: []a2a.Part{{Text: finalText}}},
		},
	})
	// ④ 产物帧：完整回复作为产物（外部 client 可展示/落盘）。
	writeA2ASSE(c, a2a.EventTaskArtifactUpdate, a2a.TaskArtifactUpdateEvent{
		ID: taskID,
		Artifact: a2a.Artifact{
			ArtifactID: "reply-1",
			Name:       "reply.txt",
			Parts:      []a2a.Part{{Text: finalText}},
		},
	})
}

// writeA2ASSE 向客户端写一条 A2A SSE 帧（可选 event: 行 + data: <json>\n\n）并 flush。
func writeA2ASSE(c *gin.Context, event string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	var buf strings.Builder
	if event != "" {
		buf.WriteString("event: " + event + "\n")
	}
	buf.WriteString("data: " + string(b) + "\n\n")
	_, _ = c.Writer.WriteString(buf.String())
	c.Writer.Flush()
}
