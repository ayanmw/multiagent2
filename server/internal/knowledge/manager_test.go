package knowledge

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// newTestDB 打开一个临时文件的 SQLite（glebarez 纯 Go 驱动，无需 gcc），并迁移 KnowledgeBase 表。
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kb_test.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(&model.KnowledgeBase{}); err != nil {
		t.Fatalf("migrate knowledge_bases: %v", err)
	}
	// Windows 下 gorm 连接池会持有 SQLite 文件句柄，测试结束后关闭以释放文件，
	// 否则 t.TempDir 的清理会报「文件被占用」导致用例 FAIL（与断言无关）。
	t.Cleanup(func() {
		if sqlDB, cerr := db.DB(); cerr == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

// seedKB 插入一条测试知识库并返回其 id。
func seedKB(t *testing.T, db *gorm.DB, userID uint) uint {
	t.Helper()
	kb := &model.KnowledgeBase{
		UserID:      userID,
		Name:        "测试知识库",
		Description: "用于 M5-02 单测",
	}
	if err := repo.CreateKnowledgeBase(db, kb); err != nil {
		t.Fatalf("seed knowledge base: %v", err)
	}
	return kb.ID
}

func TestManager_IndexAndSearch(t *testing.T) {
	db := newTestDB(t)
	m := NewManager(db)
	kbID := seedKB(t, db, 1)

	const doc = "Go 语言使用 goroutine 实现并发，channel 用于 goroutine 间通信。\n" +
		"Java 使用线程与锁实现并发，synchronized 关键字提供互斥。"

	n, err := m.IndexDocument(context.Background(), kbID, "concurrency.md", doc, "text")
	if err != nil {
		t.Fatalf("IndexDocument: %v", err)
	}
	if n == 0 {
		t.Fatalf("expected >0 chunks, got %d", n)
	}

	// 检索命中：query 含 Go/goroutine 关键词，应召回相关切片。
	hits, err := m.Search(context.Background(), kbID, "Go goroutine 并发", 3)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatalf("expected at least one hit for 'Go goroutine 并发'")
	}
	foundGo := false
	for _, h := range hits {
		if h.Source == "concurrency.md" && h.Content != "" {
			foundGo = true
		}
	}
	if !foundGo {
		t.Fatalf("search result missing expected source/chunk: %+v", hits)
	}

	// 文档列表应包含该来源，切片数 = n。
	docs, err := m.ListDocuments(context.Background(), kbID)
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(docs) != 1 || docs[0].Name != "concurrency.md" || docs[0].ChunkCount != n {
		t.Fatalf("ListDocuments mismatch: %+v (want chunk_count=%d)", docs, n)
	}

	// refreshCounts 后知识库统计应与实况一致。
	var kb model.KnowledgeBase
	if err := db.First(&kb, kbID).Error; err != nil {
		t.Fatalf("reload kb: %v", err)
	}
	if kb.DocCount != 1 || kb.ChunkCount != n {
		t.Fatalf("kb counts mismatch: doc=%d chunk=%d (want 1/%d)", kb.DocCount, kb.ChunkCount, n)
	}
}

func TestManager_RetrieveContext(t *testing.T) {
	db := newTestDB(t)
	m := NewManager(db)
	kbID := seedKB(t, db, 7)

	// 两份文档：一份讲 Go，一份讲 Python，便于验证检索定向。
	if _, err := m.IndexDocument(context.Background(), kbID, "go.md",
		"Go 的标准库提供 net/http 用于编写 HTTP 服务。", "text"); err != nil {
		t.Fatalf("index go.md: %v", err)
	}
	if _, err := m.IndexDocument(context.Background(), kbID, "py.md",
		"Python 使用 requests 库发送 HTTP 请求。", "text"); err != nil {
		t.Fatalf("index py.md: %v", err)
	}

	ctx := context.Background()
	// 查询 Go 相关内容，应返回非空且包含 go.md 的内容。
	out, err := m.RetrieveContext(ctx, "7", "Go 编写 HTTP 服务", 4000)
	if err != nil {
		t.Fatalf("RetrieveContext: %v", err)
	}
	if out == "" {
		t.Fatalf("expected non-empty retrieval for Go query")
	}
	if !containsStr(out, "go.md") {
		t.Fatalf("expected retrieval to reference go.md: %s", out)
	}

	// 无知识库的用户返回空（安全跳过）。
	empty, err := m.RetrieveContext(ctx, "999", "anything", 4000)
	if err != nil {
		t.Fatalf("RetrieveContext empty user: %v", err)
	}
	if empty != "" {
		t.Fatalf("expected empty retrieval for user without KBs, got: %s", empty)
	}
}

func TestManager_DeleteDocumentAndKnowledge(t *testing.T) {
	db := newTestDB(t)
	m := NewManager(db)
	kbID := seedKB(t, db, 3)

	if _, err := m.IndexDocument(context.Background(), kbID, "a.md", "Alpha 文档内容。", "text"); err != nil {
		t.Fatalf("index a.md: %v", err)
	}
	if _, err := m.IndexDocument(context.Background(), kbID, "b.md", "Beta 文档内容。", "text"); err != nil {
		t.Fatalf("index b.md: %v", err)
	}

	// 删除 a.md。
	del, err := m.DeleteDocument(context.Background(), kbID, "a.md")
	if err != nil {
		t.Fatalf("DeleteDocument: %v", err)
	}
	if del == 0 {
		t.Fatalf("expected >0 deleted rows for a.md")
	}
	docs, err := m.ListDocuments(context.Background(), kbID)
	if err != nil {
		t.Fatalf("ListDocuments after delete: %v", err)
	}
	if len(docs) != 1 || docs[0].Name != "b.md" {
		t.Fatalf("expected only b.md remaining, got %+v", docs)
	}

	// 删除整个知识库向量。
	if err := m.DeleteKnowledge(context.Background(), kbID); err != nil {
		t.Fatalf("DeleteKnowledge: %v", err)
	}
	docs, err = m.ListDocuments(context.Background(), kbID)
	if err != nil {
		t.Fatalf("ListDocuments after DeleteKnowledge: %v", err)
	}
	if len(docs) != 0 {
		t.Fatalf("expected 0 docs after DeleteKnowledge, got %+v", docs)
	}
}

func TestManager_IndexEmptyContent(t *testing.T) {
	db := newTestDB(t)
	m := NewManager(db)
	kbID := seedKB(t, db, 1)
	if _, err := m.IndexDocument(context.Background(), kbID, "empty.md", "   ", "text"); err == nil {
		t.Fatalf("expected error for empty content")
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
