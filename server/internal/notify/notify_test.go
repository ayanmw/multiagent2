package notify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	if err := db.AutoMigrate(&model.Notification{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// TestService_WritesInAppNotification 验证站内信落库（M4-07 验收核心）。
func TestService_WritesInAppNotification(t *testing.T) {
	db := testDB(t)
	svc := NewService(db, "", nil)
	n := NewSuccess(7, 3, "nightly-build", "sess-x", "自动化已执行完成")
	if err := svc.Notify(context.Background(), n); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	var cnt int64
	if err := db.Model(&model.Notification{}).Count(&cnt).Error; err != nil {
		t.Fatal(err)
	}
	if cnt != 1 {
		t.Fatalf("期望 1 条站内信，实际 %d", cnt)
	}
	list, _, err := repo.ListNotifications(db, 7, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Type != model.NotificationTypeSuccess {
		t.Fatalf("站内信类型错误: %+v", list)
	}
}

// TestService_PostsCallback 验证出站 webhook 回调被触发（mock 目标，best-effort）。
func TestService_PostsCallback(t *testing.T) {
	db := testDB(t)
	var mu sync.Mutex
	var gotBody string
	var gotCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		mu.Lock()
		gotBody = string(buf[:n])
		gotCount++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	svc := NewService(db, srv.URL, nil)
	svc.HTTPClient = srv.Client()
	if err := svc.Notify(context.Background(), NewFailure(7, 3, "nightly-build", "boom")); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	// 给客户端一点时间（httptest 同步关闭时会等待未完成请求）。
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if gotCount != 1 {
		t.Fatalf("期望回调 1 次，实际 %d", gotCount)
	}
	if gotBody == "" {
		t.Fatal("回调 body 为空")
	}
}

// mockNotifier 记录调用，用于验证调用方是否正确触发通知。
type mockNotifier struct {
	mu    sync.Mutex
	calls []*model.Notification
}

func (m *mockNotifier) Notify(_ context.Context, n *model.Notification) error {
	m.mu.Lock()
	m.calls = append(m.calls, n)
	m.mu.Unlock()
	return nil
}

func (m *mockNotifier) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}
