// Package knowledge 提供知识库的端到端管理（M5-02 Knowledge RAG）：
// 切片索引（源加载 → 切片 → 向量化 → 持久化）、检索、以及对话时上下文注入。
//
// 复用框架 knowledge 包管线（BuiltinKnowledge + 自研 SQLiteVectorStore +
// LocalEmbedder），向量以本地 SQLite 持久化（纯 Go 驱动，无需 gcc），预留
// PG/pgvector 升级位。对话检索经 engine.KnowledgeRetriever 接口注入用户消息，
// 长度受控（maxChars），避免污染上下文窗口。
package knowledge

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"

	"github.com/ayanmw/multiagent2/server/internal/knowledge/embed"
	internalsource "github.com/ayanmw/multiagent2/server/internal/knowledge/source"
	"github.com/ayanmw/multiagent2/server/internal/knowledge/store"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"

	"gorm.io/gorm"
)

// chunkTarget / chunkOverlap 是切片的目标长度与重叠长度（字符数）。
const (
	chunkTarget  = 800
	chunkOverlap = 120
)

// Manager 管理用户知识库的索引与检索。
type Manager struct {
	db       *gorm.DB
	embedder *embed.LocalEmbedder
}

// NewManager 构造知识库管理器（内部使用本地离线嵌入器）。
func NewManager(db *gorm.DB) *Manager {
	return &Manager{db: db, embedder: embed.NewLocalEmbedder(embed.DefaultLocalDim)}
}

// SearchHit 是检索命中的单条切片。
type SearchHit struct {
	Content    string  `json:"content"`
	Score      float64 `json:"score"`
	Source     string  `json:"source"`
	ChunkIndex int     `json:"chunk_index"`
}

// DocInfo 是知识库文档（来源）的聚合信息（名称 + 切片数）。
type DocInfo struct {
	Name       string `json:"name"`
	ChunkCount int    `json:"chunk_count"`
}

// IndexDocument 切片并索引一份文档到指定知识库，返回切片数。
// name 为文档（来源）名；content 为文本内容；contentType 为 "text"/"code"（控制切片策略）。
func (m *Manager) IndexDocument(ctx context.Context, kbID uint, name, content, contentType string) (int, error) {
	if strings.TrimSpace(content) == "" {
		return 0, fmt.Errorf("文档内容为空")
	}
	if name == "" {
		name = "document"
	}
	chunks := chunkText(content, isCode(contentType))
	if len(chunks) == 0 {
		return 0, fmt.Errorf("文档切片为空")
	}

	kbIDStr := strconv.FormatUint(uint64(kbID), 10)
	vs, err := store.New(m.db, kbIDStr)
	if err != nil {
		return 0, err
	}

	docs := make([]*document.Document, 0, len(chunks))
	for i, ch := range chunks {
		docs = append(docs, &document.Document{
			ID:      docID(kbIDStr, name, i, ch),
			Name:    fmt.Sprintf("%s [chunk %d]", name, i),
			Content: ch,
			Metadata: map[string]any{
				source.MetaSourceName: name,
				source.MetaFileName:   name,
				source.MetaChunkIndex: i,
				source.MetaURI:        fmt.Sprintf("kb://%s/%s/%d", kbIDStr, name, i),
			},
		})
	}

	kb := knowledge.New(
		knowledge.WithVectorStore(vs),
		knowledge.WithEmbedder(m.embedder),
	)
	src := internalsource.New(name, docs)
	if err := kb.AddSource(ctx, src); err != nil {
		return 0, fmt.Errorf("索引文档失败: %w", err)
	}

	// 更新知识库统计（文档数 = 来源数，切片数 = chunk 数）。
	if err := m.refreshCounts(kbID); err != nil {
		return 0, err
	}
	return len(chunks), nil
}

// Search 在指定知识库内检索 query，返回 topK 命中（按相似度降序）。
func (m *Manager) Search(ctx context.Context, kbID uint, query string, topK int) ([]SearchHit, error) {
	if topK <= 0 {
		topK = 5
	}
	kbIDStr := strconv.FormatUint(uint64(kbID), 10)
	vs, err := store.New(m.db, kbIDStr)
	if err != nil {
		return nil, err
	}
	kb := knowledge.New(
		knowledge.WithVectorStore(vs),
		knowledge.WithEmbedder(m.embedder),
	)
	res, err := kb.Search(ctx, &knowledge.SearchRequest{
		Query:     query,
		MaxResults: topK,
		MinScore:  0.0,
	})
	if err != nil {
		// 无相关文档时框架返回 error；视为空结果而非失败。
		return nil, nil
	}
	hits := make([]SearchHit, 0, len(res.Documents))
	for _, d := range res.Documents {
		src := ""
		idx := 0
		if d.Document != nil && d.Document.Metadata != nil {
			if v, ok := d.Document.Metadata[source.MetaSourceName].(string); ok {
				src = v
			}
			if v, ok := d.Document.Metadata[source.MetaChunkIndex].(int); ok {
				idx = v
			}
		}
		content := ""
		if d.Document != nil {
			content = d.Document.Content
		}
		hits = append(hits, SearchHit{
			Content:    content,
			Score:      d.Score,
			Source:     src,
			ChunkIndex: idx,
		})
	}
	return hits, nil
}

// RetrieveContext 检索某用户的全部知识库，拼接相关切片为注入文本（控长）。
// 返回空字符串表示无相关内容（调用方据此不注入）。满足 engine.KnowledgeRetriever 语义。
func (m *Manager) RetrieveContext(ctx context.Context, userIDStr, query string, maxChars int) (string, error) {
	if maxChars <= 0 {
		maxChars = 4000
	}
	uid, err := strconv.ParseUint(userIDStr, 10, 64)
	if err != nil {
		return "", fmt.Errorf("非法用户 id: %w", err)
	}
	kbs, err := repo.ListKnowledgeBases(m.db, uint(uid))
	if err != nil {
		return "", err
	}
	if len(kbs) == 0 {
		return "", nil
	}

	var sections []string
	total := 0
	seen := map[string]bool{}
	for i := range kbs {
		hits, herr := m.Search(ctx, kbs[i].ID, query, 3)
		if herr != nil || len(hits) == 0 {
			continue
		}
		var buf strings.Builder
		for _, h := range hits {
			if h.Content == "" || seen[h.Content] {
				continue
			}
			seen[h.Content] = true
			block := fmt.Sprintf("[知识库:%s | 来源:%s]\n%s", kbs[i].Name, h.Source, h.Content)
			if total+len(block) > maxChars {
				break
			}
			buf.WriteString(block)
			buf.WriteString("\n\n")
			total += len(block) + 2
		}
		if buf.Len() > 0 {
			sections = append(sections, buf.String())
		}
	}
	if len(sections) == 0 {
		return "", nil
	}
	header := "以下是与用户问题相关的知识库参考内容，请在回答时优先参考：\n\n"
	return header + strings.Join(sections, "---\n\n"), nil
}

// ListDocuments 列出某知识库的文档（来源）与切片数。
func (m *Manager) ListDocuments(_ context.Context, kbID uint) ([]DocInfo, error) {
	vs, err := store.New(m.db, strconv.FormatUint(uint64(kbID), 10))
	if err != nil {
		return nil, err
	}
	raw, err := vs.ListDocuments()
	if err != nil {
		return nil, err
	}
	out := make([]DocInfo, 0, len(raw))
	for i := range raw {
		out = append(out, DocInfo{Name: raw[i].Name, ChunkCount: raw[i].ChunkCount})
	}
	return out, nil
}

// DeleteDocument 删除某知识库内指定来源的文档（全部切片）。
func (m *Manager) DeleteDocument(_ context.Context, kbID uint, docName string) (int64, error) {
	vs, err := store.New(m.db, strconv.FormatUint(uint64(kbID), 10))
	if err != nil {
		return 0, err
	}
	n, err := vs.DeleteDocument(docName)
	if err != nil {
		return 0, err
	}
	_ = m.refreshCounts(kbID)
	return n, nil
}

// DeleteKnowledge 删除某知识库的全部向量（KB 元数据由调用方在 repo 层删除）。
func (m *Manager) DeleteKnowledge(ctx context.Context, kbID uint) error {
	vs, err := store.New(m.db, strconv.FormatUint(uint64(kbID), 10))
	if err != nil {
		return err
	}
	return vs.DeleteByFilter(ctx, vectorstoreDeleteAll())
}

// vectorstoreDeleteAll 返回「清空本 kb 全部向量」的删除选项（框架 DeleteOption）。
func vectorstoreDeleteAll() vectorstore.DeleteOption {
	return vectorstore.WithDeleteAll(true)
}

// refreshCounts 依据向量库实况回写知识库的文档数/切片数。
func (m *Manager) refreshCounts(kbID uint) error {
	vs, err := store.New(m.db, strconv.FormatUint(uint64(kbID), 10))
	if err != nil {
		return err
	}
	docs, err := vs.ListDocuments()
	if err != nil {
		return err
	}
	chunks, err := vs.Count(context.Background())
	if err != nil {
		return err
	}
	return m.db.Model(&model.KnowledgeBase{}).
		Where("id = ?", kbID).
		Updates(map[string]any{
			"doc_count":   len(docs),
			"chunk_count": chunks,
			"updated_at":  time.Now().UTC(),
		}).Error
}

// --- 内部辅助 ---

// docID 生成确定性的切片 id（内容变更即视为新切片）。
func docID(kbID, name string, idx int, content string) string {
	h := sha256.New()
	h.Write([]byte(fmt.Sprintf("%s|%s|%d|%s", kbID, name, idx, content)))
	return fmt.Sprintf("%x", h.Sum(nil))
}

// isCode 判断切片策略：contentType 含 "code" 或常见代码扩展名 → 按行切片。
func isCode(contentType string) bool {
	ct := strings.ToLower(contentType)
	if strings.Contains(ct, "code") {
		return true
	}
	for _, ext := range []string{".go", ".py", ".js", ".ts", ".java", ".c", ".cpp", ".rs", ".sh"} {
		if strings.HasSuffix(ct, ext) {
			return true
		}
	}
	return false
}

// chunkText 把文本切成不超过 chunkTarget 的片段，片段间保留 chunkOverlap 重叠。
// byLines=true 时按行累积（适合代码），否则按段落/自然断点累积（适合 prose）。
func chunkText(text string, byLines bool) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	var raw []string
	if byLines {
		raw = strings.Split(text, "\n")
	} else {
		// 按空行分段，段内整体作为累积单元。
		raw = strings.Split(text, "\n")
	}

	var chunks []string
	var cur strings.Builder
	flush := func() {
		s := strings.TrimSpace(cur.String())
		if s != "" {
			chunks = append(chunks, s)
		}
		cur.Reset()
	}
	for _, seg := range raw {
		// 单段/单行超长：硬切到目标长度。
		if len([]rune(seg)) > chunkTarget {
			if cur.Len() > 0 {
				flush()
			}
			for _, piece := range hardSplit(seg, chunkTarget) {
				chunks = append(chunks, piece)
			}
			continue
		}
		proposed := cur.String()
		if proposed != "" {
			proposed += "\n"
		}
		proposed += seg
		if len([]rune(proposed)) > chunkTarget {
			flush()
			cur.WriteString(seg)
			// 重叠：把上一段尾部保留到新片段开头。
			if chunkOverlap > 0 {
				r := []rune(seg)
				if len(r) > chunkOverlap {
					cur.Reset()
					cur.WriteString(string(r[len(r)-chunkOverlap:]))
				}
			}
		} else {
			cur.WriteString(proposed)
		}
	}
	flush()
	return chunks
}

// hardSplit 把超长文本按 rune 长度硬切成若干片段（用于单行/段超长）。
func hardSplit(s string, size int) []string {
	r := []rune(s)
	var out []string
	for i := 0; i < len(r); i += size {
		end := i + size
		if end > len(r) {
			end = len(r)
		}
		out = append(out, string(r[i:end]))
	}
	return out
}
