package api

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/anmingwei/go-multi-agent-v2/internal/crypto"
	"github.com/anmingwei/go-multi-agent-v2/internal/engine"
	"github.com/anmingwei/go-multi-agent-v2/internal/model"
	"github.com/anmingwei/go-multi-agent-v2/internal/repo"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// chatRequest 是 POST /api/chat 的请求体。
type chatRequest struct {
	Message   string `json:"message" binding:"required"`
	ModelID   uint   `json:"model_id"`   // 可选：指定托管模型 id；为空则用用户的默认启用模型
	SessionID string `json:"session_id"` // 可选：多轮会话 id（M0-10 仅内存态）
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
func ChatHandler(db *gorm.DB, encKey []byte) gin.HandlerFunc {
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

		// 构造引擎并对话。
		eng, err := engine.New(engine.ModelConfig{
			ModelID:  m.ModelID,
			BaseURL:  p.BaseURL,
			APIKey:   apiKey,
			Protocol: string(p.Protocol),
		})
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		defer eng.Close()

		sessionID := req.SessionID
		if sessionID == "" {
			sessionID = fmt.Sprintf("sess-%d", time.Now().UnixNano())
		}

		reply, err := eng.Chat(c.Request.Context(), sessionID, req.Message)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "调用模型失败", "detail": err.Error()})
			return
		}

		c.JSON(http.StatusOK, chatResponse{
			Reply:     reply,
			SessionID: sessionID,
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
