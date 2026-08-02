package repo

import (
	"sync"
	"testing"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/model"
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
	// 休眠一小段时间，确保 s2 的 updated_at 严格晚于创建时刻，
	// 避免低时钟分辨率下与 s1/s3 的 updated_at 相同导致排序不稳定（时序 flake）。
	time.Sleep(10 * time.Millisecond)
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

// TestCrossUserSameSessionKeyAllowed 验证复合唯一索引允许不同用户复用同一 key。
func TestCrossUserSameSessionKeyAllowed(t *testing.T) {
	db := newSessionTestDB(t)

	a, err := GetOrCreateSession(db, 1, "shared-key")
	if err != nil {
		t.Fatalf("user1 create: %v", err)
	}
	b, err := GetOrCreateSession(db, 2, "shared-key")
	if err != nil {
		t.Fatalf("user2 create: %v", err)
	}
	if a.SessionKey != b.SessionKey {
		t.Fatalf("keys differ: %q vs %q", a.SessionKey, b.SessionKey)
	}
	if a.ID == b.ID {
		t.Fatalf("expected distinct rows for different users, got same id %d", a.ID)
	}
	var cnt int64
	if err := db.Model(&model.Session{}).Where("session_key = ?", "shared-key").Count(&cnt).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if cnt != 2 {
		t.Fatalf("expected 2 rows for same key across users (composite unique allows reuse), got %d", cnt)
	}
}

// TestSameUserDuplicateKeyIdempotent 验证同一用户重复 key 不会新建重复行。
func TestSameUserDuplicateKeyIdempotent(t *testing.T) {
	db := newSessionTestDB(t)

	first, err := GetOrCreateSession(db, 5, "dup-key")
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	second, err := GetOrCreateSession(db, 5, "dup-key")
	if err != nil {
		t.Fatalf("second create: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("expected same row for same user+key, got %d and %d", first.ID, second.ID)
	}
	var cnt int64
	if err := db.Model(&model.Session{}).Where("user_id = ? AND session_key = ?", 5, "dup-key").Count(&cnt).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if cnt != 1 {
		t.Fatalf("expected exactly 1 row for same user+key, got %d", cnt)
	}
}

// TestConcurrentGetOrCreateSession 验证并发调用不产生脏数据（最终仅一行）。
func TestConcurrentGetOrCreateSession(t *testing.T) {
	db := newSessionTestDB(t)

	const n = 20
	var wg sync.WaitGroup
	wg.Add(n)
	errCh := make(chan error, n)
	ids := make([]uint, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			s, err := GetOrCreateSession(db, 100, "concurrent-key")
			if err != nil {
				errCh <- err
				return
			}
			ids[i] = s.ID
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent create error: %v", err)
		}
	}

	var cnt int64
	if err := db.Model(&model.Session{}).Where("user_id = ? AND session_key = ?", 100, "concurrent-key").Count(&cnt).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if cnt != 1 {
		t.Fatalf("expected exactly 1 row after %d concurrent creates, got %d", n, cnt)
	}
	for i, id := range ids {
		if id == 0 {
			t.Fatalf("goroutine %d got zero id", i)
		}
		if id != ids[0] {
			t.Fatalf("goroutine %d got different id %d vs %d", i, id, ids[0])
		}
	}
}
