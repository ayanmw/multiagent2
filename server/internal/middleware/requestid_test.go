package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ayanmw/multiagent2/server/internal/obslog"
	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestRequestID_GeneratesAndEchoes(t *testing.T) {
	r := gin.New()
	r.Use(RequestID())
	r.GET("/ping", func(c *gin.Context) {
		rid, _ := c.Get("request_id")
		traceID, reqID := obslog.TraceFrom(c.Request.Context())
		if rid != reqID {
			t.Errorf("gin context request_id(%v) 与 ctx(%q) 不一致", rid, reqID)
		}
		if len(traceID) != 32 {
			t.Errorf("ctx trace_id 长度 = %d, want 32", len(traceID))
		}
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	r.ServeHTTP(w, req)

	if got := w.Header().Get("X-Request-ID"); len(got) != 16 {
		t.Errorf("响应 X-Request-ID = %q, want 16 hex", got)
	}
	tp := w.Header().Get("traceparent")
	if !strings.HasPrefix(tp, "00-") || len(tp) != 55 { // 00-<32>-<16>-01
		t.Errorf("响应 traceparent = %q, want W3C 格式", tp)
	}
}

func TestRequestID_PassthroughAndTraceparent(t *testing.T) {
	r := gin.New()
	r.Use(RequestID())
	var gotRID, gotTID string
	r.GET("/ping", func(c *gin.Context) {
		_, gotRID = obslog.TraceFrom(c.Request.Context())
		gotTID, _ = obslog.TraceFrom(c.Request.Context())
		c.Status(http.StatusOK)
	})

	clientTraceID := strings.Repeat("a", 32)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set("X-Request-ID", "client-rid-1")
	req.Header.Set("traceparent", "00-"+clientTraceID+"-0000000000000001-01")
	r.ServeHTTP(w, req)

	if gotRID != "client-rid-1" {
		t.Errorf("request_id 未透传: %q, want client-rid-1", gotRID)
	}
	if gotTID != clientTraceID {
		t.Errorf("trace_id 未从 traceparent 提取: %q", gotTID)
	}
	if w.Header().Get("X-Request-ID") != "client-rid-1" {
		t.Errorf("响应 X-Request-ID 应回显透传值: %q", w.Header().Get("X-Request-ID"))
	}
}

func TestParseTraceParent(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-0000000000000001-01", strings.Repeat("a", 32)},
		{"00-abc-0000000000000001-01", ""},       // trace-id 长度非法
		{"not-a-traceparent", ""},                // 分段不足
		{"", ""},                                 // 空
		{"01-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-0000000000000001-00", strings.Repeat("a", 32)},
	}
	for _, c := range cases {
		if got := parseTraceParent(c.in); got != c.want {
			t.Errorf("parseTraceParent(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
