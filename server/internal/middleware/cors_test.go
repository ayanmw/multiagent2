package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func newCORSRouter(opts CORSOptions) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CORS(opts))
	r.GET("/api/ping", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
	return r
}

func TestCORS_AllowedOrigin(t *testing.T) {
	r := newCORSRouter(CORSOptions{
		AllowedOrigins:  []string{"http://localhost:5173"},
		AllowCredentials: true,
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/ping", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	r.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("expected allow origin echo, got %q", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("expected credentials true, got %q", got)
	}
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestCORS_DisallowedOrigin(t *testing.T) {
	r := newCORSRouter(CORSOptions{
		AllowedOrigins:  []string{"http://localhost:5173"},
		AllowCredentials: true,
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/ping", nil)
	req.Header.Set("Origin", "http://evil.example.com")
	r.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected no ACAO for disallowed origin, got %q", got)
	}
	if w.Code != 200 {
		t.Fatalf("expected 200 (request still served), got %d", w.Code)
	}
}

func TestCORS_PreflightOptions(t *testing.T) {
	r := newCORSRouter(CORSOptions{
		AllowedOrigins:  []string{"http://localhost:5173"},
		AllowCredentials: true,
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/api/ping", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	r.ServeHTTP(w, req)

	if w.Code != 204 {
		t.Fatalf("expected 204 preflight, got %d", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Fatal("expected allow-methods header on preflight")
	}
}

func TestCORS_WildcardNoCredentials(t *testing.T) {
	r := newCORSRouter(CORSOptions{
		AllowedOrigins:  []string{"*"},
		AllowCredentials: true,
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/ping", nil)
	req.Header.Set("Origin", "http://anything.example.com")
	r.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("expected wildcard, got %q", got)
	}
	// Spec forbids credentials with wildcard origin.
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("expected no credentials with wildcard, got %q", got)
	}
}

func TestCORS_NoOriginPassthrough(t *testing.T) {
	r := newCORSRouter(CORSOptions{
		AllowedOrigins:  []string{"http://localhost:5173"},
		AllowCredentials: true,
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/api/ping", nil) // no Origin
	r.ServeHTTP(w, req)
	// Non-CORS OPTIONS should fall through to Gin's normal 404 (not our 204).
	if w.Code == 204 {
		t.Fatal("expected non-preflight OPTIONS to not be short-circuited")
	}
}
