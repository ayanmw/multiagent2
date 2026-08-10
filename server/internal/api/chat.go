package api

import (
	"errors"
	"net/http"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"
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
// 经统一 Gateway（M4-04）跑非流式对话：Gateway 负责「稳定 session_id + 每会话串行锁 +
// 构建引擎 + 多轮记忆 + 用量计量 + 落库」。本 handler 仅做鉴权、参数解析、预算护栏前置与响应封装。
func ChatHandler(gw *Gateway) gin.HandlerFunc {
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

		// 稳定分配 session_id（空则生成），用于预算检查与统一网关。
		sessionKey := req.SessionID
		if sessionKey == "" {
			sessionKey = repo.NewSessionKey()
		}

		// 预算护栏（M3-04）：在发起 LLM 调用前评估平台级预算，超限返回「预算耗尽，待恢复」。
		budgetEv, berr := gw.EvaluateBudget(uid, sessionKey)
		if berr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "预算评估失败"})
			return
		}
		if budgetEv.Blocked {
			writeBudgetBlockAudit(gw.DB(), uid, budgetEv)
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":  "预算耗尽，待恢复",
				"detail": "该用户/会话的平台级预算已用尽，请管理员提额后重试",
				"scope":  budgetEv.Scope,
				"used":   budgetEv.Used,
				"max":    budgetEv.Max,
			})
			return
		}

		res, err := gw.Run(c.Request.Context(), Request{
			Channel:      ChannelWeb,
			UserID:       uid,
			SessionKey:   sessionKey,
			Message:      req.Message,
			ModelID:      req.ModelID,
			WorkspaceKey: req.WorkspaceKey,
		})
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "调用模型失败", "detail": err.Error()})
			return
		}

		c.JSON(http.StatusOK, chatResponse{
			Reply:     res.Reply,
			SessionID: res.SessionKey,
			ModelID:   res.ModelID,
			ModelName: res.ModelName,
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
