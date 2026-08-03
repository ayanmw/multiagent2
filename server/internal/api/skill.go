package api

import (
	"net/http"
	"strconv"

	"github.com/ayanmw/multiagent2/server/internal/skillrepo"
	"github.com/gin-gonic/gin"
)

// skillView 是技能列表项视图。
type skillView struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Scope       string `json:"scope"`
	ReadOnly    bool   `json:"read_only"`
}

// skillDetailView 是技能完整内容视图（含 SKILL.md body）。
type skillDetailView struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Scope       string `json:"scope"`
	ReadOnly    bool   `json:"read_only"`
	Body        string `json:"body"`
}

func toSkillView(s skillrepo.Summary) skillView {
	return skillView{Name: s.Name, Description: s.Description, Scope: s.Scope, ReadOnly: s.ReadOnly}
}

func toSkillDetailView(d *skillrepo.Detail) skillDetailView {
	if d == nil {
		return skillDetailView{}
	}
	return skillDetailView{Name: d.Name, Description: d.Description, Scope: d.Scope, ReadOnly: d.ReadOnly, Body: d.Body}
}

// ListSkillsHandler 处理 GET /api/skills（需 skills:read）。
// 返回当前用户可见的全部技能（共享 + 私有），共享为只读，私有可经 API 改写。
func ListSkillsHandler(sharedRoot, dataDir string) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		uidStr := strconv.FormatUint(uint64(uid), 10)
		mgr := skillrepo.NewManager(sharedRoot, dataDir)
		list, err := mgr.List(uidStr)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list skills"})
			return
		}
		views := make([]skillView, 0, len(list))
		for i := range list {
			views = append(views, toSkillView(list[i]))
		}
		c.JSON(http.StatusOK, gin.H{"skills": views, "total": len(views)})
	}
}

// GetSkillHandler 处理 GET /api/skills/:name（需 skills:read）。
// 私有优先、其次共享；都不存在返回 404。
func GetSkillHandler(sharedRoot, dataDir string) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		uidStr := strconv.FormatUint(uint64(uid), 10)
		name := c.Param("name")
		if !skillrepo.ValidSkillName(name) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid skill name"})
			return
		}
		mgr := skillrepo.NewManager(sharedRoot, dataDir)
		d, err := mgr.Get(uidStr, name)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "skill not found"})
			return
		}
		c.JSON(http.StatusOK, toSkillDetailView(d))
	}
}

// skillCreateRequest 是创建技能的请求体。
type skillCreateRequest struct {
	Name string `json:"name" binding:"required"`
	Body string `json:"body"`
}

// CreateSkillHandler 处理 POST /api/skills（需 skills:write）。
// 写入当前用户的私有技能目录（dataDir/<uid>/<name>/SKILL.md），owner 隔离；
// 共享技能根不可经 API 创建/覆盖（只读）。
func CreateSkillHandler(sharedRoot, dataDir string) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		uidStr := strconv.FormatUint(uint64(uid), 10)
		var req skillCreateRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if !skillrepo.ValidSkillName(req.Name) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid skill name (only [A-Za-z0-9_-] allowed)"})
			return
		}
		mgr := skillrepo.NewManager(sharedRoot, dataDir)
		if err := mgr.Create(uidStr, req.Name, req.Body); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create skill"})
			return
		}
		d, _ := mgr.Get(uidStr, req.Name)
		c.JSON(http.StatusCreated, toSkillDetailView(d))
	}
}

// skillUpdateRequest 是更新技能的请求体（仅 body）。
type skillUpdateRequest struct {
	Body string `json:"body"`
}

// UpdateSkillHandler 处理 PUT /api/skills/:name（需 skills:write）。
// 仅更新用户私有技能；不存在返回 404。
func UpdateSkillHandler(sharedRoot, dataDir string) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		uidStr := strconv.FormatUint(uint64(uid), 10)
		name := c.Param("name")
		if !skillrepo.ValidSkillName(name) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid skill name"})
			return
		}
		var req skillUpdateRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		mgr := skillrepo.NewManager(sharedRoot, dataDir)
		if err := mgr.Update(uidStr, name, req.Body); err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "skill not found"})
			return
		}
		d, _ := mgr.Get(uidStr, name)
		c.JSON(http.StatusOK, toSkillDetailView(d))
	}
}

// DeleteSkillHandler 处理 DELETE /api/skills/:name（需 skills:write）。
// 仅删除用户私有技能；共享技能不可经 API 删除，不存在返回 404。
func DeleteSkillHandler(sharedRoot, dataDir string) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		uidStr := strconv.FormatUint(uint64(uid), 10)
		name := c.Param("name")
		if !skillrepo.ValidSkillName(name) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid skill name"})
			return
		}
		mgr := skillrepo.NewManager(sharedRoot, dataDir)
		if err := mgr.Delete(uidStr, name); err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "skill not found"})
			return
		}
		c.Status(http.StatusNoContent)
	}
}
