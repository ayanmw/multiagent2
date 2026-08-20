package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/auth"
	"github.com/ayanmw/multiagent2/server/internal/im"
	"github.com/ayanmw/multiagent2/server/internal/middleware"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// ---- 测试基础设施 ----

// mockIMSender 捕获回发调用（断言「完成通知回发 IM」）。
type mockIMSender struct {
	mu    sync.Mutex
	sent  []im.InboundMessage
	texts []string
}

func (m *mockIMSender) Send(_ context.Context, msg im.InboundMessage, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	m.texts = append(m.texts, text)
	return nil
}

func (m *mockIMSender) last() (im.InboundMessage, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sent) == 0 {
		return im.InboundMessage{}, ""
	}
	return m.sent[len(m.sent)-1], m.texts[len(m.texts)-1]
}

func (m *mockIMSender) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sent)
}

func newIMTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.IMBinding{}, &model.Role{}, &model.RolePermission{}, &model.User{}, &model.AuditLog{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, r := range model.SeedRoles() {
		if err := db.Create(&r).Error; err != nil {
			t.Fatalf("seed role %q: %v", r.Name, err)
		}
	}
	return db
}

// newIMWebhookRouter 构造挂载了 IM webhook + bindings 路由的 gin engine。
func newIMWebhookRouter(t *testing.T, db *gorm.DB, run IMRunFunc, sender im.Sender, secret string) (*gin.Engine, *mockIMSender) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ms := sender.(*mockIMSender)
	limiter := NewWebhookRateLimiter(10, time.Minute)
	h := NewIMWebhookHandler(IMWebhookOptions{
		DB:      db,
		Limiter: limiter,
		RunFunc: run,
		Senders: map[im.Platform]im.Sender{im.Feishu: ms, im.DingTalk: ms, im.WeCom: ms},
		Secrets: map[im.Platform]string{im.Feishu: secret, im.DingTalk: secret, im.WeCom: secret},
	})
	r := gin.New()
	r.POST("/api/im/:platform/webhook", h.Handle)
	protected := r.Group("/api")
	protected.Use(middleware.AuthMiddleware("test-jwt", db))
	protected.GET("/im/bindings", middleware.RequirePermission(db, "im", "read"), ListIMBindingsHandler(db))
	protected.POST("/im/bindings", middleware.RequirePermission(db, "im", "write"), CreateIMBindingHandler(db))
	protected.DELETE("/im/bindings/:id", middleware.RequirePermission(db, "im", "write"), DeleteIMBindingHandler(db))
	return r, ms
}

// imTestUser 创建测试用户并返回 id + role。
func imTestUser(t *testing.T, db *gorm.DB, username, role string) uint {
	t.Helper()
	u := model.User{Username: username, Email: username + "@test.dev", PasswordHash: "x", DisplayName: username}
	db.Create(&u)
	if role == "" {
		return u.ID
	}
	// 补角色列（RBAC 中间件从 context 读 role，测试直接 Set，无需真正关联角色表）
	return u.ID
}

// ---- bindings CRUD ----

func TestIMBindings_CRUD_OwnerIsolation(t *testing.T) {
	db := newIMTestDB(t)
	uid1 := imTestUser(t, db, "alice", "")
	uid2 := imTestUser(t, db, "bob", "")
	gin.SetMode(gin.TestMode)

	// 创建（alice）
	router := gin.New()
	protected := router.Group("/api")
	protected.Use(middleware.AuthMiddleware("test-jwt", db))
	protected.POST("/im/bindings", middleware.RequirePermission(db, "im", "write"), CreateIMBindingHandler(db))
	protected.GET("/im/bindings", middleware.RequirePermission(db, "im", "read"), ListIMBindingsHandler(db))
	protected.DELETE("/im/bindings/:id", middleware.RequirePermission(db, "im", "write"), DeleteIMBindingHandler(db))

	body := `{"platform":"feishu","im_user_id":"ou_123","chat_id":"oc_456","username":"alice"}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/im/bindings", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tokenFor(uid1))
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: want 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Binding model.IMBinding `json:"binding"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Binding.UserID != uid1 {
		t.Fatalf("binding.user_id = %d, want %d", resp.Binding.UserID, uid1)
	}

	// 重复创建 → 409
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodPost, "/api/im/bindings", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tokenFor(uid1))
	router.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate: want 409, got %d", w.Code)
	}

	// 越权删除（bob 删 alice 的绑定）→ 403
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodDelete, "/api/im/bindings/1", nil)
	req.Header.Set("Authorization", "Bearer "+tokenFor(uid2))
	router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-user delete: want 403, got %d", w.Code)
	}

	// 本人删除 → 200
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodDelete, "/api/im/bindings/1", nil)
	req.Header.Set("Authorization", "Bearer "+tokenFor(uid1))
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("own delete: want 200, got %d", w.Code)
	}
}

// ---- webhook 全链路 ----

func TestIMWebhook_BoundUser_TriggersLoop_AndReplies(t *testing.T) {
	db := newIMTestDB(t)
	uid := imTestUser(t, db, "alice", "")
	// 先建绑定
	if err := repoCreateBinding(db, uid, "feishu", "ou_123", "oc_456"); err != nil {
		t.Fatalf("seed binding: %v", err)
	}

	var calledUID uint
	var calledSession, calledText string
	run := func(ctx context.Context, uid uint, sessionKey, text string) (string, error) {
		calledUID = uid
		calledSession = sessionKey
		calledText = text
		return "已完成部署 ✅", nil
	}
	router, ms := newIMWebhookRouter(t, db, run, &mockIMSender{}, "")

	body := `{"schema":"2.0","header":{"event_type":"im.message.receive_v1"},"event":{"sender":{"sender_id":{"open_id":"ou_123"},"sender_type":"user"},"message":{"message_id":"om_1","chat_id":"oc_456","message_type":"text","content":"{\"text\":\"部署一下\"}"}}}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/im/feishu/webhook", bytes.NewBufferString(body))
	router.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d: %s", w.Code, w.Body.String())
	}
	// 解析返回体含 session_key
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal resp: %v", err)
	}
	if resp["session_key"] != "im:feishu:oc_456" {
		t.Fatalf("session_key = %v, want im:feishu:oc_456", resp["session_key"])
	}

	// 等待异步 goroutine 完成（轮询，上限 2s）
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ms.count() > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if calledUID != uid || calledSession != "im:feishu:oc_456" || calledText != "部署一下" {
		t.Fatalf("runFunc not called with expected args: uid=%d session=%q text=%q", calledUID, calledSession, calledText)
	}
	_, txt := ms.last()
	if !strings.Contains(txt, "已完成部署") {
		t.Fatalf("reply not sent back: %q", txt)
	}
}

func TestIMWebhook_UnboundUser_GetsGuidance(t *testing.T) {
	db := newIMTestDB(t)
	router, ms := newIMWebhookRouter(t, db, func(ctx context.Context, uid uint, s, txt string) (string, error) {
		t.Fatal("runFunc should not be called for unbound user")
		return "", nil
	}, &mockIMSender{}, "")

	body := `{"schema":"2.0","header":{"event_type":"im.message.receive_v1"},"event":{"sender":{"sender_id":{"open_id":"ou_unbound"},"sender_type":"user"},"message":{"message_id":"om_2","chat_id":"oc_789","message_type":"text","content":"{\"text\":\"hi\"}"}}}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/im/feishu/webhook", bytes.NewBufferString(body))
	router.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d", w.Code)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ms.count() > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if ms.count() == 0 {
		t.Fatal("no guidance reply sent")
	}
	_, txt := ms.last()
	if !strings.Contains(txt, "尚未绑定") {
		t.Fatalf("guidance reply missing: %q", txt)
	}
}

func TestIMWebhook_InvalidSignature_401(t *testing.T) {
	db := newIMTestDB(t)
	router, _ := newIMWebhookRouter(t, db, func(ctx context.Context, uid uint, s, txt string) (string, error) {
		t.Fatal("runFunc should not be called")
		return "", nil
	}, &mockIMSender{}, "enc-secret")

	body := []byte(`{"schema":"2.0","header":{"event_type":"im.message.receive_v1"},"event":{"sender":{"sender_id":{"open_id":"ou_1"},"sender_type":"user"},"message":{"message_id":"om_1","chat_id":"oc_1","message_type":"text","content":"{\"text\":\"x\"}"}}}`)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/im/feishu/webhook", bytes.NewBuffer(body))
	req.Header.Set("X-Lark-Request-Timestamp", "1700000000")
	req.Header.Set("X-Lark-Signature", "bad-signature")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestIMWebhook_ValidSignature_Passes(t *testing.T) {
	db := newIMTestDB(t)
	uid := imTestUser(t, db, "alice", "")
	if err := repoCreateBinding(db, uid, "dingtalk", "staff-1", "cid1"); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	var called bool
	router, _ := newIMWebhookRouter(t, db, func(ctx context.Context, uid uint, s, t string) (string, error) {
		called = true
		return "ok", nil
	}, &mockIMSender{}, "ding-secret")

	// 用正确签名构造钉钉回调（timestamp + sign header）
	body := []byte(`{"senderStaffId":"staff-1","conversationId":"cid1","msgtype":"text","text":{"content":"hi"}}`)
	timestamp := "1700000000000"
	sign := dingTalkTestSign("ding-secret", timestamp)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/im/dingtalk/webhook", bytes.NewBuffer(body))
	req.Header.Set("timestamp", timestamp)
	req.Header.Set("sign", sign)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d: %s", w.Code, w.Body.String())
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if called {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !called {
		t.Fatal("runFunc not called")
	}
}

func TestIMWebhook_UnsupportedPlatform_400(t *testing.T) {
	db := newIMTestDB(t)
	router, _ := newIMWebhookRouter(t, db, nil, &mockIMSender{}, "")
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/im/slack/webhook", bytes.NewBufferString(`{}`))
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestIMWebhook_NonTextMessage_400(t *testing.T) {
	db := newIMTestDB(t)
	router, _ := newIMWebhookRouter(t, db, nil, &mockIMSender{}, "")
	body := `{"schema":"2.0","header":{"event_type":"im.message.receive_v1"},"event":{"sender":{"sender_id":{"open_id":"ou_1"},"sender_type":"user"},"message":{"message_id":"om_1","chat_id":"oc_1","message_type":"image","content":"{}"}}}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/im/feishu/webhook", bytes.NewBufferString(body))
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

// ---- 辅助 ----

func repoCreateBinding(db *gorm.DB, uid uint, platform, imUserID, chatID string) error {
	return db.Create(&model.IMBinding{UserID: uid, Platform: platform, IMUserID: imUserID, ChatID: chatID}).Error
}

// tokenFor 生成 JWT（AuthMiddleware 用 "test-jwt" 验签；角色为 developer）。
func tokenFor(uid uint) string {
	tok, err := auth.GenerateToken("test-jwt", uid, model.RoleDeveloper)
	if err != nil {
		panic(err)
	}
	return tok
}

func dingTalkTestSign(secret, timestamp string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "\n" + secret))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
