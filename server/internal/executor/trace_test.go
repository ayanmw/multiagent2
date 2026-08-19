package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/obslog"
)

// captureTraceLogs 初始化 JSON 日志到 buf（返回 restore 恢复全局 slog）。
func captureTraceLogs(t *testing.T) (*bytes.Buffer, func()) {
	t.Helper()
	var buf bytes.Buffer
	if err := obslog.Init(obslog.Config{Format: obslog.FormatJSON, Level: slog.LevelDebug, Output: &buf}); err != nil {
		t.Fatalf("obslog.Init: %v", err)
	}
	return &buf, func() {
		slog.SetDefault(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	}
}

// lastJSON 解析 buf 最后一行 JSON 日志。
func lastJSON(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) == 0 || lines[len(lines)-1] == "" {
		t.Fatalf("无日志输出: %q", buf.String())
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &m); err != nil {
		t.Fatalf("解析日志失败 %q: %v", lines[len(lines)-1], err)
	}
	return m
}

// TestSafeExecutor_TraceSpan_AllowAndDeny 验证 M7-06「错误可下钻到具体工具调用」：
// SafeExecutor.Run 的每条命令都产出 executor.run span 日志，trace_id 继承调用方注入的
// 根 trace；allow 命令记 status=ok + exit_code，deny 命令记 status=error + decision=denied。
func TestSafeExecutor_TraceSpan_AllowAndDeny(t *testing.T) {
	buf, restore := captureTraceLogs(t)
	defer restore()

	inner, _ := NewHostExecutorWithTimeout(t.TempDir(), 5*time.Second)
	se := NewSafeExecutor(inner, NewDangerousCommandPolicy(ModeUnattended), NewMemoryAuditor(), nil, nil)
	rootTrace := strings.Repeat("ab", 16) // 32 hex
	ctx := obslog.WithTrace(context.Background(), rootTrace, "req-span-test")

	// ① allow：echo 经 cmd.exe 正常执行 → span status=ok、decision=allowed、exit_code=0。
	if _, err := se.Run(ctx, "echo hello"); err != nil {
		t.Fatalf("allow 命令不应失败: %v", err)
	}
	m := lastJSON(t, buf)
	if m["span_name"] != "executor.run" {
		t.Errorf("span_name = %v, want executor.run", m["span_name"])
	}
	if m["status"] != "ok" {
		t.Errorf("status = %v, want ok", m["status"])
	}
	if m["decision"] != "allowed" {
		t.Errorf("decision = %v, want allowed", m["decision"])
	}
	if m["exit_code"] != float64(0) {
		t.Errorf("exit_code = %v, want 0", m["exit_code"])
	}
	if m["trace_id"] != rootTrace {
		t.Errorf("trace_id = %v, want 继承根 trace %s", m["trace_id"], rootTrace)
	}
	if m["request_id"] != "req-span-test" {
		t.Errorf("request_id = %v, want req-span-test", m["request_id"])
	}

	// ② deny：rm -rf / 被策略拒绝 → span status=error、decision=denied、err 可读。
	buf.Reset()
	_, err := se.Run(ctx, "rm -rf /")
	if !errors.Is(err, ErrCommandDenied) {
		t.Fatalf("deny 命令应返回 ErrCommandDenied, got %v", err)
	}
	m2 := lastJSON(t, buf)
	if m2["status"] != "error" {
		t.Errorf("deny span status = %v, want error", m2["status"])
	}
	if m2["decision"] != "denied" {
		t.Errorf("deny span decision = %v, want denied", m2["decision"])
	}
	if s, _ := m2["err"].(string); !strings.Contains(s, "拒绝") {
		t.Errorf("deny span err = %v, want 含拒绝原因", m2["err"])
	}
	if m2["trace_id"] != rootTrace {
		t.Errorf("deny span trace_id = %v, want 继承根 trace", m2["trace_id"])
	}
	if m2["exit_code"] != nil {
		t.Error("被拒绝的命令不应有 exit_code")
	}
}

// TestSafeExecutor_TraceSpan_ParentChain 验证 span 嵌套：父 span（如 gateway.run）开启后，
// 其下命令执行的 executor.run span 会记录 parent_span_id，日志可按 parent_span_id 重建调用树。
func TestSafeExecutor_TraceSpan_ParentChain(t *testing.T) {
	buf, restore := captureTraceLogs(t)
	defer restore()

	inner, _ := NewHostExecutorWithTimeout(t.TempDir(), 5*time.Second)
	se := NewSafeExecutor(inner, NewDangerousCommandPolicy(ModeUnattended), NewMemoryAuditor(), nil, nil)

	parentCtx, endParent := obslog.StartSpan(context.Background(), "parent.op", "layer", "root")
	parentID := obslog.SpanFrom(parentCtx)
	if _, err := se.Run(parentCtx, "echo child"); err != nil {
		t.Fatalf("命令失败: %v", err)
	}
	child := lastJSON(t, buf)
	if child["parent_span_id"] != parentID {
		t.Errorf("executor.run parent_span_id = %v, want %v", child["parent_span_id"], parentID)
	}
	// 父子 trace 一致。
	endParent(nil)
	parent := lastJSON(t, buf)
	if parent["trace_id"] != child["trace_id"] {
		t.Errorf("父子 trace_id 不一致: %v vs %v", parent["trace_id"], child["trace_id"])
	}
}
