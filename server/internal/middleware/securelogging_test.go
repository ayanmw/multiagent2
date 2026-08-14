package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRedactHeaders_MasksSensitive(t *testing.T) {
	in := http.Header{}
	in.Set("Authorization", "Bearer secret-token")
	in.Set("X-API-Key", "raw-api-key")
	in.Set("Content-Type", "application/json")

	out := redactHeaders(in)
	if out.Get("Authorization") != "***redacted***" {
		t.Fatalf("Authorization not redacted: %q", out.Get("Authorization"))
	}
	if out.Get("X-API-Key") != "***redacted***" {
		t.Fatalf("X-API-Key not redacted: %q", out.Get("X-API-Key"))
	}
	if out.Get("Content-Type") != "application/json" {
		t.Fatalf("non-sensitive header mutated: %q", out.Get("Content-Type"))
	}
	// Original header map must not be mutated (defensive copy).
	if in.Get("Authorization") != "Bearer secret-token" {
		t.Fatal("original header map was mutated")
	}
}

func TestSecureLogger_NoPanicAndServes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(SecureLogger())
	r.GET("/ping", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set("Authorization", "Bearer secret")
	req.RemoteAddr = "10.0.0.9:5555"
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}
