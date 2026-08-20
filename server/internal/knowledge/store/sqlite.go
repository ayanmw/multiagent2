// Package store 提供基于 SQLite（glebarez 纯 Go 驱动，无需 gcc）的持久化向量存储
// （M5-02 Knowledge RAG 的向量库基线）。
//
// 设计：实现框架 knowledge/vectorstore.VectorStore 接口，使上层可直接复用
// knowledge.BuiltinKnowledge 的检索管线（embedder + retriever + reranker）。
// 向量以 JSON 文本落库，按 kb_id 逻辑隔离（一个知识库一张子空间），满足
// 「建知识库 → 索引 → 跨重启保留 → 检索」全流程。预留 PG/pgvector 升级位：
// 未来替换本实现为 pgvector 后端即可，上层 Manager/API 无需改动。
package store

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"

	"gorm.io/gorm"
)

// vectorRow 是 kb_vectors 表的 GORM 模型（切片向量落库行）。
type vectorRow struct {
	ID        string `gorm:"column:id;primaryKey"`
	KBID      string `gorm:"column:kb_id;index"`
	DocName   string `gorm:"column:doc_name"`  // 来源文档名（用于列出/删除文档）
	Name      string `gorm:"column:name"`      // 切片名
	Content   string `gorm:"column:content"`   // 切片文本
	Embedding string `gorm:"column:embedding"` // JSON []float64
	Metadata  string `gorm:"column:metadata"`  // JSON map
	CreatedAt time.Time
}

// TableName 固定向量表名。
func (vectorRow) TableName() string { return "kb_vectors" }

// SQLiteVectorStore 是基于 GORM + SQLite 的向量存储，按 kb_id 逻辑隔离。
type SQLiteVectorStore struct {
	db  *gorm.DB
	kbID string
}

// New 构造（或复用）某知识库的向量存储，并确保 kb_vectors 表存在。
// db 为共享 *gorm.DB（与业务库同一 SQLite 文件）；kbID 用于逻辑隔离。
func New(db *gorm.DB, kbID string) (*SQLiteVectorStore, error) {
	if db == nil {
		return nil, fmt.Errorf("store: nil db")
	}
	if kbID == "" {
		return nil, fmt.Errorf("store: empty kb id")
	}
	if err := db.AutoMigrate(&vectorRow{}); err != nil {
		return nil, fmt.Errorf("store: ensure kb_vectors table: %w", err)
	}
	return &SQLiteVectorStore{db: db, kbID: kbID}, nil
}

// Add 写入一个切片向量（幂等：同 id 覆盖）。
func (s *SQLiteVectorStore) Add(_ context.Context, doc *document.Document, embedding []float64) error {
	if doc == nil {
		return fmt.Errorf("document cannot be nil")
	}
	if doc.ID == "" {
		return fmt.Errorf("document ID cannot be empty")
	}
	if len(embedding) == 0 {
		return fmt.Errorf("embedding cannot be empty")
	}
	embJSON, err := json.Marshal(embedding)
	if err != nil {
		return fmt.Errorf("marshal embedding: %w", err)
	}
	metaJSON, err := json.Marshal(doc.Metadata)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}
	docName := docNameOf(doc)
	row := vectorRow{
		ID:        doc.ID,
		KBID:      s.kbID,
		DocName:   docName,
		Name:      doc.Name,
		Content:   doc.Content,
		Embedding: string(embJSON),
		Metadata:  string(metaJSON),
		CreatedAt: time.Now().UTC(),
	}
	// Upsert：同 id 覆盖（SQLite 经 GORM 先删后插，简单幂等）。
	if err := s.db.Where("id = ? AND kb_id = ?", row.ID, s.kbID).Delete(&vectorRow{}).Error; err != nil {
		return fmt.Errorf("store: delete old row: %w", err)
	}
	if err := s.db.Create(&row).Error; err != nil {
		return fmt.Errorf("store: insert row: %w", err)
	}
	return nil
}

// Get 按 id 取切片及其向量。
func (s *SQLiteVectorStore) Get(_ context.Context, id string) (*document.Document, []float64, error) {
	var row vectorRow
	if err := s.db.First(&row, "id = ? AND kb_id = ?", id, s.kbID).Error; err != nil {
		return nil, nil, fmt.Errorf("document not found: %s", id)
	}
	return row.toDocument(), row.toEmbedding(), nil
}

// Update 更新切片内容与向量（复用 Add 的幂等逻辑）。
func (s *SQLiteVectorStore) Update(ctx context.Context, doc *document.Document, embedding []float64) error {
	return s.Add(ctx, doc, embedding)
}

// Delete 按 id 删除切片。
func (s *SQLiteVectorStore) Delete(_ context.Context, id string) error {
	return s.db.Where("id = ? AND kb_id = ?", id, s.kbID).Delete(&vectorRow{}).Error
}

// Search 余弦相似检索（向量模式）。
func (s *SQLiteVectorStore) Search(_ context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	if query == nil {
		return nil, fmt.Errorf("query cannot be nil")
	}
	if len(query.Vector) == 0 {
		return nil, fmt.Errorf("query vector cannot be empty")
	}
	var rows []vectorRow
	if err := s.db.Where("kb_id = ?", s.kbID).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("store: load vectors: %w", err)
	}

	var results []*vectorstore.ScoredDocument
	for i := range rows {
		emb := rows[i].toEmbedding()
		if len(emb) != len(query.Vector) {
			continue
		}
		if query.Filter != nil && !matchFilter(&rows[i], query.Filter) {
			continue
		}
		score := cosineSimilarity(query.Vector, emb)
		if score >= query.MinScore {
			results = append(results, &vectorstore.ScoredDocument{
				Document: rows[i].toDocument(),
				Score:    score,
			})
		}
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	limit := query.Limit
	if limit <= 0 {
		limit = 10
	}
	if len(results) > limit {
		results = results[:limit]
	}
	return &vectorstore.SearchResult{Results: results}, nil
}

// DeleteByFilter 按条件删除：DeleteAll（清空本 kb）/ DocumentIDs / Metadata（Go 侧精确匹配）。
func (s *SQLiteVectorStore) DeleteByFilter(_ context.Context, opts ...vectorstore.DeleteOption) error {
	config := vectorstore.ApplyDeleteOptions(opts...)
	if config.DeleteAll {
		return s.db.Where("kb_id = ?", s.kbID).Delete(&vectorRow{}).Error
	}
	if len(config.DocumentIDs) > 0 {
		return s.db.Where("kb_id = ? AND id IN ?", s.kbID, config.DocumentIDs).Delete(&vectorRow{}).Error
	}
	if len(config.Filter) > 0 {
		// 元数据过滤：逐行解析 JSON 精确匹配后删除匹配 id。
		var rows []vectorRow
		if err := s.db.Where("kb_id = ?", s.kbID).Find(&rows).Error; err != nil {
			return err
		}
		var toDel []string
		for i := range rows {
			if matchMetaFilter(&rows[i], config.Filter) {
				toDel = append(toDel, rows[i].ID)
			}
		}
		if len(toDel) > 0 {
			return s.db.Where("kb_id = ? AND id IN ?", s.kbID, toDel).Delete(&vectorRow{}).Error
		}
		return nil
	}
	return fmt.Errorf("delete by filter: no filter conditions specified")
}

// UpdateByFilter 本基线不支持（返回 0, nil），检索管线不依赖它。
func (s *SQLiteVectorStore) UpdateByFilter(_ context.Context, _ ...vectorstore.UpdateByFilterOption) (int64, error) {
	return 0, nil
}

// Count 统计本 kb 切片数（可带 Metadata 过滤）。
func (s *SQLiteVectorStore) Count(_ context.Context, opts ...vectorstore.CountOption) (int, error) {
	config := vectorstore.ApplyCountOptions(opts...)
	if len(config.Filter) == 0 {
		var n int64
		if err := s.db.Model(&vectorRow{}).Where("kb_id = ?", s.kbID).Count(&n).Error; err != nil {
			return 0, err
		}
		return int(n), nil
	}
	var rows []vectorRow
	if err := s.db.Where("kb_id = ?", s.kbID).Find(&rows).Error; err != nil {
		return 0, err
	}
	n := 0
	for i := range rows {
		if matchMetaFilter(&rows[i], config.Filter) {
			n++
		}
	}
	return n, nil
}

// GetMetadata 返回本 kb 全部切片的元数据（供 ShowDocumentInfo 等使用）。
func (s *SQLiteVectorStore) GetMetadata(_ context.Context, opts ...vectorstore.GetMetadataOption) (map[string]vectorstore.DocumentMetadata, error) {
	config, err := vectorstore.ApplyGetMetadataOptions(opts...)
	if err != nil {
		return nil, err
	}
	var rows []vectorRow
	if err := s.db.Where("kb_id = ?", s.kbID).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]vectorstore.DocumentMetadata)
	for i := range rows {
		if len(config.IDs) > 0 && !contains(config.IDs, rows[i].ID) {
			continue
		}
		if len(config.Filter) > 0 && !matchMetaFilter(&rows[i], config.Filter) {
			continue
		}
		out[rows[i].ID] = vectorstore.DocumentMetadata{Metadata: rows[i].toMetadata()}
	}
	return out, nil
}

// Close 共享 DB，无需关闭。
func (s *SQLiteVectorStore) Close() error { return nil }

// VectorStore 是 knowledge.Manager 依赖的向量存储子集：框架 vectorstore.VectorStore
// 接口 + 文档聚合能力（ListDocuments/DeleteDocument）。SQLiteVectorStore 与
// PGVectorStore 均实现它，Manager 经 store 工厂按后端切换，业务层无感知（M8-04）。
type VectorStore interface {
	vectorstore.VectorStore
	ListDocuments(ctx context.Context) ([]DocInfo, error)
	DeleteDocument(ctx context.Context, docName string) (int64, error)
}

// 编译期断言：SQLiteVectorStore 满足 VectorStore 接口。
var _ VectorStore = (*SQLiteVectorStore)(nil)

// ListDocuments 返回本 kb 的文档（来源）列表与切片数（按 doc_name 聚合）。
func (s *SQLiteVectorStore) ListDocuments(context.Context) ([]DocInfo, error) {
	type agg struct {
		DocName    string
		ChunkCount int
	}
	var aggs []agg
	if err := s.db.Model(&vectorRow{}).
		Select("doc_name, COUNT(*) as chunk_count").
		Where("kb_id = ?", s.kbID).
		Group("doc_name").
		Scan(&aggs).Error; err != nil {
		return nil, err
	}
	var out []DocInfo
	for _, a := range aggs {
		out = append(out, DocInfo{Name: a.DocName, ChunkCount: a.ChunkCount})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// DeleteDocument 删除某来源文档的全部切片。
func (s *SQLiteVectorStore) DeleteDocument(_ context.Context, docName string) (int64, error) {
	res := s.db.Where("kb_id = ? AND doc_name = ?", s.kbID, docName).Delete(&vectorRow{})
	return res.RowsAffected, res.Error
}

// DocInfo 是文档（来源）聚合信息。
type DocInfo struct {
	Name       string `json:"name"`
	ChunkCount int    `json:"chunk_count"`
}

// --- 内部辅助 ---

func (r *vectorRow) toDocument() *document.Document {
	return &document.Document{
		ID:       r.ID,
		Name:     r.Name,
		Content:  r.Content,
		Metadata: r.toMetadata(),
	}
}

func (r *vectorRow) toEmbedding() []float64 {
	var v []float64
	if r.Embedding == "" {
		return v
	}
	_ = json.Unmarshal([]byte(r.Embedding), &v)
	return v
}

func (r *vectorRow) toMetadata() map[string]any {
	if r.Metadata == "" {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(r.Metadata), &m); err != nil {
		return map[string]any{}
	}
	return m
}

// docNameOf 取切片的来源文档名：优先 MetaSourceName，其次 Name，否则 "default"。
func docNameOf(doc *document.Document) string {
	if doc.Metadata != nil {
		if v, ok := doc.Metadata[source.MetaSourceName].(string); ok && v != "" {
			return v
		}
	}
	if doc.Name != "" {
		return doc.Name
	}
	return "default"
}

// matchFilter 按 vectorstore.SearchFilter（IDs / Metadata）过滤单行。
func matchFilter(r *vectorRow, f *vectorstore.SearchFilter) bool {
	if f == nil {
		return true
	}
	if len(f.IDs) > 0 && !contains(f.IDs, r.ID) {
		return false
	}
	if len(f.Metadata) > 0 && !matchMetaFilter(r, f.Metadata) {
		return false
	}
	return true
}

// matchMetaFilter 对单行的 metadata JSON 做精确键值匹配。
func matchMetaFilter(r *vectorRow, filter map[string]any) bool {
	meta := r.toMetadata()
	for k, v := range filter {
		mv, ok := meta[k]
		if !ok || !equivalent(mv, v) {
			return false
		}
	}
	return true
}

func equivalent(a, b any) bool {
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// cosineSimilarity 余弦相似度（已归一化向量时等价于点积）。
func cosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := 0; i < len(a); i++ {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
