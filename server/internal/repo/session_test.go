package repo

import (
	"testing"

	"github.com/anmingwei/go-multi-agent-v2/internal/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// newSessionTestDB 打开内存 SQLite 并迁移会话相关表。
func newSessionTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1) // 内存库保持单连接
	if err := db.AutoMigrate(&model.Session{}, &model.Message{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestListSessionsScopedAndOrdered(t *testing.T) {
	db := newSessionTestDB(t)

	// 用户 7 创建两个会话，用户 9 创建一个（应被隔离）。
	s1 := &model.Session{UserID: 7, SessionKey: "sess-a", Title: "A"}
	s2 := &model.Session{UserID: 7, SessionKey: "sess-b", Title: "B"}
	s3 := &model.Session{UserID: 9, SessionKey: "sess-c", Title: "C"}
	if err := db.Create(s1).Error; err != nil {
		t.Fatalf("create s1: %v", err)
	}
	if err := db.Create(s2).Error; err != nil {
		t.Fatalf("create s2: %v", err)
	}
	if err := db.Create(s3).Error; err != nil {
		t.Fatalf("create s3: %v", err)
	}

	// 先给 s2 追加消息（会刷新其 updated_at），期望列表里 s2 排在 s1 前面。
	if err := AppendMessage(db, s2.ID, "user", "hi"); err != nil {
		t.Fatalf("append: %v", err)
	}

	list, err := ListSessions(db, 7)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 sessions for user 7, got %d", len(list))
	}
	if list[0].SessionKey != "sess-b" {
		t.Fatalf("expected most-recently-active session first, got %q", list[0].SessionKey)
	}

	// 跨用户查询不应返回其他用户的会话。
	other, err := ListSessions(db, 9)
	if err != nil {
		t.Fatalf("list other: %v", err)
	}
	if len(other) != 1 || other[0].SessionKey != "sess-c" {
		t.Fatalf("expected only user 9's session, got %+v", other)
	}
}

func TestListSessionMessagesInOrder(t *testing.T) {
	db := newSessionTestDB(t)

	s := &model.Session{UserID: 7, SessionKey: "sess-x", Title: "X"}
	if err := db.Create(s).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}
	// 写入一轮 user -> assistant 对话。
	if err := AppendMessage(db, s.ID, "user", "你好"); err != nil {
		t.Fatalf("append user: %v", err)
	}
	if err := AppendMessage(db, s.ID, "assistant", "你好，有什么可以帮你？"); err != nil {
		t.Fatalf("append assistant: %v", err)
	}

	msgs, err := ListSessionMessages(db, s.ID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[0].Content != "你好" {
		t.Fatalf("first message wrong: %+v", msgs[0])
	}
	if msgs[1].Role != "assistant" {
		t.Fatalf("second message should be assistant, got %q", msgs[1].Role)
	}

	// GetSessionByKey 跨用户应返回 RecordNotFound。
	if _, err := GetSessionByKey(db, 99, "sess-x"); err == nil {
		t.Fatalf("expected cross-user lookup to fail")
	}
}
