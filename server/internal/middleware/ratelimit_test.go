package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestRateLimiter_AllowsWithinLimit(t *testing.T) {
	now := time.Unix(1000, 0)
	l := NewRateLimiter(2, time.Minute, func() time.Time { return now })
	if !l.Allow("k") {
		t.Fatal("first hit should be allowed")
	}
	if !l.Allow("k") {
		t.Fatal("second hit should be allowed")
	}
	if l.Allow("k") {
		t.Fatal("third hit should be blocked (limit=2)")
	}
}

func TestRateLimiter_WindowExpiry(t *testing.T) {
	base := time.Unix(1000, 0)
	now := base
	l := NewRateLimiter(1, time.Minute, func() time.Time { return now })
	if !l.Allow("k") {
		t.Fatal("first hit should be allowed")
	}
	if l.Allow("k") {
		t.Fatal("immediate second hit should be blocked")
	}
	// Advance beyond the window; the counter should reset.
	now = base.Add(2 * time.Minute)
	if !l.Allow("k") {
		t.Fatal("hit after window expiry should be allowed")
	}
}

func TestRateLimiter_IndependentKeys(t *testing.T) {
	now := time.Unix(1000, 0)
	l := NewRateLimiter(1, time.Minute, func() time.Time { return now })
	if !l.Allow("a") {
		t.Fatal("a first should allow")
	}
	if !l.Allow("b") {
		t.Fatal("b first should allow (independent key)")
	}
	if l.Allow("a") {
		t.Fatal("a second should be blocked")
	}
}

// constantKey returns a fixed key so rate-limit tests are deterministic
// regardless of ClientIP parsing.
func constantKey(_ *gin.Context) string { return "fixed" }

func TestRateLimitMiddleware_429(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RateLimit(constantKey, 1, time.Minute))
	r.GET("/x", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	do := func() int {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		r.ServeHTTP(w, req)
		return w.Code
	}
	if do() != 200 {
		t.Fatal("first request should pass")
	}
	if do() != 429 {
		t.Fatal("second request should be rate-limited (429)")
	}
}

func TestRateLimitMiddleware_DisabledWhenLimitZero(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RateLimit(constantKey, 0, time.Minute))
	r.GET("/x", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		r.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("request %d should pass when limiter disabled (limit=0)", i)
		}
	}
}

func TestUserIDKey_Fallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.RemoteAddr = "10.0.0.5:1234"

	if got := UserIDKey(c); got != "ip:10.0.0.5" {
		t.Fatalf("expected ip fallback, got %q", got)
	}

	c.Set(CtxUserID, uint(42))
	if got := UserIDKey(c); got != "u:42" {
		t.Fatalf("expected u:42, got %q", got)
	}
}
