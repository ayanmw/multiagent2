package api

import (
	"errors"
	"net/http"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"

	"github.com/ayanmw/multiagent2/server/internal/crypto"
	"github.com/ayanmw/multiagent2/server/internal/engine"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	codectool "github.com/ayanmw/multiagent2/server/internal/tool"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// chatRequest 是 POST /api/chat 的请求体。
type chatRequest struct {
	Message      string `json:"message" binding:"required"`
	ModelID      uint   `json:"model_id"`      // 可选：指定托管模型 id；为空则用用户的默认启用模型
	SessionID    string `json:"session_id"`    // 可选：多轮会话 id（M0-10 仅内存态）
	WorkspaceKey string `json:"workspace_key"` // 可选：绑定到某 workspace（M1-07），Executor 在其目录执行
}

// chatResponse 是 /api/chat 的返回体。
type chatResponse struct {
	Reply     string `json:"reply"`
	SessionID string `json:"session_id"`
	ModelID   uint   `json:"model_id"`
	ModelName string `json:"model_name"`
}

// ChatHandler handles POST /api/chat.
// 它从 DB 解析出已启用的 Model + Provider，解密 APIKey，构造 engine.Engine 并调用 LLM 得到回复。
// engineTimeout 为单次对话流式超时（由配置 ENGINE_TIMEOUT_SECONDS 注入，M0.5-05）。
// workspaceRoot 为用户工作区根目录（M1-06 CodeAct 工具的执行根，按 <root>/<uid> 隔离）。
// team 为 CodeTeam 编排配置（M1-08/M1-09）：EnableSubAgents=true 时启用子代理委托
// （Orchestrator→Coder，CodeAct 工具集装配给 Coder，Orchestrator 自身不持有写工具）；
// 叠加 EnableReviewer=true 时再加入只读 Reviewer，形成「实现→审阅→修复」回环。
func ChatHandler(db *gorm.DB, encKey []byte, engineTimeout time.Duration, workspaceRoot string, team engine.TeamConfig) gin.HandlerFunc {
	enableSubAgents := team.EnableSubAgents
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		var req chatRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		// 解析本次对话要使用的模型与 Provider。
		m, p, err := resolveChatModel(db, uid, req.ModelID)
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

		// 解析/创建会话并持久化用户消息，用于多轮记忆（M0.5-01）。
		sessionKey := req.SessionID
		if sessionKey == "" {
			sessionKey = repo.NewSessionKey()
		}
		sess, serr := repo.GetOrCreateSession(db, uid, sessionKey)
		if serr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "创建会话失败"})
			return
		}
		if aerr := repo.AppendMessage(db, sess.ID, "user", req.Message); aerr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "写入用户消息失败"})
			return
		}

		// 解析对话绑定的工作目录（M1-07）：若指定 workspace_key 则切到该 workspace 目录，
		// 否则复用已绑定目录，皆无则回退默认 WorkspaceRoot/<uid>。
		wsLocalDir, werr := resolveWorkspaceLocalDir(db, uid, req.WorkspaceKey, sess)
		if werr != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "workspace not found"})
			return
		}

		// 构造引擎并对话。M1-06：为当前用户装配 CodeAct 工具（shell_exec/file_read/file_write/file_edit），
		// 工作目录为本次解析出的 workspace 目录（或默认 WorkspaceRoot/<uid>），命令经危险命令策略包装。
		// M1-08：子代理委托模式下改由 Coder 子代理持有这批工具，此处不再装配到根 Agent。
		workdir, dErr := ensureWorkdir(workspaceRoot, uid, wsLocalDir)
		if dErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": dErr.Error()})
			return
		}
		var tools []tool.Tool
		if !enableSubAgents {
			var tErr error
			tools, tErr = codectool.NewCodeAct(workdir)
			if tErr != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "构建代码执行工具失败: " + tErr.Error()})
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
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		defer eng.Close()

		// 多轮记忆（M0.5-01）：从 DB 加载历史（排除刚写入的当前 user 消息）回灌引擎。
		history := loadChatHistory(db, sess.ID, 1)

		reply, err := eng.Chat(c.Request.Context(), sessionKey, req.Message, history)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "调用模型失败", "detail": err.Error()})
			return
		}

		// 仅在正常结束时落库助手消息（客户端中途断开不写脏数据）。
		if perr := repo.AppendMessage(db, sess.ID, "assistant", reply); perr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "写入助手消息失败: " + perr.Error()})
			return
		}

		c.JSON(http.StatusOK, chatResponse{
			Reply:     reply,
			SessionID: sessionKey,
			ModelID:   m.ID,
			ModelName: m.Name,
		})
	}
}

// resolveChatModel 选出本次对话使用的模型与 Provider：
//   - 指定 model_id：校验归属与启用状态；
//   - 未指定：取用户默认且启用的模型，退化取第一个启用模型；
//   - 都没有：报错提示先创建 Provider 并启用模型。
func resolveChatModel(db *gorm.DB, uid, modelID uint) (*model.Model, *model.Provider, error) {
	var m *model.Model
	var err error
	if modelID != 0 {
		m, err = repo.GetModelByID(db, modelID, uid)
		if err != nil {
			return nil, nil, errors.New("指定的模型不存在或无权限")
		}
		if !m.Enabled {
			return nil, nil, errors.New("指定的模型未启用")
		}
	} else {
		list, lerr := repo.ListEnabledModels(db, uid)
		if lerr != nil {
			return nil, nil, lerr
		}
		for i := range list {
			if list[i].IsDefault {
				m = &list[i]
				break
			}
		}
		if m == nil && len(list) > 0 {
			m = &list[0] // 退化为第一个启用模型
		}
		if m == nil {
			return nil, nil, errors.New("当前用户没有已启用的模型，请先创建 Provider 并启用模型")
		}
	}

	p, perr := repo.GetProviderByID(db, m.ProviderID)
	if perr != nil {
		return nil, nil, errors.New("找不到模型对应的 Provider")
	}
	if p.UserID != uid {
		return nil, nil, errors.New("无权限使用该 Provider")
	}
	return m, p, nil
}
