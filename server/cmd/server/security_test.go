// security_test.go 实现 M7.5-05「安全复核」的集成级验收测试。
//
// 与 M7.5-01/02/03 一脉相承：MX-07 / M7-06 / M7-04 的安全交付物（登录·对话限流、
// CORS 白名单、访问日志脱敏、Alertmanager webhook 共享密钥）此前仅经中间件单测验证，
// 本套件走**完整 buildRouter 中间件链**（RequestID → CORS → SecureLogger → 限流 →
// 鉴权 → handler），用 config.Load() + t.Setenv 注入低阈值/白名单配置，验证防线在
// 真实路由上生效（而非仅单元层正确）。
//
// 运行：go test -count=1 -v ./cmd/server/ -run 'TestSecurity_' -timeout 10m
package main

import (
	"bytes"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/api"
	"github.com/ayanmw/multiagent2/server/internal/config"
	"github.com/ayanmw/multiagent2/server/internal/notify"
	"github.com/ayanmw/multiagent2/server/internal/obslog"
	"github.com/ayanmw/multiagent2/server/internal/provider"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/gin-gonic/gin"
)

// newSecurityRouter 构造带安全配置（从环境变量加载）的完整路由。
// 用 config.Load() 而非手动构造 Config：限流阈值/开关、CORS 白名单等私有字段
// 只能经 Load 从环境变量设置；t.Setenv 保证测试间环境隔离（同包测试串行执行）。
func newSecurityRouter(t *testing.T) (*gin.Engine, *repo.DB, *config.Config) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := config.Load()
	cfg.DBPath = filepath.Join(t.TempDir(), "sec.db")
	cfg.Port = "0"
	cfg.WorkspaceRoot = t.TempDir()
	db, err := repo.NewDB(cfg)
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, e := db.DB.DB(); e == nil {
			_ = sqlDB.Close()
		}
	})
	disc := provider.NewDiscoverer(cfg.EncryptionKey, time.Minute)
	r := buildRouter(db, cfg, disc, nil, false, nil, nil, nil,
		buildGateway(db, cfg, nil, false, nil, nil, nil))
	return r, db, cfg
}

// TestSecurity_HighFreqLoginRateLimited 验证 MX-07：高频登录被限流。
// 低阈值 3 次/60s：前 3 次凭据错误返回 401（请求到达 handler），第 4 次被中间件 429 拦截。
func TestSecurity_HighFreqLoginRateLimited(t *testing.T) {
	t.Setenv("RATE_LIMIT_LOGIN_ENABLED", "true")
	t.Setenv("RATE_LIMIT_LOGIN_LIMIT", "3")
	t.Setenv("RATE_LIMIT_LOGIN_WINDOW_SECONDS", "60")
	r, _, _ := newSecurityRouter(t)
	c := &e2eClient{t: t, r: r}

	var codes []int
	for i := 0; i < 4; i++ {
		code, _ := c.do("POST", "/api/auth/login", map[string]any{
			"account":  "nobody",
			"password": "wrong",
		})
		codes = append(codes, code)
	}
	t.Logf("登录 4 连发状态码：%v", codes)
	for i := 0; i < 3; i++ {
		if codes[i] != http.StatusUnauthorized {
			t.Errorf("前 3 次登录应 401（未达限流），第 %d 次 got %d", i+1, codes[i])
		}
	}
	if codes[3] != http.StatusTooManyRequests {
		t.Errorf("第 4 次登录应 429（触发限流），got %d", codes[3])
	}
}

// TestSecurity_ChatRateLimited 验证 MX-07：对话按认证用户限流（缺省回落 IP）。
// 注册用户拿 token 后连打 /api/chat（无启用 provider，handler 返回 502），
// 前 3 次请求到达 handler，第 4 次被中间件 429 拦截——限流先于业务逻辑生效。
func TestSecurity_ChatRateLimited(t *testing.T) {
	t.Setenv("RATE_LIMIT_CHAT_ENABLED", "true")
	t.Setenv("RATE_LIMIT_CHAT_LIMIT", "3")
	t.Setenv("RATE_LIMIT_CHAT_WINDOW_SECONDS", "60")
	r, _, _ := newSecurityRouter(t)
	c := &e2eClient{t: t, r: r}

	code, _ := c.do("POST", "/api/auth/register", map[string]any{
		"username": "chatuser",
		"email":    "chat@example.com",
		"password": "secret123",
	})
	if code != http.StatusCreated {
		t.Fatalf("注册应 201，got %d", code)
	}
	loginCode, loginResp := c.do("POST", "/api/auth/login", map[string]any{
		"account":  "chatuser",
		"password": "secret123",
	})
	if loginCode != http.StatusOK {
		t.Fatalf("登录应 200，got %d", loginCode)
	}
	tok, _ := loginResp["token"].(string)
	if tok == "" {
		t.Fatal("登录响应缺 token")
	}
	c.tok = tok

	var codes []int
	for i := 0; i < 4; i++ {
		code, _ := c.do("POST", "/api/chat", map[string]any{"message": "hi"})
		codes = append(codes, code)
	}
	t.Logf("chat 4 连发状态码：%v", codes)
	for i := 0; i < 3; i++ {
		if codes[i] == http.StatusTooManyRequests {
			t.Errorf("第 %d 次 chat 不应 429（未达限流），got %v", i+1, codes)
		}
	}
	if codes[3] != http.StatusTooManyRequests {
		t.Errorf("第 4 次 chat 应 429（触发限流），got %d", codes[3])
	}
}

// TestSecurity_CORSWhitelist 验证 MX-07：CORS 仅放行白名单源，无反射回显。
// 白名单源预检 204 且回显 ACAO + Allow-Credentials；非白名单源不获 ACAO 头
// （浏览器据此拒绝读取响应），预检不被短路（避免非 CORS OPTIONS 意外放行）。
func TestSecurity_CORSWhitelist(t *testing.T) {
	t.Setenv("CORS_ALLOWED_ORIGINS", "http://localhost:5173")
	r, _, _ := newSecurityRouter(t)

	// 白名单源 OPTIONS 预检：204 + ACAO 回显 + 凭证。
	req := httptest.NewRequest(http.MethodOptions, "/api/auth/login", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "POST")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Errorf("白名单预检应 204，got %d", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Errorf("ACAO 应回显白名单源，got %q", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("Allow-Credentials 应为 true，got %q", got)
	}

	// 非白名单源预检：不获 ACAO 头（无反射回显）。
	req2 := httptest.NewRequest(http.MethodOptions, "/api/auth/login", nil)
	req2.Header.Set("Origin", "http://evil.example.com")
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if got := w2.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("非白名单源不应获 ACAO 头，got %q", got)
	}

	// 非白名单源普通请求：服务照常响应但无 ACAO 头（浏览器据此拦截读取）。
	req3 := httptest.NewRequest(http.MethodGet, "/health", nil)
	req3.Header.Set("Origin", "http://evil.example.com")
	w3 := httptest.NewRecorder()
	r.ServeHTTP(w3, req3)
	if w3.Code != http.StatusOK {
		t.Errorf("/health 应 200，got %d", w3.Code)
	}
	if got := w3.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("非白名单源普通请求不应获 ACAO 头，got %q", got)
	}
}

// TestSecurity_AccessLog_NoPlaintext 验证 MX-07 + M7-06：访问日志不落明文敏感信息。
// 捕获 obslog 输出，请求携带明文 Authorization / X-API-Key / body 密码，
// 断言访问日志 JSON 中三者均不出现（SecureLogger 不打印 body；redactHeaders 防御掩码，
// 防止未来改动引入泄露——M0.5-06 已将 message 移入 body 亦复验）。
func TestSecurity_AccessLog_NoPlaintext(t *testing.T) {
	var buf bytes.Buffer
	if err := obslog.Init(obslog.Config{Format: obslog.FormatJSON, Level: slog.LevelDebug, Output: &buf}); err != nil {
		t.Fatalf("obslog.Init: %v", err)
	}
	defer func() {
		slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
		log.SetOutput(io.Discard)
	}()

	r, _, _ := newSecurityRouter(t)

	body := `{"username":"secuser","password":"plaintext-password-789"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer super-secret-plaintext-123")
	req.Header.Set("X-API-Key", "apikey-plaintext-456")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	out := buf.String()
	t.Logf("访问日志（截取 600B）：%s", truncateStr(out, 600))
	if !strings.Contains(out, "http.request") {
		t.Error("访问日志未输出 http.request 记录")
	}
	for _, secret := range []string{"super-secret-plaintext-123", "apikey-plaintext-456", "plaintext-password-789"} {
		if strings.Contains(out, secret) {
			t.Errorf("访问日志泄漏明文：%q", secret)
		}
	}
}

// TestSecurity_AlertsWebhookToken 验证 M7-04：Alertmanager webhook 共享密钥。
// /api/alerts 由 main() 单独注册（不在 buildRouter 内），此处仿照 main() 接线到
// 同一 router，验证无令牌 / 错令牌 401、正确令牌（Bearer 与 X-Alert-Token 双通道）200。
func TestSecurity_AlertsWebhookToken(t *testing.T) {
	t.Setenv("ALERT_WEBHOOK_TOKEN", "secret123")
	t.Setenv("ALERT_NOTIFY_USER_IDS", "1")
	r, db, cfg := newSecurityRouter(t)

	notifier := notify.NewService(db.DB, "", nil)
	h := api.NewAlertsWebhookHandler(notifier).WithToken(cfg.AlertWebhookToken()).WithTargetUsers(cfg.AlertNotifyUserIDs())
	r.POST("/api/alerts", h.Handle)

	payload := `{"status":"firing","alerts":[{"status":"firing","labels":{"alertname":"TestAlert"}}]}`

	// 无令牌 → 401
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/alerts", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("无令牌应 401，got %d", w.Code)
	}

	// 错误令牌 → 401
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/api/alerts", strings.NewReader(payload))
	req2.Header.Set("Authorization", "Bearer wrong")
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusUnauthorized {
		t.Errorf("错误令牌应 401，got %d", w2.Code)
	}

	// 正确令牌（Bearer）→ 200 且收到 1 条 firing 告警
	w3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodPost, "/api/alerts", strings.NewReader(payload))
	req3.Header.Set("Authorization", "Bearer secret123")
	r.ServeHTTP(w3, req3)
	if w3.Code != http.StatusOK {
		t.Errorf("正确令牌应 200，got %d (body=%s)", w3.Code, w3.Body.String())
	}
	if !strings.Contains(w3.Body.String(), `"received":1`) {
		t.Errorf("应回显 received=1，body=%s", w3.Body.String())
	}

	// 正确令牌（X-Alert-Token 通道）→ 200
	w4 := httptest.NewRecorder()
	req4 := httptest.NewRequest(http.MethodPost, "/api/alerts", strings.NewReader(payload))
	req4.Header.Set("X-Alert-Token", "secret123")
	r.ServeHTTP(w4, req4)
	if w4.Code != http.StatusOK {
		t.Errorf("X-Alert-Token 应 200，got %d", w4.Code)
	}
}

// truncateStr 截断长字符串用于日志展示。
func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
