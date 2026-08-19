// Package obslog 实现 M7-06「日志聚合 + trace 贯通」的基础设施：
// 基于标准库 log/slog 的**结构化 JSON 日志**，与 **W3C trace-id / span-id / request-id**
// 上下文的贯通能力。
//
// 设计要点：
//   - 全局日志统一为 slog（默认 JSON 输出，机器可读、可被 Loki/ELK 等集中采集）；
//     标准 log 包（log.Printf 等既有调用）经 log.SetOutput 重定向进同一 handler，
//     使存量日志行也产出 JSON，逐步收敛。
//   - ctx 携带 trace_id / request_id / span_id（W3C 格式，32/16 位十六进制），
//     业务层经 obslog.Ctx(ctx) 取派生 logger 时自动附带这三个字段；
//     按 trace_id 过滤即可把「HTTP 请求 → Gateway → Runner(LLM) → 工具执行」串成一条链。
//   - StartSpan 近似 OTel span：生成子 span_id 挂进 ctx，结束时写一条 span.end
//     JSON 日志（含 span_name / duration_ms / status / parent_span_id），
//     未来升级 OTel SDK 导出时字段语义可直接映射（traceparent 兼容）。
package obslog

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

// Format 是日志输出格式（env LOG_FORMAT）。
type Format string

const (
	// FormatJSON 结构化 JSON 输出（生产默认）：每行一个 JSON 对象，便于集中采集与字段过滤。
	FormatJSON Format = "json"
	// FormatText 人类可读文本输出（本地调试）。
	FormatText Format = "text"
)

// Config 描述 obslog 初始化参数。
type Config struct {
	// Format 为 json（默认）或 text。
	Format Format
	// Level 为日志级别（默认 slog.LevelInfo）。
	Level slog.Level
	// Output 为日志输出目标（nil 时回退 os.Stdout）。
	Output io.Writer
}

// ParseLevel 解析 LOG_LEVEL 字符串（debug/info/warn/error），非法值回退 Info。
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// Init 初始化全局 slog logger 并把标准 log 包重定向进同一 handler。
// 未调用时（如单测/纯库使用）回退 slog 默认（文本、Info 级、stderr），行为与标准库一致。
func Init(cfg Config) error {
	if cfg.Output == nil {
		cfg.Output = os.Stdout
	}
	opts := &slog.HandlerOptions{Level: cfg.Level}
	var h slog.Handler
	switch cfg.Format {
	case FormatText:
		h = slog.NewTextHandler(cfg.Output, opts)
	default:
		h = slog.NewJSONHandler(cfg.Output, opts)
	}
	l := slog.New(h)
	slog.SetDefault(l)
	// 把标准 log 包（log.Printf/log.Println/log.Default）的输出重定向进同一 handler，
	// 使存量日志行也以结构化 JSON 产出；slog.NewLogLogger 会把 log 消息作为 msg 字段记录。
	ll := slog.NewLogLogger(h, slog.LevelInfo)
	log.SetOutput(ll.Writer())
	log.SetFlags(0)
	return nil
}

// NewLogWriter 返回一个把 log 消息转发到指定 slog.Logger 的 io.Writer
// （供需要 io.Writer 的既有组件复用，如 notify.NewService 的 logger）。
func NewLogWriter(l *slog.Logger) io.Writer {
	if l == nil {
		l = slog.Default()
	}
	return &slogWriter{l: l}
}

type slogWriter struct {
	l *slog.Logger
}

func (w *slogWriter) Write(p []byte) (int, error) {
	msg := strings.TrimSpace(string(p))
	if msg == "" {
		return len(p), nil
	}
	w.l.Info(msg)
	return len(p), nil
}

// ---------------------------------------------------------------------------
// trace / request / span 上下文
// ---------------------------------------------------------------------------

type ctxKey int

const (
	ctxKeyTraceID ctxKey = iota
	ctxKeyRequestID
	ctxKeySpanID
)

// randHex 生成 n 字节随机数的十六进制编码（n=16 → 32 hex，对齐 W3C trace-id；
// n=8 → 16 hex，对齐 span-id/request-id）。熵源失败（极低概率）时回退
// 时间戳+原子计数，保证返回的 ID 始终格式合法、进程内唯一。
var fallbackCounter atomic.Uint64

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err == nil {
		return hex.EncodeToString(b)
	}
	// 回退：以「纳秒时间戳 ^ 原子计数」的 64 位值 hex 填充到目标长度（仅异常路径）。
	v := uint64(time.Now().UnixNano()) ^ fallbackCounter.Add(1)
	s := hex.EncodeToString([]byte{
		byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24),
		byte(v >> 32), byte(v >> 40), byte(v >> 48), byte(v >> 56),
	})
	for len(s) < n*2 {
		s += s
	}
	return s[:n*2]
}

// NewTraceID 生成 W3C 兼容 trace-id（32 位十六进制 = 16 字节随机）。
func NewTraceID() string { return randHex(16) }

// NewSpanID 生成 span-id（16 位十六进制 = 8 字节随机）。
func NewSpanID() string { return randHex(8) }

// NewRequestID 生成统一请求 ID（16 位十六进制 = 8 字节随机）。
func NewRequestID() string { return randHex(8) }

// WithTrace 把 trace_id / request_id 注入 ctx（空值跳过）。
func WithTrace(ctx context.Context, traceID, requestID string) context.Context {
	if traceID != "" {
		ctx = context.WithValue(ctx, ctxKeyTraceID, traceID)
	}
	if requestID != "" {
		ctx = context.WithValue(ctx, ctxKeyRequestID, requestID)
	}
	return ctx
}

// WithSpan 把 span_id 注入 ctx（StartSpan 内部使用；外部一般不需要直接调用）。
func WithSpan(ctx context.Context, spanID string) context.Context {
	if spanID == "" {
		return ctx
	}
	return context.WithValue(ctx, ctxKeySpanID, spanID)
}

// TraceFrom 从 ctx 取出 (trace_id, request_id)，未注入时返回空串。
func TraceFrom(ctx context.Context) (traceID, requestID string) {
	if ctx == nil {
		return "", ""
	}
	if v, ok := ctx.Value(ctxKeyTraceID).(string); ok {
		traceID = v
	}
	if v, ok := ctx.Value(ctxKeyRequestID).(string); ok {
		requestID = v
	}
	return traceID, requestID
}

// SpanFrom 从 ctx 取出当前 span_id，未注入时返回空串。
func SpanFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(ctxKeySpanID).(string); ok {
		return v
	}
	return ""
}

// Ctx 返回携带 trace/request/span 上下文字段的派生 logger：
// 调用点 slog 记录时无需手动拼 trace_id/request_id/span_id，
// 全部日志自动携带，从而可按 trace_id 串联一次完整对话链路。
func Ctx(ctx context.Context) *slog.Logger {
	l := slog.Default()
	traceID, requestID := TraceFrom(ctx)
	if traceID != "" {
		l = l.With("trace_id", traceID)
	}
	if requestID != "" {
		l = l.With("request_id", requestID)
	}
	if spanID := SpanFrom(ctx); spanID != "" {
		l = l.With("span_id", spanID)
	}
	return l
}

// ---------------------------------------------------------------------------
// Span（近似 OTel span 的日志化耗时记录）
// ---------------------------------------------------------------------------

// SpanEnd 是 StartSpan 返回的结束回调：err 非 nil 时 status=error 并附 err 字段，
// extra 为调用方追加的键值属性（如 exit_code / decision / reply_chars）。
type SpanEnd func(err error, extra ...any)

// StartSpan 开启一个命名 span（近似 OTel span，以日志事件表达）：
//   - trace_id 从 ctx 取（无则新生成，使该 span 自成根 trace）；
//   - 生成新的 span_id 注入返回的 ctx，同时记录 parent_span_id（还原调用树）；
//   - 调用方用返回的 ctx 继续往下传（子 span / 日志自动继承）；
//   - 结束时调用返回的回调，写一条 span.end JSON 日志
//     （span_name / duration_ms / status / parent_span_id / 调用方附加属性）。
//
// 用法：
//
//	ctx, end := obslog.StartSpan(ctx, "gateway.run", "session_key", key)
//	defer func() { end(err) }()
func StartSpan(ctx context.Context, name string, attrs ...any) (context.Context, SpanEnd) {
	if ctx == nil {
		ctx = context.Background()
	}
	traceID, requestID := TraceFrom(ctx)
	if traceID == "" {
		traceID = NewTraceID()
		ctx = context.WithValue(ctx, ctxKeyTraceID, traceID)
	}
	if requestID != "" {
		ctx = context.WithValue(ctx, ctxKeyRequestID, requestID)
	}
	parentID := SpanFrom(ctx)
	spanID := NewSpanID()
	ctx = context.WithValue(ctx, ctxKeySpanID, spanID)
	start := time.Now()
	return ctx, func(err error, extra ...any) {
		fields := make([]any, 0, len(attrs)+len(extra)+6)
		fields = append(fields, "span_name", name)
		fields = append(fields, "span_id", spanID)
		if parentID != "" {
			fields = append(fields, "parent_span_id", parentID)
		}
		fields = append(fields, "duration_ms", time.Since(start).Milliseconds())
		fields = append(fields, attrs...)
		fields = append(fields, extra...)
		if err != nil {
			fields = append(fields, "status", "error", "err", err.Error())
			Ctx(ctx).Error("span.end", fields...)
			return
		}
		fields = append(fields, "status", "ok")
		Ctx(ctx).Info("span.end", fields...)
	}
}
