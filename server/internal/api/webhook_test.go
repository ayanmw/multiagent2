package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/notify"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newWebhookTestDB 构造共享内存 SQLite（MaxOpenConns=1 保证 in-memory 单连接），
// 迁移 webhook 链路所需的表：automations / sessions / audit_logs（roles 一并播种，便于扩展）。
func newWebhookTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	if err := db.AutoMigrate(&model.Automation{}, &model.Session{}, &model.Role{}, &model.RolePermission{}, &model.AuditLog{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, r := range model.SeedRoles() {
		if err := db.Create(&r).Error; err != nil {
			t.Fatalf("seed role %q: %v", r.Name, err)
		}
	}
	return db
}

// mockLoopRunner 是 automationLoopRunner 的测试桩：记录调用并支持阻塞（用于测试并发重入）。
type mockLoopRunner struct {
	mu       sync.Mutex
	calls    int
	lastAuto *model.Automation
	lastKey  string
	block    chan struct{} // 非 nil 时 Run 阻塞直到被关闭
	done     chan struct{} // Run 退出前关闭（测试等待用）
}

func (m *mockLoopRunner) Run(ctx context.Context, a *model.Automation, sessionKey string) error {
	m.mu.Lock()
	m.calls++
	m.lastAuto = a
	m.lastKey = sessionKey
	m.mu.Unlock()
	if m.block != nil {
		<-m.block
	}
	if m.done != nil {
		close(m.done)
	}
	return nil
}

func newWebhookRouter(h *WebhookHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/webhooks/:token", h.Handle)
	return r
}

func createWebhookAutomation(t *testing.T, db *gorm.DB, token string, enabled bool) *model.Automation {
	t.Helper()
	a := &model.Automation{
		UserID:       1,
		Name:         "wh-" + token,
		TriggerType:  model.AutomationTriggerWebhook,
		WebhookToken: token,
		GoalPrompt:   "webhook goal for " + token,
		Enabled:      enabled,
	}
	if err := repo.CreateAutomation(db, a); err != nil {
		t.Fatalf("create automation: %v", err)
	}
	// gorm 对带 default 标签的 bool 字段在零值（false）时会省略该列，使 Enabled=false 无法落库；
	// 这里显式回写一次，确保测试意图（enabled 真值）生效，handler 方能按 enabled 过滤。
	if err := db.Model(a).Update("enabled", enabled).Error; err != nil {
		t.Fatalf("set enabled: %v", err)
	}
	return a
}

func TestWebhookRateLimiter_AllowAndReset(t *testing.T) {
	now := time.Unix(1000, 0)
	l := &WebhookRateLimiter{limit: 2, window: time.Minute, now: func() time.Time { return now }, hits: map[string][]time.Time{}}
	if !l.Allow("t") {
		t.Fatal("第 1 次应放行")
	}
	if !l.Allow("t") {
		t.Fatal("第 2 次应放行")
	}
	if l.Allow("t") {
		t.Fatal("第 3 次应拒绝（达上限）")
	}
	// 不同 token 互不影响
	if !l.Allow("other") {
		t.Fatal("其他 token 应放行")
	}
	// 超过窗口后重置
	now = now.Add(2 * time.Minute)
	if !l.Allow("t") {
		t.Fatal("窗口过后应重置并放行")
	}
}

// TestWebhookHandler_TriggersLoop 验证合法 token（启用 webhook 自动化）触发 Loop 并返回 202。
func TestWebhookHandler_TriggersLoop(t *testing.T) {
	db := newWebhookTestDB(t)
	a := createWebhookAutomation(t, db, "tok-valid", true)
	mock := &mockLoopRunner{done: make(chan struct{})}
	limiter := NewWebhookRateLimiter(10, time.Minute)
	h := NewWebhookHandler(db, mock, limiter)
	r := newWebhookRouter(h)

	body := []byte(`{"event":"push"}`)
	req, _ := http.NewRequest(http.MethodPost, "/api/webhooks/tok-valid", bytes.NewBuffer(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("期望 202，得到 %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Status       string `json:"status"`
		AutomationID uint   `json:"automation_id"`
		SessionKey   string `json:"session_key"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Status != "accepted" || resp.AutomationID != a.ID || resp.SessionKey == "" {
		t.Fatalf("响应字段异常: %+v", resp)
	}

	// 等待异步 Loop 实际被调用。
	select {
	case <-mock.done:
	case <-time.After(3 * time.Second):
		t.Fatal("超时：Loop 运行器未被调用")
	}
	mock.mu.Lock()
	calls, lastAuto, lastKey := mock.calls, mock.lastAuto, mock.lastKey
	mock.mu.Unlock()
	if calls != 1 {
		t.Fatalf("期望 runner 被调用 1 次，得到 %d", calls)
	}
	if lastAuto == nil || lastAuto.ID != a.ID {
		t.Fatalf("runner 收到的 automation 不匹配: %+v", lastAuto)
	}
	if lastKey != resp.SessionKey {
		t.Fatalf("runner 收到的 sessionKey 与响应不一致: %q != %q", lastKey, resp.SessionKey)
	}
	// 会话应已落库（handler 预先建会话）。
	if _, err := repo.GetOrCreateSession(db, 1, lastKey); err != nil {
		t.Fatalf("会话查询失败: %v", err)
	}
}

// TestWebhookHandler_InvalidToken 验证非法 token 返回 401。
func TestWebhookHandler_InvalidToken(t *testing.T) {
	db := newWebhookTestDB(t)
	mock := &mockLoopRunner{}
	limiter := NewWebhookRateLimiter(10, time.Minute)
	h := NewWebhookHandler(db, mock, limiter)
	r := newWebhookRouter(h)

	req, _ := http.NewRequest(http.MethodPost, "/api/webhooks/bogus-token", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("期望 401，得到 %d: %s", w.Code, w.Body.String())
	}
	if mock.calls != 0 {
		t.Fatalf("非法 token 不应触发 Loop（calls=%d）", mock.calls)
	}
}

// TestWebhookHandler_DisabledAutomation 验证被禁用（enabled=false）的 webhook 自动化返回 401。
func TestWebhookHandler_DisabledAutomation(t *testing.T) {
	db := newWebhookTestDB(t)
	createWebhookAutomation(t, db, "tok-disabled", false)
	mock := &mockLoopRunner{}
	limiter := NewWebhookRateLimiter(10, time.Minute)
	h := NewWebhookHandler(db, mock, limiter)
	r := newWebhookRouter(h)

	req, _ := http.NewRequest(http.MethodPost, "/api/webhooks/tok-disabled", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("期望 401，得到 %d: %s", w.Code, w.Body.String())
	}
	if mock.calls != 0 {
		t.Fatalf("禁用自动化不应触发 Loop（calls=%d）", mock.calls)
	}
}

// TestWebhookHandler_RateLimit 验证同一 token 超出速率上限时返回 429。
func TestWebhookHandler_RateLimit(t *testing.T) {
	db := newWebhookTestDB(t)
	createWebhookAutomation(t, db, "tok-ratelimit", true)
	mock := &mockLoopRunner{}
	limiter := NewWebhookRateLimiter(1, time.Minute) // 窗口内仅 1 次
	h := NewWebhookHandler(db, mock, limiter)
	r := newWebhookRouter(h)

	// 第 1 次：202
	req1, _ := http.NewRequest(http.MethodPost, "/api/webhooks/tok-ratelimit", nil)
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)
	if w1.Code != http.StatusAccepted {
		t.Fatalf("第 1 次应 202，得到 %d: %s", w1.Code, w1.Body.String())
	}
	// 第 2 次（同一 token，窗口内）：429
	req2, _ := http.NewRequest(http.MethodPost, "/api/webhooks/tok-ratelimit", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("第 2 次应 429，得到 %d: %s", w2.Code, w2.Body.String())
	}
}

// TestWebhookHandler_ConcurrentGuard 验证同一自动化正在运行时，新 webhook 返回 409（防重入）。
func TestWebhookHandler_ConcurrentGuard(t *testing.T) {
	db := newWebhookTestDB(t)
	createWebhookAutomation(t, db, "tok-block", true)
	block := make(chan struct{})
	mock := &mockLoopRunner{block: block}
	limiter := NewWebhookRateLimiter(10, time.Minute)
	h := NewWebhookHandler(db, mock, limiter)
	r := newWebhookRouter(h)

	// 第 1 次：202，且 runner 阻塞（running 锁保持）。
	req1, _ := http.NewRequest(http.MethodPost, "/api/webhooks/tok-block", nil)
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)
	if w1.Code != http.StatusAccepted {
		t.Fatalf("第 1 次应 202，得到 %d: %s", w1.Code, w1.Body.String())
	}
	// 第 2 次（同一自动化仍在跑）：409
	req2, _ := http.NewRequest(http.MethodPost, "/api/webhooks/tok-block", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusConflict {
		t.Fatalf("第 2 次应 409，得到 %d: %s", w2.Code, w2.Body.String())
	}
	// 释放阻塞，让第 1 次 goroutine 收尾。
	close(block)
}

// TestWebhookHandler_NotifiesOnSuccess 验证成功 Loop 后写入一条成功站内信（M4-07）。
func TestWebhookHandler_NotifiesOnSuccess(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	if err := db.AutoMigrate(&model.Automation{}, &model.Session{}, &model.Role{}, &model.RolePermission{}, &model.AuditLog{}, &model.Notification{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	a := createWebhookAutomation(t, db, "tok-notify", true)
	mock := &mockLoopRunner{done: make(chan struct{})}
	limiter := NewWebhookRateLimiter(10, time.Minute)
	h := NewWebhookHandler(db, mock, limiter).WithNotifier(notify.NewService(db, "", nil))
	r := newWebhookRouter(h)

	req, _ := http.NewRequest(http.MethodPost, "/api/webhooks/tok-notify", bytes.NewBufferString(`{"event":"push"}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("期望 202，得到 %d", w.Code)
	}
	select {
	case <-mock.done:
	case <-time.After(3 * time.Second):
		t.Fatal("超时：Loop 未被调用")
	}
	// 等待异步 runLoop 写完通知。
	time.Sleep(100 * time.Millisecond)
	var cnt int64
	if err := db.Model(&model.Notification{}).Where("user_id = ? AND type = ?", a.UserID, model.NotificationTypeSuccess).Count(&cnt).Error; err != nil {
		t.Fatal(err)
	}
	if cnt != 1 {
		t.Fatalf("期望 1 条成功通知，实际 %d", cnt)
	}
}
