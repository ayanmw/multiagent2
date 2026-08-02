package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/provider"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// modelView is the JSON shape returned for a managed model row.
type modelView struct {
	ID         uint   `json:"id"`
	ProviderID uint   `json:"provider_id"`
	ModelID    string `json:"model_id"`
	Name       string `json:"name"`
	OwnedBy    string `json:"owned_by"`
	Enabled    bool   `json:"enabled"`
	IsDefault  bool   `json:"is_default"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

func toModelView(m *model.Model) modelView {
	return modelView{
		ID:         m.ID,
		ProviderID: m.ProviderID,
		ModelID:    m.ModelID,
		Name:       m.Name,
		OwnedBy:    m.OwnedBy,
		Enabled:    m.Enabled,
		IsDefault:  m.IsDefault,
		CreatedAt:  m.CreatedAt.Format(time.RFC3339),
		UpdatedAt:  m.UpdatedAt.Format(time.RFC3339),
	}
}

// updateModelRequest is the body for PATCH/PUT of a model's flags. Both fields
// are optional; nil means "leave unchanged".
type updateModelRequest struct {
	Enabled   *bool `json:"enabled"`
	IsDefault *bool `json:"is_default"`
}

// SyncProviderModelsHandler handles POST /api/providers/:id/models/sync. It
// discovers the upstream model list (cached) and upserts each into the managed
// models table, then returns the full managed list for the provider.
func SyncProviderModelsHandler(db *gorm.DB, disc *provider.Discoverer) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		p, ok := lookupOwnedProvider(c, db, uid)
		if !ok {
			return
		}

		infos, cached, err := disc.FetchModels(p)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{
				"error":    "failed to discover models from provider",
				"detail":   err.Error(),
				"protocol": string(p.Protocol),
				"base_url": p.BaseURL,
			})
			return
		}

		for i := range infos {
			m := &model.Model{
				ProviderID: p.ID,
				UserID:     uid,
				ModelID:    infos[i].ID,
				Name:       infos[i].Name,
				OwnedBy:    infos[i].OwnedBy,
				Enabled:    false,
				IsDefault:  false,
			}
			if err := repo.UpsertModel(db, m); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to persist discovered models"})
				return
			}
		}

		managed, err := repo.ListModelsByProvider(db, p.ID, uid)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list managed models"})
			return
		}
		views := make([]modelView, 0, len(managed))
		for i := range managed {
			views = append(views, toModelView(&managed[i]))
		}
		c.JSON(http.StatusOK, gin.H{
			"provider_id": p.ID,
			"cached":      cached,
			"synced":      len(infos),
			"models":      views,
		})
	}
}

// ListManagedModelsHandler handles GET /api/providers/:id/models/managed. It
// returns the persisted (curated) model list for the provider, including the
// user's enable/default choices.
func ListManagedModelsHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		p, ok := lookupOwnedProvider(c, db, uid)
		if !ok {
			return
		}
		managed, err := repo.ListModelsByProvider(db, p.ID, uid)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list managed models"})
			return
		}
		views := make([]modelView, 0, len(managed))
		for i := range managed {
			views = append(views, toModelView(&managed[i]))
		}
		c.JSON(http.StatusOK, gin.H{
			"provider_id": p.ID,
			"models":      views,
		})
	}
}

// ListEnabledModelsHandler handles GET /api/models. It returns all enabled
// models for the current user (across their providers) — the pool an Agent may
// select from (M0-10). Provider name and protocol are included for convenience.
func ListEnabledModelsHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		models, err := repo.ListEnabledModels(db, uid)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list enabled models"})
			return
		}

		// Resolve provider name/protocol for each model.
		provMap := map[uint]model.Provider{}
		for i := range models {
			if _, seen := provMap[models[i].ProviderID]; !seen {
				if p, err := repo.GetProviderByID(db, models[i].ProviderID); err == nil {
					provMap[models[i].ProviderID] = *p
				}
			}
		}

		views := make([]gin.H, 0, len(models))
		for i := range models {
			p := provMap[models[i].ProviderID]
			views = append(views, gin.H{
				"id":            models[i].ID,
				"provider_id":   models[i].ProviderID,
				"provider_name": p.Name,
				"protocol":      string(p.Protocol),
				"model_id":      models[i].ModelID,
				"name":          models[i].Name,
				"is_default":    models[i].IsDefault,
				"enabled":       models[i].Enabled,
			})
		}
		c.JSON(http.StatusOK, gin.H{
			"models":        views,
			"total_enabled": len(views),
		})
	}
}

// UpdateModelHandler handles PUT /api/providers/:id/models/:mid. It toggles the
// enabled and/or is_default flags of a managed model. Marking a model default
// clears any other default in the same provider.
func UpdateModelHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		if _, ok := lookupOwnedProvider(c, db, uid); !ok {
			return
		}

		mid, err := parseUintParam(c, "mid")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid model id"})
			return
		}
		m, err := repo.GetModelByID(db, mid, uid)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "model not found"})
			return
		}

		var req updateModelRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		if err := repo.PatchModel(db, m, req.Enabled, req.IsDefault); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update model"})
			return
		}
		c.JSON(http.StatusOK, toModelView(m))
	}
}

// parseUintParam parses a path parameter as an unsigned integer.
func parseUintParam(c *gin.Context, key string) (uint, error) {
	s := c.Param(key)
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, err
	}
	return uint(v), nil
}
