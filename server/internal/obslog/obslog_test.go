package obslog

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"log/slog"
	"strings"
	"testing"
)

// capture 初始化一个输出到 buf 的 JSON logger，并返回 restore 回调（恢复全局 logger）。
func capture(t *testing.T) (*bytes.Buffer, func()) {
	t.Helper()
	var buf bytes.Buffer
	if err := Init(Config{Format: FormatJSON, Level: slog.LevelDebug, Output: &buf}); err != nil {
		t.Fatalf("obslog.Init: %v", err)
	}
	return &buf, func() {
		// 恢复标准 slog 默认，避免影响其他测试。
		slog.SetDefault(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	}
}

// decodeLast 解析 buf 里最后一行的 JSON 对象。
func decodeLast(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) == 0 || lines[len(lines)-1] == "" {
		t.Fatalf("无日志行输出: %q", buf.String())
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &m); err != nil {
		t.Fatalf("解析日志 JSON 失败 %q: %v", lines[len(lines)-1], err)
	}
	return m
}

func TestInit_JSONFormat(t *testing.T) {
	var buf bytes.Buffer
	if err := Init(Config{Format: FormatJSON, Level: slog.LevelInfo, Output: &buf}); err != nil {
		t.Fatal(err)
	}
	defer slog.SetDefault(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))

	slog.Info("hello", "key", "value")
	m := decodeLast(t, &buf)
	if m["msg"] != "hello" {
		t.Errorf("msg = %v, want hello", m["msg"])
	}
	if m["key"] != "value" {
		t.Errorf("key = %v, want value", m["key"])
	}
	if _, ok := m["time"]; !ok {
		t.Error("JSON 日志缺少 time 字段")
	}
	if _, ok := m["level"]; !ok {
		t.Error("JSON 日志缺少 level 字段")
	}
}

func TestInit_TextFormat(t *testing.T) {
	var buf bytes.Buffer
	if err := Init(Config{Format: FormatText, Level: slog.LevelInfo, Output: &buf}); err != nil {
		t.Fatal(err)
	}
	defer slog.SetDefault(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	slog.Info("text-log")
	if !strings.Contains(buf.String(), "text-log") {
		t.Errorf("文本格式输出缺消息: %q", buf.String())
	}
}

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"debug": slog.LevelDebug, "DEBUG": slog.LevelDebug,
		"info": slog.LevelInfo, "warn": slog.LevelWarn, "warning": slog.LevelWarn,
		"error": slog.LevelError, "": slog.LevelInfo, "bogus": slog.LevelInfo,
	}
	for in, want := range cases {
		if got := ParseLevel(in); got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestTraceContext(t *testing.T) {
	ctx := WithTrace(context.Background(), "abc", "req123")
	traceID, requestID := TraceFrom(ctx)
	if traceID != "abc" || requestID != "req123" {
		t.Errorf("TraceFrom = (%q,%q), want (abc,req123)", traceID, requestID)
	}
	// 空值不覆盖已有字段。
	ctx2 := WithTrace(ctx, "", "")
	t2, r2 := TraceFrom(ctx2)
	if t2 != "abc" || r2 != "req123" {
		t.Errorf("空值 WithTrace 不应覆盖: (%q,%q)", t2, r2)
	}
	// 未注入的 ctx 返回空串。
	t3, r3 := TraceFrom(context.Background())
	if t3 != "" || r3 != "" {
		t.Errorf("空 ctx 应返回空串: (%q,%q)", t3, r3)
	}
}

func TestIDFormats(t *testing.T) {
	if got := NewTraceID(); len(got) != 32 {
		t.Errorf("NewTraceID 长度 = %d, want 32", len(got))
	}
	if got := NewSpanID(); len(got) != 16 {
		t.Errorf("NewSpanID 长度 = %d, want 16", len(got))
	}
	if got := NewRequestID(); len(got) != 16 {
		t.Errorf("NewRequestID 长度 = %d, want 16", len(got))
	}
}

func TestCtxAttachesTraceFields(t *testing.T) {
	buf, restore := capture(t)
	defer restore()

	ctx := WithTrace(context.Background(), "trace-123", "req-456")
	Ctx(ctx).Info("ctx-log")
	m := decodeLast(t, buf)
	if m["trace_id"] != "trace-123" {
		t.Errorf("trace_id = %v, want trace-123", m["trace_id"])
	}
	if m["request_id"] != "req-456" {
		t.Errorf("request_id = %v, want req-456", m["request_id"])
	}
}

func TestStartSpan_OkAndError(t *testing.T) {
	buf, restore := capture(t)
	defer restore()

	// 成功 span：ctx 自动获得新 trace_id（无父 ctx），span.end 含 span_name/duration/status。
	ctx, end := StartSpan(context.Background(), "test.op", "attr", "v1")
	if got := SpanFrom(ctx); len(got) != 16 {
		t.Fatalf("StartSpan 应注入 span_id, got %q", got)
	}
	end(nil, "extra", 42)
	m := decodeLast(t, buf)
	if m["span_name"] != "test.op" {
		t.Errorf("span_name = %v", m["span_name"])
	}
	if m["status"] != "ok" {
		t.Errorf("status = %v, want ok", m["status"])
	}
	if m["attr"] != "v1" || m["extra"] != float64(42) {
		t.Errorf("span 属性丢失: %v", m)
	}
	if m["trace_id"] == "" {
		t.Error("无父 ctx 时 span 应自生成 trace_id")
	}
	if m["duration_ms"] == nil {
		t.Error("span.end 应含 duration_ms")
	}

	// 错误 span：status=error 且附 err 字段。
	buf.Reset()
	_, end2 := StartSpan(WithTrace(context.Background(), "tr2", "rq2"), "err.op")
	end2(errTest("boom"))
	m2 := decodeLast(t, buf)
	if m2["status"] != "error" {
		t.Errorf("status = %v, want error", m2["status"])
	}
	if m2["err"] != "boom" {
		t.Errorf("err = %v, want boom", m2["err"])
	}
	if m2["trace_id"] != "tr2" {
		t.Errorf("trace_id = %v, want tr2（子 span 继承父 trace）", m2["trace_id"])
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }

func TestStartSpan_ParentChain(t *testing.T) {
	buf, restore := capture(t)
	defer restore()

	// 父 span → 子 span：子 span.end 应记录 parent_span_id 指向父 span。
	ctx, endParent := StartSpan(context.Background(), "parent.op")
	parentID := SpanFrom(ctx)
	_, endChild := StartSpan(ctx, "child.op")
	endChild(nil)
	cm := decodeLast(t, buf)
	if cm["parent_span_id"] != parentID {
		t.Errorf("child parent_span_id = %v, want %v", cm["parent_span_id"], parentID)
	}
	if cm["span_id"] == parentID {
		t.Error("child span_id 不应等于 parent span_id")
	}
	// 两条 span.end 共用同一 trace_id（继承）。
	buf.Reset()
	endParent(nil)
	pm := decodeLast(t, buf)
	if pm["trace_id"] != cm["trace_id"] {
		t.Errorf("父/子 span trace_id 不一致: %v vs %v", pm["trace_id"], cm["trace_id"])
	}
}

func TestInit_RedirectsStdLog(t *testing.T) {
	var buf bytes.Buffer
	if err := Init(Config{Format: FormatJSON, Level: slog.LevelInfo, Output: &buf}); err != nil {
		t.Fatal(err)
	}
	defer slog.SetDefault(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))

	// 标准 log 包的输出应重定向进 JSON handler（msg 字段承载原文）。
	log.Printf("[WARN] 存量日志")
	m := decodeLast(t, &buf)
	if !strings.Contains(m["msg"].(string), "存量日志") {
		t.Errorf("标准 log 未重定向: %v", m)
	}
}
