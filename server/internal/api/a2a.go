package api

import (
	"errors"
	"net/http"

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
				Streaming:              false,
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
// 协议：POST /api/a2a，请求体为 JSON-RPC 2.0 信封，method=message/send（或 tasks/send）。
// 经统一 Gateway（M4-04）跑引擎，复用会话串行锁、多轮记忆、预算护栏与用量计量；
// 外部任务以 ChannelA2A 标记，与 Web/CLI/定时/Webhook 收敛到同一套 Runner。
func A2AHandler(gw *Gateway) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, a2a.JSONRPCError(nil, -32001, "未认证"))
			return
		}

		var req a2a.JSONRPCRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, a2a.JSONRPCError(nil, -32700, "无效的 JSON-RPC 请求: "+err.Error()))
			return
		}
		if req.JSONRPC != "2.0" {
			c.JSON(http.StatusBadRequest, a2a.JSONRPCError(req.ID, -32600, "jsonrpc 字段必须为 2.0"))
			return
		}
		if req.Method != a2a.MethodMessageSend && req.Method != a2a.MethodTasksSend {
			c.JSON(http.StatusNotFound, a2a.JSONRPCError(req.ID, -32601, "不支持的方法: "+req.Method))
			return
		}

		text := req.Params.Message.Text()
		if text == "" {
			c.JSON(http.StatusBadRequest, a2a.JSONRPCError(req.ID, -32602, "message 中缺少文本内容"))
			return
		}

		// 任务 id 复用为会话 key：同一 id 的后续 message/send 在该会话内多轮续聊。
		taskID := req.Params.ID
		if taskID == "" {
			taskID = repo.NewSessionKey()
		}

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
}
