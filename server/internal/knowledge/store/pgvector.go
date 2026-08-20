// Package store 的 pgvector 后端（M8-04 Knowledge RAG 升级）：
// 把 M5-02 的本地 SQLite JSON 向量基线升级为 PostgreSQL + pgvector 扩展，
// 支撑万级 chunk 检索（HNSW 索引）与并发检索（连接池）。
//
// 设计：
//   - 驱动：jackc/pgx/v5 的 stdlib 模式（纯 Go，无 CGO），经 database/sql 使用；
//     全部参数走 `$n` 占位符 + database/sql 兼容类型（string/bool/int/float64）。
//   - 连接：PGPool 持有单个 *sql.DB（按 DSN 建一次池），ForKB(kbID) 返回轻量 store
//     实例——避免每个知识库都开新连接池。
//   - 表：单表 kb_vectors（id TEXT PK / kb_id / doc_name / name / content /
//     embedding vector(dim) / metadata jsonb / created_at），kb_id 逻辑隔离，
//     与 SQLite 基线同构（表名一致，仅库不同）。
//   - 索引：pgvector >= 0.5.0 建 HNSW（vector_cosine_ops）；失败降级 IVFFlat；
//     再失败仅警告（小数据量纯扫描可用）。
//   - 维度：建表固定 embedding vector(N)（N=KB_PG_DIM，默认 256，须与嵌入器一致）；
//     已存在表维度不一致时返回明确错误（提示重建），杜绝静默错配。
//   - 检索：余弦距离算子 `<=>`，`1 - distance` 即余弦相似度；metadata 过滤用
//     jsonb 包含操作符 `@>`（精确键值包含），IDs 过滤用 `id = ANY($n::text[])`。
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // 注册 pgx 驱动（database/sql 兼容）

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

// 默认参数（与 config 侧默认值保持一致；NewPGPool 内对零值回落）。
const (
	DefaultPGDim     = 256
	DefaultPGPoolSize = 10
)

// pg 建表 / 索引 SQL（embedding 维度在 NewPGPool 内动态拼装）。
const (
	pgCreateTableSQL = `CREATE TABLE IF NOT EXISTS kb_vectors (
		id         TEXT PRIMARY KEY,
		kb_id      TEXT NOT NULL,
		doc_name   TEXT NOT NULL DEFAULT '',
		name       TEXT NOT NULL DEFAULT '',
		content    TEXT NOT NULL DEFAULT '',
		embedding  vector(%d) NOT NULL,
		metadata   JSONB NOT NULL DEFAULT '{}',
		created_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`
	pgIndexKBSQL       = "CREATE INDEX IF NOT EXISTS idx_kb_vectors_kb ON kb_vectors (kb_id)"
	pgIndexHNSWSQL     = "CREATE INDEX IF NOT EXISTS idx_kb_vectors_embedding_hnsw ON kb_vectors USING hnsw (embedding vector_cosine_ops)"
	pgIndexIVFFlatSQL  = "CREATE INDEX IF NOT EXISTS idx_kb_vectors_embedding_ivf ON kb_vectors USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100)"
	pgCheckExtensionSQL = "SELECT extversion FROM pg_extension WHERE extname = 'vector'"
	pgColumnTypeSQL     = `SELECT format_type(atttypid, atttypmod) FROM pg_attribute
		WHERE attrelid = 'kb_vectors'::regclass AND attname = 'embedding' AND NOT attisdropped`
)

// PGConfig 是 pgvector 后端的连接与维度配置。
type PGConfig struct {
	DSN      string // PostgreSQL 连接串（如 postgres://user:pass@host:5432/db）
	Dim      int    // 向量维度（默认 256，须与嵌入器 GetDimensions 一致）
	PoolSize int    // 连接池大小（默认 10，并发检索支撑）
}

// PGPool 是 pgvector 后端的共享连接池。多个知识库 store 复用同一 *sql.DB。
type PGPool struct {
	db  *sql.DB
	dim int
}

// NewPGPool 连接 PostgreSQL 并幂等初始化 kb_vectors 表与索引。
// 无 pgvector 扩展 / 维度不匹配 / 连接失败均返回带指引的错误。
func NewPGPool(ctx context.Context, cfg PGConfig) (*PGPool, error) {
	if strings.TrimSpace(cfg.DSN) == "" {
		return nil, fmt.Errorf("store: pgvector DSN 为空")
	}
	dim := cfg.Dim
	if dim <= 0 {
		dim = DefaultPGDim
	}
	poolSize := cfg.PoolSize
	if poolSize <= 0 {
		poolSize = DefaultPGPoolSize
	}

	db, err := sql.Open("pgx", cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("store: 打开 pgx 连接失败: %w", err)
	}
	db.SetMaxOpenConns(poolSize)
	db.SetMaxIdleConns(poolSize)
	db.SetConnMaxLifetime(30 * time.Minute)

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: 连接 PostgreSQL 失败（DSN=%q）: %w", maskDSN(cfg.DSN), err)
	}

	p := &PGPool{db: db, dim: dim}
	if err := p.ensureSchema(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return p, nil
}

// Close 关闭连接池。
func (p *PGPool) Close() error {
	if p == nil || p.db == nil {
		return nil
	}
	return p.db.Close()
}

// Dim 返回池配置的向量维度。
func (p *PGPool) Dim() int { return p.dim }

// ForKB 返回某知识库（逻辑隔离）的向量存储视图。
func (p *PGPool) ForKB(kbID string) *PGVectorStore {
	return &PGVectorStore{pool: p, kbID: kbID}
}

// ensureSchema 幂等初始化：扩展检查 → 建表 → 维度校验 → kb 索引 → HNSW/IVFFlat。
func (p *PGPool) ensureSchema(ctx context.Context) error {
	// 1. pgvector 扩展必须已安装（CREATE EXTENSION vector）。
	var extVer string
	if err := p.db.QueryRowContext(ctx, pgCheckExtensionSQL).Scan(&extVer); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("store: 目标库未安装 pgvector 扩展——请先执行 CREATE EXTENSION vector（部署见 docs/knowledge-pgvector.md）")
		}
		return fmt.Errorf("store: 检查 pgvector 扩展失败: %w", err)
	}

	// 2. 建表（幂等；vector 类型依赖扩展，故在扩展检查之后）。
	createSQL := fmt.Sprintf(pgCreateTableSQL, p.dim)
	if _, err := p.db.ExecContext(ctx, createSQL); err != nil {
		return fmt.Errorf("store: 创建 kb_vectors 表失败: %w", err)
	}

	// 3. 维度一致性：表已存在时校验 embedding 列维度与配置一致。
	var colType string
	if err := p.db.QueryRowContext(ctx, pgColumnTypeSQL).Scan(&colType); err == nil {
		if dim := parseVectorDim(colType); dim > 0 && dim != p.dim {
			return fmt.Errorf("store: kb_vectors.embedding 已存在且维度=%d，与配置 KB_PG_DIM=%d 不一致——请 DROP TABLE kb_vectors 后重建，或调整 KB_PG_DIM", dim, p.dim)
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("store: 检查 embedding 列维度失败: %w", err)
	}

	// 4. kb_id 普通索引（按知识库过滤）。
	if _, err := p.db.ExecContext(ctx, pgIndexKBSQL); err != nil {
		log.Printf("[pgvector] 创建 kb_id 索引失败（可忽略，检索仍可用）: %v", err)
	}

	// 5. 相似度索引：HNSW（pgvector >= 0.5.0）→ IVFFlat → 无索引警告。
	_, herr := p.db.ExecContext(ctx, pgIndexHNSWSQL)
	if herr == nil {
		log.Printf("[pgvector] HNSW 索引就绪（vector_cosine_ops）")
		return nil
	}
	if _, ierr := p.db.ExecContext(ctx, pgIndexIVFFlatSQL); ierr == nil {
		log.Printf("[pgvector] HNSW 不可用（pgvector < 0.5.0？），已降级 IVFFlat（lists=100）")
		return nil
	}
	log.Printf("[pgvector] 警告：相似度索引创建失败（HNSW/IVFFlat 均不可用），检索将退化为全表扫描：%v", herr)
	return nil
}

// parseVectorDim 解析 format_type 输出（"vector(256)" → 256；无括号 → 0）。
func parseVectorDim(colType string) int {
	open := strings.IndexByte(colType, '(')
	if open < 0 {
		return 0
	}
	close := strings.IndexByte(colType[open:], ')')
	if close < 0 {
		return 0
	}
	n, err := strconv.Atoi(colType[open+1 : open+close])
	if err != nil {
		return 0
	}
	return n
}

// maskDSN 脱敏连接串中的密码（仅用于错误日志）。
func maskDSN(dsn string) string {
	at := strings.IndexByte(dsn, '@')
	if at < 0 {
		return dsn
	}
	scheme := dsn
	if i := strings.Index(dsn, "://"); i >= 0 {
		scheme = dsn[:i+3]
		rest := dsn[i+3:]
		at = strings.IndexByte(rest, '@')
		if at < 0 {
			return dsn
		}
		return scheme + "***@" + rest[at+1:]
	}
	return "***@" + dsn[at+1:]
}

// PGVectorStore 是某知识库的 pgvector 存储视图（共享 PGPool 连接池）。
// 实现框架 knowledge/vectorstore.VectorStore 接口 + ListDocuments/DeleteDocument。
type PGVectorStore struct {
	pool *PGPool
	kbID string
}

// 编译期断言：满足 vectorstore.VectorStore 接口。
var _ vectorstore.VectorStore = (*PGVectorStore)(nil)

// pgRow 是 pgvector 检索/读取行的载体（embedding 以文本 "[]" 形式承载）。
type pgRow struct {
	ID        string
	DocName   string
	Name      string
	Content   string
	Embedding string
	Metadata  string
	Score     float64
}

// Add 写入一个切片向量（幂等：同 id 覆盖）。
func (s *PGVectorStore) Add(ctx context.Context, doc *document.Document, embedding []float64) error {
	if doc == nil {
		return fmt.Errorf("document cannot be nil")
	}
	if doc.ID == "" {
		return fmt.Errorf("document ID cannot be empty")
	}
	if len(embedding) == 0 {
		return fmt.Errorf("embedding cannot be empty")
	}
	metaJSON, err := json.Marshal(doc.Metadata)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}
	_, err = s.pool.db.ExecContext(ctx, `INSERT INTO kb_vectors (id, kb_id, doc_name, name, content, embedding, metadata)
		VALUES ($1, $2, $3, $4, $5, $6::vector, $7::jsonb)
		ON CONFLICT (id) DO UPDATE SET
			kb_id = EXCLUDED.kb_id, doc_name = EXCLUDED.doc_name, name = EXCLUDED.name,
			content = EXCLUDED.content, embedding = EXCLUDED.embedding, metadata = EXCLUDED.metadata,
			created_at = now()`,
		doc.ID, s.kbID, docNameOf(doc), doc.Name, doc.Content, formatVector(embedding), string(metaJSON))
	if err != nil {
		return fmt.Errorf("store: insert row: %w", err)
	}
	return nil
}

// Get 按 id 取切片及其向量。
func (s *PGVectorStore) Get(ctx context.Context, id string) (*document.Document, []float64, error) {
	var r pgRow
	err := s.pool.db.QueryRowContext(ctx,
		`SELECT id, doc_name, name, content, embedding::text, metadata FROM kb_vectors WHERE kb_id = $1 AND id = $2`,
		s.kbID, id).Scan(&r.ID, &r.DocName, &r.Name, &r.Content, &r.Embedding, &r.Metadata)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, fmt.Errorf("document not found: %s", id)
		}
		return nil, nil, fmt.Errorf("store: get row: %w", err)
	}
	emb, err := parseVector(r.Embedding)
	if err != nil {
		return nil, nil, fmt.Errorf("store: parse embedding: %w", err)
	}
	return r.toDocument(), emb, nil
}

// Update 更新切片内容与向量（复用 Add 的幂等逻辑）。
func (s *PGVectorStore) Update(ctx context.Context, doc *document.Document, embedding []float64) error {
	return s.Add(ctx, doc, embedding)
}

// Delete 按 id 删除切片。
func (s *PGVectorStore) Delete(ctx context.Context, id string) error {
	_, err := s.pool.db.ExecContext(ctx, `DELETE FROM kb_vectors WHERE kb_id = $1 AND id = $2`, s.kbID, id)
	if err != nil {
		return fmt.Errorf("store: delete row: %w", err)
	}
	return nil
}

// Search 余弦相似检索：`embedding <=> $v` 余弦距离，`1 - distance` 即相似度。
// 支持 IDs / Metadata 过滤与 MinScore 下界，全部下推到 SQL（万级数据靠 HNSW 索引）。
func (s *PGVectorStore) Search(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	if query == nil {
		return nil, fmt.Errorf("query cannot be nil")
	}
	if len(query.Vector) == 0 {
		return nil, fmt.Errorf("query vector cannot be empty")
	}

	conds := []string{"kb_id = $1"}
	args := []any{s.kbID}
	if query.Filter != nil {
		if len(query.Filter.IDs) > 0 {
			args = append(args, "{"+strings.Join(query.Filter.IDs, ",")+"}")
			conds = append(conds, fmt.Sprintf("id = ANY($%d::text[])", len(args)))
		}
		if len(query.Filter.Metadata) > 0 {
			mj, err := json.Marshal(query.Filter.Metadata)
			if err != nil {
				return nil, fmt.Errorf("marshal metadata filter: %w", err)
			}
			args = append(args, string(mj))
			conds = append(conds, fmt.Sprintf("metadata @> $%d::jsonb", len(args)))
		}
	}

	vecIdx := len(args) + 1
	args = append(args, formatVector(query.Vector))
	if query.MinScore > 0 {
		args = append(args, query.MinScore)
		conds = append(conds, fmt.Sprintf("1 - (embedding <=> $%d::vector) >= $%d", vecIdx, len(args)))
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 10
	}
	args = append(args, limit)

	sqlStr := fmt.Sprintf(`SELECT id, doc_name, name, content, metadata, 1 - (embedding <=> $%d::vector) AS score
		FROM kb_vectors WHERE %s
		ORDER BY embedding <=> $%d::vector
		LIMIT $%d`,
		vecIdx, strings.Join(conds, " AND "), vecIdx, len(args))

	rows, err := s.pool.db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("store: search: %w", err)
	}
	defer rows.Close()

	var results []*vectorstore.ScoredDocument
	for rows.Next() {
		var r pgRow
		if err := rows.Scan(&r.ID, &r.DocName, &r.Name, &r.Content, &r.Metadata, &r.Score); err != nil {
			return nil, fmt.Errorf("store: scan search row: %w", err)
		}
		results = append(results, &vectorstore.ScoredDocument{
			Document: r.toDocument(),
			Score:    r.Score,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate search rows: %w", err)
	}
	return &vectorstore.SearchResult{Results: results}, nil
}

// DeleteByFilter 按条件删除：DeleteAll（清空本 kb）/ DocumentIDs / Metadata。
func (s *PGVectorStore) DeleteByFilter(ctx context.Context, opts ...vectorstore.DeleteOption) error {
	config := vectorstore.ApplyDeleteOptions(opts...)
	if config.DeleteAll {
		_, err := s.pool.db.ExecContext(ctx, `DELETE FROM kb_vectors WHERE kb_id = $1`, s.kbID)
		if err != nil {
			return fmt.Errorf("store: delete all: %w", err)
		}
		return nil
	}
	if len(config.DocumentIDs) > 0 {
		_, err := s.pool.db.ExecContext(ctx, `DELETE FROM kb_vectors WHERE kb_id = $1 AND id = ANY($2::text[])`,
			s.kbID, "{"+strings.Join(config.DocumentIDs, ",")+"}")
		if err != nil {
			return fmt.Errorf("store: delete by ids: %w", err)
		}
		return nil
	}
	if len(config.Filter) > 0 {
		mj, err := json.Marshal(config.Filter)
		if err != nil {
			return fmt.Errorf("marshal metadata filter: %w", err)
		}
		_, err = s.pool.db.ExecContext(ctx, `DELETE FROM kb_vectors WHERE kb_id = $1 AND metadata @> $2::jsonb`,
			s.kbID, string(mj))
		if err != nil {
			return fmt.Errorf("store: delete by metadata: %w", err)
		}
		return nil
	}
	return fmt.Errorf("delete by filter: no filter conditions specified")
}

// UpdateByFilter 与 SQLite 基线一致：本后端不支持（返回 0, nil），检索管线不依赖它。
func (s *PGVectorStore) UpdateByFilter(_ context.Context, _ ...vectorstore.UpdateByFilterOption) (int64, error) {
	return 0, nil
}

// Count 统计本 kb 切片数（可带 Metadata 过滤）。
func (s *PGVectorStore) Count(ctx context.Context, opts ...vectorstore.CountOption) (int, error) {
	config := vectorstore.ApplyCountOptions(opts...)
	if len(config.Filter) == 0 {
		var n int64
		if err := s.pool.db.QueryRowContext(ctx, `SELECT count(*) FROM kb_vectors WHERE kb_id = $1`, s.kbID).Scan(&n); err != nil {
			return 0, fmt.Errorf("store: count: %w", err)
		}
		return int(n), nil
	}
	mj, err := json.Marshal(config.Filter)
	if err != nil {
		return 0, fmt.Errorf("marshal metadata filter: %w", err)
	}
	var n int64
	if err := s.pool.db.QueryRowContext(ctx,
		`SELECT count(*) FROM kb_vectors WHERE kb_id = $1 AND metadata @> $2::jsonb`, s.kbID, string(mj)).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: count with filter: %w", err)
	}
	return int(n), nil
}

// GetMetadata 返回本 kb 全部（或按 IDs 过滤）切片的元数据。
func (s *PGVectorStore) GetMetadata(ctx context.Context, opts ...vectorstore.GetMetadataOption) (map[string]vectorstore.DocumentMetadata, error) {
	config, err := vectorstore.ApplyGetMetadataOptions(opts...)
	if err != nil {
		return nil, err
	}
	sqlStr := `SELECT id, metadata FROM kb_vectors WHERE kb_id = $1`
	args := []any{s.kbID}
	if len(config.IDs) > 0 {
		args = append(args, "{"+strings.Join(config.IDs, ",")+"}")
		sqlStr += fmt.Sprintf(" AND id = ANY($%d::text[])", len(args))
	}
	rows, err := s.pool.db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("store: get metadata: %w", err)
	}
	defer rows.Close()
	out := make(map[string]vectorstore.DocumentMetadata)
	for rows.Next() {
		var id, meta string
		if err := rows.Scan(&id, &meta); err != nil {
			return nil, fmt.Errorf("store: scan metadata row: %w", err)
		}
		out[id] = vectorstore.DocumentMetadata{Metadata: toMetaMap(meta)}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate metadata rows: %w", err)
	}
	return out, nil
}

// Close 共享连接池，store 视图不关闭（由 PGPool.Close 统一回收）。
func (s *PGVectorStore) Close() error { return nil }

// ListDocuments 返回本 kb 的文档（来源）列表与切片数（按 doc_name 聚合）。
func (s *PGVectorStore) ListDocuments(ctx context.Context) ([]DocInfo, error) {
	rows, err := s.pool.db.QueryContext(ctx,
		`SELECT doc_name, count(*) FROM kb_vectors WHERE kb_id = $1 GROUP BY doc_name`, s.kbID)
	if err != nil {
		return nil, fmt.Errorf("store: list documents: %w", err)
	}
	defer rows.Close()
	var out []DocInfo
	for rows.Next() {
		var d DocInfo
		if err := rows.Scan(&d.Name, &d.ChunkCount); err != nil {
			return nil, fmt.Errorf("store: scan doc row: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate doc rows: %w", err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// DeleteDocument 删除某来源文档的全部切片。
func (s *PGVectorStore) DeleteDocument(ctx context.Context, docName string) (int64, error) {
	res, err := s.pool.db.ExecContext(ctx, `DELETE FROM kb_vectors WHERE kb_id = $1 AND doc_name = $2`, s.kbID, docName)
	if err != nil {
		return 0, fmt.Errorf("store: delete document: %w", err)
	}
	return res.RowsAffected()
}

// --- 内部辅助 ---

func (r *pgRow) toDocument() *document.Document {
	return &document.Document{
		ID:       r.ID,
		Name:     r.Name,
		Content:  r.Content,
		Metadata: toMetaMap(r.Metadata),
	}
}

// toMetaMap 解析 metadata JSON 文本为 map（空/非法回落空 map）。
func toMetaMap(s string) map[string]any {
	if s == "" {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return map[string]any{}
	}
	return m
}

// formatVector 把 []float64 序列化为 pgvector 文本字面量 "[0.1,0.2,...]"。
func formatVector(v []float64) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, x := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(x, 'f', -1, 64))
	}
	b.WriteByte(']')
	return b.String()
}

// parseVector 解析 pgvector 文本字面量 "[0.1,0.2,...]" 为 []float64。
func parseVector(s string) ([]float64, error) {
	var v []float64
	if strings.TrimSpace(s) == "" {
		return nil, fmt.Errorf("empty vector text")
	}
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return nil, fmt.Errorf("invalid vector text %q: %w", s, err)
	}
	return v, nil
}

// docNameOf 取切片的来源文档名：优先 MetaSourceName，其次 Name，否则 "default"。
// （与 sqlite.go 同语义，导出给其他后端复用；此处为包内复用已定义在 sqlite.go 的版本）
