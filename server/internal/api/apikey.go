package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/anmingwei/go-multi-agent-v2/internal/auth"
	"github.com/anmingwei/go-multi-agent-v2/internal/middleware"
	"github.com/anmingwei/go-multi-agent-v2/internal/model"
	"github.com/anmingwei/go-multi-agent-v2/internal/repo"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type createAPIKeyRequest struct {
	Name       string `json:"name" binding:"required,max=128"`
	ExpiresIn  *int   `json:"expires_in_days"`
}

type apiKeyView struct {
	ID         uint   `json:"id"`
	Name       string `json:"name"`
	Prefix     string `json:"prefix"`
	Status     string `json:"status"`
	LastUsedAt string `json:"last_used_at,omitempty"`
	ExpiresAt  string `json:"expires_at,omitempty"`
	CreatedAt  string `json:"created_at"`
}

func toAPIKeyView(ak *model.APIKey) apiKeyView {
	v := apiKeyView{
		ID:        ak.ID,
		Name:      ak.Name,
		Prefix:    ak.Prefix,
		Status:    string(ak.Status),
		CreatedAt: ak.CreatedAt.Format(time.RFC3339),
	}
	if ak.LastUsedAt != nil {
		v.LastUsedAt = ak.LastUsedAt.Format(time.RFC3339)
	}
	if ak.ExpiresAt != nil {
		v.ExpiresAt = ak.ExpiresAt.Format(time.RFC3339)
	}
	return v
}

// CreateAPIKeyHandler handles POST /api/auth/apikeys.
// Returns the raw key exactly once; subsequent calls only return metadata.
func CreateAPIKeyHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req createAPIKeyRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		userID, ok := c.Get(middleware.CtxUserID)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		uid := userID.(uint)

		raw, prefix, hash := auth.GenerateAPIKey()
		ak := &model.APIKey{
			UserID:  uid,
			Name:    req.Name,
			KeyHash: hash,
			Prefix:  prefix,
			Status:  model.APIKeyStatusActive,
		}
		if req.ExpiresIn != nil && *req.ExpiresIn > 0 {
			t := time.Now().AddDate(0, 0, *req.ExpiresIn)
			ak.ExpiresAt = &t
		}

		if err := repo.CreateAPIKey(db, ak); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create api key"})
			return
		}

		resp := toAPIKeyView(ak)
		c.JSON(http.StatusCreated, gin.H{
			"api_key":     raw, // raw secret, returned ONLY here
			"id":          resp.ID,
			"name":        resp.Name,
			"prefix":      resp.Prefix,
			"status":      resp.Status,
			"expires_at":  resp.ExpiresAt,
			"created_at":  resp.CreatedAt,
		})
	}
}

// ListAPIKeysHandler handles GET /api/auth/apikeys.
func ListAPIKeysHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := c.Get(middleware.CtxUserID)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		uid := userID.(uint)

		keys, err := repo.ListAPIKeysByUser(db, uid)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list api keys"})
			return
		}

		views := make([]apiKeyView, 0, len(keys))
		for i := range keys {
			views = append(views, toAPIKeyView(&keys[i]))
		}
		c.JSON(http.StatusOK, gin.H{"api_keys": views})
	}
}

// RevokeAPIKeyHandler handles DELETE /api/auth/apikeys/:id.
func RevokeAPIKeyHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := c.Get(middleware.CtxUserID)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		uid := userID.(uint)

		idParam := c.Param("id")
		id, err := strconv.ParseUint(idParam, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
			return
		}
		keyID := uint(id)

		ak, err := repo.GetAPIKeyByID(db, keyID)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "api key not found"})
			return
		}
		// Enforce ownership: a user may only revoke their own keys.
		if ak.UserID != uid {
			c.JSON(http.StatusForbidden, gin.H{"error": "not allowed to revoke this api key"})
			return
		}

		if err := repo.RevokeAPIKey(db, keyID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to revoke api key"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "revoked", "id": keyID})
	}
}
