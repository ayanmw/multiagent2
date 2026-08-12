package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/ayanmw/multiagent2/server/internal/knowledge"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// knowledgeBaseView 是知识库列表/详情视图（不回显嵌入模型等内部字段细节）。
type knowledgeBaseView struct {
	ID             uint   `json:"id"`
	UserID         uint   `json:"user_id"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	EmbeddingModel string `json:"embedding_model"`
	DocCount       int    `json:"doc_count"`
	ChunkCount     int    `json:"chunk_count"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

func toKnowledgeBaseView(kb *model.KnowledgeBase) knowledgeBaseView {
	return knowledgeBaseView{
		ID:             kb.ID,
		UserID:         kb.UserID,
		Name:           kb.Name,
		Description:    kb.Description,
		EmbeddingModel: kb.EmbeddingModel,
		DocCount:       kb.DocCount,
		ChunkCount:     kb.ChunkCount,
		CreatedAt:      kb.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:      kb.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

// docInfoView 是知识库文档（来源）视图。
type docInfoView struct {
	Name       string `json:"name"`
	ChunkCount int    `json:"chunk_count"`
}

func toDocInfoView(d knowledge.DocInfo) docInfoView {
	return docInfoView{Name: d.Name, ChunkCount: d.ChunkCount}
}

// searchHitView 是检索命中视图。
type searchHitView struct {
	Content    string  `json:"content"`
	Score      float64 `json:"score"`
	Source     string  `json:"source"`
	ChunkIndex int     `json:"chunk_index"`
}

func toSearchHitView(h knowledge.SearchHit) searchHitView {
	return searchHitView{Content: h.Content, Score: h.Score, Source: h.Source, ChunkIndex: h.ChunkIndex}
}

// CreateKnowledgeBaseHandler 处理 POST /api/knowledge（需 knowledge:write）。
// 新建一个归属于当前用户的知识库。
func CreateKnowledgeBaseHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		var body struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		kb := &model.KnowledgeBase{UserID: uid, Name: body.Name, Description: body.Description}
		if err := repo.CreateKnowledgeBase(db, kb); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, toKnowledgeBaseView(kb))
	}
}

// ListKnowledgeBasesHandler 处理 GET /api/knowledge（需 knowledge:read）。
// 列出当前用户的全部知识库（按最近更新倒序）。
func ListKnowledgeBasesHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		list, err := repo.ListKnowledgeBases(db, uid)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list knowledge bases"})
			return
		}
		views := make([]knowledgeBaseView, 0, len(list))
		for i := range list {
			views = append(views, toKnowledgeBaseView(&list[i]))
		}
		c.JSON(http.StatusOK, gin.H{"knowledge_bases": views, "total": len(views)})
	}
}

// GetKnowledgeBaseHandler 处理 GET /api/knowledge/:id（需 knowledge:read，owner 隔离）。
func GetKnowledgeBaseHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		id, err := parseKnowledgeID(c)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		kb, err := repo.GetKnowledgeBase(db, id, uid)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get knowledge base"})
			return
		}
		if kb == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "knowledge base not found"})
			return
		}
		c.JSON(http.StatusOK, toKnowledgeBaseView(kb))
	}
}

// UpdateKnowledgeBaseHandler 处理 PUT /api/knowledge/:id（需 knowledge:write，owner 隔离）。
func UpdateKnowledgeBaseHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		id, err := parseKnowledgeID(c)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		var body struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		kb, err := repo.UpdateKnowledgeBase(db, id, uid, body.Name, body.Description)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if kb == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "knowledge base not found"})
			return
		}
		c.JSON(http.StatusOK, toKnowledgeBaseView(kb))
	}
}

// DeleteKnowledgeBaseHandler 处理 DELETE /api/knowledge/:id（需 knowledge:write，owner 隔离）。
// 删除知识库元数据及其全部向量（向量库按 kb_id 逻辑隔离，整库清空）。
func DeleteKnowledgeBaseHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		id, err := parseKnowledgeID(c)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		// 先确认归属，再删元数据 + 向量。
		kb, err := repo.GetKnowledgeBase(db, id, uid)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get knowledge base"})
			return
		}
		if kb == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "knowledge base not found"})
			return
		}
		mgr := knowledge.NewManager(db)
		if derr := mgr.DeleteKnowledge(context.Background(), id); derr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete knowledge vectors"})
			return
		}
		if derr := repo.DeleteKnowledgeBase(db, id, uid); derr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete knowledge base"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"deleted": true, "id": id})
	}
}

// IndexDocumentHandler 处理 POST /api/knowledge/:id/documents（需 knowledge:write，owner 隔离）。
// 切片并索引一份文本文档到该知识库，返回切片数。content_type 为 "text"/"code"。
func IndexDocumentHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		id, err := parseKnowledgeID(c)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		kb, err := repo.GetKnowledgeBase(db, id, uid)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get knowledge base"})
			return
		}
		if kb == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "knowledge base not found"})
			return
		}
		var body struct {
			Name         string `json:"name"`
			Content      string `json:"content"`
			ContentType  string `json:"content_type"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		if body.Content == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "content is required"})
			return
		}
		mgr := knowledge.NewManager(db)
		n, err := mgr.IndexDocument(context.Background(), id, body.Name, body.Content, body.ContentType)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"indexed_chunks": n})
	}
}

// ListKnowledgeDocumentsHandler 处理 GET /api/knowledge/:id/documents（需 knowledge:read，owner 隔离）。
func ListKnowledgeDocumentsHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		id, err := parseKnowledgeID(c)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		kb, err := repo.GetKnowledgeBase(db, id, uid)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get knowledge base"})
			return
		}
		if kb == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "knowledge base not found"})
			return
		}
		mgr := knowledge.NewManager(db)
		docs, err := mgr.ListDocuments(context.Background(), id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list documents"})
			return
		}
		views := make([]docInfoView, 0, len(docs))
		for i := range docs {
			views = append(views, toDocInfoView(docs[i]))
		}
		c.JSON(http.StatusOK, gin.H{"documents": views, "total": len(views)})
	}
}

// DeleteKnowledgeDocumentHandler 处理 DELETE /api/knowledge/:id/documents/:name
// （需 knowledge:write，owner 隔离）。删除某来源文档的全部切片。
func DeleteKnowledgeDocumentHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		id, err := parseKnowledgeID(c)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		kb, err := repo.GetKnowledgeBase(db, id, uid)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get knowledge base"})
			return
		}
		if kb == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "knowledge base not found"})
			return
		}
		docName := c.Param("name")
		if docName == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "document name required"})
			return
		}
		mgr := knowledge.NewManager(db)
		n, err := mgr.DeleteDocument(context.Background(), id, docName)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete document"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"deleted_chunks": n, "name": docName})
	}
}

// SearchKnowledgeHandler 处理 POST /api/knowledge/:id/search（需 knowledge:read，owner 隔离）。
// 在该知识库内检索 query，返回 top_k 命中（按相似度降序）。
func SearchKnowledgeHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		id, err := parseKnowledgeID(c)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		kb, err := repo.GetKnowledgeBase(db, id, uid)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get knowledge base"})
			return
		}
		if kb == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "knowledge base not found"})
			return
		}
		var body struct {
			Query string `json:"query"`
			TopK  int    `json:"top_k"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		if body.Query == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "query is required"})
			return
		}
		mgr := knowledge.NewManager(db)
		hits, err := mgr.Search(context.Background(), id, body.Query, body.TopK)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "search failed"})
			return
		}
		views := make([]searchHitView, 0, len(hits))
		for i := range hits {
			views = append(views, toSearchHitView(hits[i]))
		}
		c.JSON(http.StatusOK, gin.H{"hits": views, "total": len(views)})
	}
}

// parseKnowledgeID 从路由参数解析知识库 id（uint）。
func parseKnowledgeID(c *gin.Context) (uint, error) {
	s := c.Param("id")
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, err
	}
	return uint(v), nil
}
