package middleware

import (
	"fmt"
	"strings"

	"github.com/ayanmw/multiagent2/server/internal/obslog"
	"github.com/gin-gonic/gin"
)

// RequestID 为每个 HTTP 请求分配统一的 request_id 与 trace_id（M7-06 日志聚合 + trace 贯通）：
//   - X-Request-ID：客户端可透传（用于前后端关联），否则自动生成（8 字节随机 hex）；
//   - traceparent：客户端可透传 W3C traceparent（第二段为 trace-id），否则自动生成
//     W3C 兼容 trace-id（16 字节随机 hex）；
//
// 注入位置：gin context（c.Get("request_id")）与请求 context（obslog 上下文），
// 后续访问日志 / Gateway / 引擎 / 执行器全部自动携带这两个字段；
// 响应头回显 X-Request-ID 与 traceparent，使外部调用方可按同一 ID 关联请求与日志。
//
// 挂载顺序：必须在 SecureLogger（访问日志）之前，使访问日志能读到 request_id/trace_id。
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 请求 ID：客户端透传优先（网关/负载均衡链路沿用），否则生成。
		requestID := c.GetHeader("X-Request-ID")
		if requestID == "" {
			requestID = obslog.NewRequestID()
		}
		// trace ID：优先解析客户端 traceparent（W3C），否则新建根 trace。
		traceID := ""
		if tp := c.GetHeader("traceparent"); tp != "" {
			traceID = parseTraceParent(tp)
		}
		if traceID == "" {
			traceID = obslog.NewTraceID()
		}
		// 注入请求 context，使下游（handler/引擎/执行器）日志自动携带。
		c.Request = c.Request.WithContext(obslog.WithTrace(c.Request.Context(), traceID, requestID))
		c.Set("request_id", requestID)

		// 响应头回显：调用方可拿 X-Request-ID 到日志/监控中检索本次请求。
		c.Header("X-Request-ID", requestID)
		c.Header("traceparent", fmt.Sprintf("00-%s-%s-01", traceID, obslog.NewSpanID()))

		c.Next()
	}
}

// parseTraceParent 从 W3C traceparent 头（version-traceid-spanid-flags）提取 trace-id。
// 格式非法或 trace-id 长度不为 32 时返回空串（调用方回落自生成）。
func parseTraceParent(tp string) string {
	parts := strings.Split(tp, "-")
	if len(parts) >= 2 && len(parts[1]) == 32 {
		return parts[1]
	}
	return ""
}
