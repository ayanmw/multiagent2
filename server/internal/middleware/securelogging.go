package middleware

import (
	"net/http"
	"net/textproto"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/obslog"
	"github.com/gin-gonic/gin"
)

// sensitiveHeaders are never echoed into request logs. Even though this logger
// does not print raw header values, redactHeaders is applied defensively so
// that any future header logging (or reuse of this helper for audit) cannot
// leak bearer tokens or API keys (MX-07: 敏感信息不出日志).
var sensitiveHeaders = []string{"Authorization", "X-API-Key"}

// redactHeaders returns a clone of h with sensitive values masked. A nil input
// yields a nil output. Header keys are canonicalized (e.g. "X-API-Key" ->
// "X-Api-Key") before lookup because http.Header stores canonicalized keys and
// direct map indexing does not normalize them.
func redactHeaders(h http.Header) http.Header {
	if h == nil {
		return nil
	}
	out := h.Clone()
	for _, k := range sensitiveHeaders {
		ck := textproto.CanonicalMIMEHeaderKey(k)
		if _, ok := out[ck]; ok {
			out.Set(ck, "***redacted***")
		}
	}
	return out
}

// SecureLogger is a drop-in replacement for gin.Logger() that records request
// method / path / status / latency / client IP but NEVER logs request bodies or
// sensitive header values (Authorization / X-API-Key). This guarantees auth
// secrets cannot leak through the access log (MX-07).
//
// M7-06：输出改为结构化 JSON（经 obslog，自动携带 RequestID 中间件注入的
// trace_id / request_id），使访问日志可与其他链路日志按同一 trace 串联；
// 同时保留敏感头掩码防御（redactHeaders），防止未来改动引入泄露。
func SecureLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery
		// Defensive: ensure sensitive headers are masked should any future
		// code decide to log them. No-op for the current log line below.
		_ = redactHeaders(c.Request.Header)

		c.Next()

		latency := time.Since(start)
		if query != "" {
			path = path + "?" + query
		}
		obslog.Ctx(c.Request.Context()).Info("http.request",
			"method", c.Request.Method,
			"path", path,
			"status", c.Writer.Status(),
			"latency_ms", latency.Milliseconds(),
			"client_ip", c.ClientIP(),
		)
	}
}
