package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/anmingwei/go-multi-agent-v2/internal/crypto"
	"github.com/anmingwei/go-multi-agent-v2/internal/middleware"
	"github.com/anmingwei/go-multi-agent-v2/internal/model"
	"github.com/anmingwei/go-multi-agent-v2/internal/provider"
	"github.com/anmingwei/go-multi-agent-v2/internal/repo"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// providerRequest is the JSON body for create/update operations.
//   - protocol must be one of openai/anthropic/gemini
//   - api_key is the plaintext secret; it is only stored/updated when provided
//     (on create it is required-ish via the caller, on update it is optional).
type providerRequest struct {
	Name        string `json:"name" binding:"required,max=128"`
	Protocol    string `json:"protocol" binding:"required,oneof=openai anthropic gemini"`
	BaseURL     string `json:"base_url"`
	APIKey      string `json:"api_key"`
	Description string `json:"description" binding:"max=512"`
}

// providerView is the JSON returned to clients. The API key is never echoed;
// only a boolean flag indicates whether a key is configured.
type providerView struct {
	ID          uint   `json:"id"`
	Name        string `json:"name"`
	Protocol    string `json:"protocol"`
	BaseURL     string `json:"base_url"`
	HasAPIKey   bool   `json:"has_api_key"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func toProviderView(p *model.Provider) providerView {
	return providerView{
		ID:          p.ID,
		Name:        p.Name,
		Protocol:    string(p.Protocol),
		BaseURL:     p.BaseURL,
		HasAPIKey:   p.APIKeyEnc != "",
		Description: p.Description,
		CreatedAt:   p.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   p.UpdatedAt.Format(time.RFC3339),
	}
}

// currentUserID extracts the authenticated user id from the Gin context.
func currentUserID(c *gin.Context) (uint, bool) {
	v, ok := c.Get(middleware.CtxUserID)
	if !ok {
		return 0, false
	}
	uid, ok := v.(uint)
	return uid, ok
}

// CreateProviderHandler handles POST /api/providers.
func CreateProviderHandler(db *gorm.DB, encKey []byte) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req providerRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}

		proto, ok := model.ParseProtocol(req.Protocol)
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid protocol"})
			return
		}

		p := &model.Provider{
			UserID:      uid,
			Name:        req.Name,
			Protocol:    proto,
			BaseURL:     req.BaseURL,
			Description: req.Description,
			Status:      "active",
		}
		// Encrypt the API key if the caller supplied one.
		if req.APIKey != "" {
			enc, err := crypto.Encrypt(req.APIKey, encKey)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encrypt api key"})
				return
			}
			p.APIKeyEnc = enc
		}

		if err := repo.CreateProvider(db, p); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create provider"})
			return
		}
		c.JSON(http.StatusCreated, toProviderView(p))
	}
}

// ListProvidersHandler handles GET /api/providers (current user's providers).
func ListProvidersHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		list, err := repo.ListProvidersByUser(db, uid)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list providers"})
			return
		}
		views := make([]providerView, 0, len(list))
		for i := range list {
			views = append(views, toProviderView(&list[i]))
		}
		c.JSON(http.StatusOK, gin.H{"providers": views})
	}
}

// GetProviderHandler handles GET /api/providers/:id.
func GetProviderHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		p, ok2 := lookupOwnedProvider(c, db, uid)
		if !ok2 {
			return // handler already wrote the response
		}
		c.JSON(http.StatusOK, toProviderView(p))
	}
}

// UpdateProviderHandler handles PUT /api/providers/:id.
func UpdateProviderHandler(db *gorm.DB, encKey []byte) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		p, ok2 := lookupOwnedProvider(c, db, uid)
		if !ok2 {
			return
		}

		var req providerRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		proto, ok := model.ParseProtocol(req.Protocol)
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid protocol"})
			return
		}

		p.Name = req.Name
		p.Protocol = proto
		p.BaseURL = req.BaseURL
		p.Description = req.Description
		// Only overwrite the encrypted key when a new one is supplied.
		if req.APIKey != "" {
			enc, err := crypto.Encrypt(req.APIKey, encKey)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encrypt api key"})
				return
			}
			p.APIKeyEnc = enc
		}

		if err := repo.UpdateProvider(db, p); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update provider"})
			return
		}
		c.JSON(http.StatusOK, toProviderView(p))
	}
}

// DeleteProviderHandler handles DELETE /api/providers/:id.
func DeleteProviderHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		p, ok2 := lookupOwnedProvider(c, db, uid)
		if !ok2 {
			return
		}
		if err := repo.DeleteProvider(db, p.ID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete provider"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "deleted", "id": p.ID})
	}
}

// ListProviderModelsHandler handles GET /api/providers/:id/models. It
// discovers the models exposed by the provider's upstream model-list endpoint
// and returns them. The discoverer caches results per provider for 5 minutes.
func ListProviderModelsHandler(db *gorm.DB, disc *provider.Discoverer) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		p, ok2 := lookupOwnedProvider(c, db, uid)
		if !ok2 {
			return // handler already wrote the response
		}

		models, cached, err := disc.FetchModels(p)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{
				"error":    "failed to discover models from provider",
				"detail":   err.Error(),
				"protocol": string(p.Protocol),
				"base_url": p.BaseURL,
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"provider_id": p.ID,
			"protocol":    string(p.Protocol),
			"base_url":    p.BaseURL,
			"cached":      cached,
			"models":      models,
		})
	}
}

// lookupOwnedProvider parses the :id param, loads the provider, and verifies
// ownership. On any failure it writes the appropriate HTTP error and returns
// (nil, false); otherwise it returns the provider and (provider, true).
func lookupOwnedProvider(c *gin.Context, db *gorm.DB, uid uint) (*model.Provider, bool) {
	idParam := c.Param("id")
	id, err := strconv.ParseUint(idParam, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return nil, false
	}
	p, err := repo.GetProviderByID(db, uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "provider not found"})
		return nil, false
	}
	if p.UserID != uid {
		c.JSON(http.StatusForbidden, gin.H{"error": "not allowed to access this provider"})
		return nil, false
	}
	return p, true
}
