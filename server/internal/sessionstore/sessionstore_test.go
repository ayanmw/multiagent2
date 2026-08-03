//go:build cgo

package sessionstore

import (
	"context"
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func newTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "sess.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	return db
}

func newUserEvent(content string) *event.Event {
	return &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{Role: model.RoleUser, Content: content}},
			},
		},
	}
}

// TestSessionService_PersistAndReload 验证：AppendEvent 落盘 → 同实例读回 →
// 新建实例（复用同 db）读回（模拟进程重启），transcript 跨重启仍在。
func TestSessionService_PersistAndReload(t *testing.T) {
	db := newTestDB(t)
	key := session.Key{AppName: "go-multi-agent-v2", UserID: "u1", SessionID: "s1"}
	ctx := context.Background()

	svc := New(db)
	sess := session.NewSession(key.AppName, key.UserID, key.SessionID)
	if err := svc.AppendEvent(ctx, sess, newUserEvent("hello")); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	got, err := svc.GetSession(ctx, key)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got == nil || len(got.GetEvents()) != 1 {
		t.Fatalf("同实例期望 1 事件，实际 %d", len(got.GetEvents()))
	}

	// 模拟进程重启：新建 service 实例，复用同一 sqlite db。
	svc2 := New(db)
	got2, err := svc2.GetSession(ctx, key)
	if err != nil {
		t.Fatalf("重启后 GetSession: %v", err)
	}
	if got2 == nil || len(got2.GetEvents()) != 1 {
		t.Fatalf("重启后期望 1 事件，实际 %v", got2)
	}
}

// TestSessionService_SkipsPartial 验证：partial / 无有效内容的事件不落盘。
func TestSessionService_SkipsPartial(t *testing.T) {
	db := newTestDB(t)
	key := session.Key{AppName: "go-multi-agent-v2", UserID: "u2", SessionID: "s2"}
	ctx := context.Background()
	svc := New(db)
	sess := session.NewSession(key.AppName, key.UserID, key.SessionID)

	// partial 事件（流式增量帧）应被跳过。
	partial := &event.Event{Response: &model.Response{IsPartial: true, Choices: []model.Choice{{Message: model.Message{Content: "x"}}}}}
	if err := svc.AppendEvent(ctx, sess, partial); err != nil {
		t.Fatalf("AppendEvent partial: %v", err)
	}
	got, _ := svc.GetSession(ctx, key)
	if got != nil && len(got.GetEvents()) != 0 {
		t.Fatalf("partial 事件不应落盘，实际 %d", len(got.GetEvents()))
	}

	// 有效事件应落盘。
	if err := svc.AppendEvent(ctx, sess, newUserEvent("real")); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	got, _ = svc.GetSession(ctx, key)
	if len(got.GetEvents()) != 1 {
		t.Fatalf("有效事件应落盘，实际 %d", len(got.GetEvents()))
	}
}

// TestSessionService_EmptyNotFound 验证：从未落库的 key 返回 nil（与 inmemory 一致）。
func TestSessionService_EmptyNotFound(t *testing.T) {
	db := newTestDB(t)
	svc := New(db)
	got, err := svc.GetSession(context.Background(), session.Key{AppName: "a", UserID: "x", SessionID: "none"})
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got != nil {
		t.Fatalf("未落库应返回 nil，实际 %+v", got)
	}
}
