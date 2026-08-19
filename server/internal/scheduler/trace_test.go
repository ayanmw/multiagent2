package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/obslog"
	"github.com/ayanmw/multiagent2/server/internal/repo"
)

// TestScheduler_RunAutomationTraceSpan 验证 M7-06「自主 Loop 根 span」：
// cron 触发的 runAutomation 会产出 automation.run 的 span.end JSON 日志
// （含 automation_name / status / attempts），成功与失败路径均覆盖。
func TestScheduler_RunAutomationTraceSpan(t *testing.T) {
	var buf bytes.Buffer
	if err := obslog.Init(obslog.Config{Format: obslog.FormatJSON, Level: slog.LevelDebug, Output: &buf}); err != nil {
		t.Fatal(err)
	}
	defer slog.SetDefault(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))

	db := newTestDB(t)
	base := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	a := &model.Automation{
		UserID: 1, Name: "trace-loop", TriggerType: model.AutomationTriggerCron,
		CronExpr: "*/1 * * * *", GoalPrompt: "do something", Enabled: true,
		NextRun: ptrTime(base.Add(-time.Minute)), // 已过期，立即触发
	}
	if err := repo.CreateAutomation(db, a); err != nil {
		t.Fatal(err)
	}
	mock := &mockRunner{}
	s := New(db, mock)
	s.Now = func() time.Time { return base }
	s.MaxRetries = 0
	if ran := s.TickSync(context.Background()); ran != 1 {
		t.Fatalf("应触发 1 个，ran=%d", ran)
	}

	// 成功路径：日志应出现 automation.run span.end，status=ok，带 automation_name。
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	found := false
	for _, ln := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			continue
		}
		if m["span_name"] != "automation.run" {
			continue
		}
		found = true
		if m["status"] != "ok" {
			t.Errorf("automation.run status = %v, want ok", m["status"])
		}
		if m["automation_name"] != "trace-loop" {
			t.Errorf("automation_name = %v, want trace-loop", m["automation_name"])
		}
		if m["trace_id"] == "" {
			t.Error("automation.run 应有自生成 trace_id（无父 ctx 时作为根 trace）")
		}
		if m["duration_ms"] == nil {
			t.Error("automation.run 应含 duration_ms")
		}
	}
	if !found {
		t.Fatalf("日志中未找到 automation.run span: %s", buf.String())
	}

	// 失败路径：status=error + err 字段。
	buf.Reset()
	mock.err = context.DeadlineExceeded
	// 重置 next_run 为过期再触发一次。
	got, gerr := repo.GetAutomationByID(db, 1, a.ID)
	if gerr != nil {
		t.Fatal(gerr)
	}
	past := base.Add(-time.Minute)
	got.NextRun = &past
	if uerr := repo.UpdateAutomation(db, got); uerr != nil {
		t.Fatal(uerr)
	}
	if ran := s.TickSync(context.Background()); ran != 1 {
		t.Fatalf("第二次应触发 1 个，ran=%d", ran)
	}
	lines2 := strings.Split(strings.TrimSpace(buf.String()), "\n")
	foundErr := false
	for _, ln := range lines2 {
		var m map[string]any
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			continue
		}
		if m["span_name"] != "automation.run" {
			continue
		}
		foundErr = true
		if m["status"] != "error" {
			t.Errorf("失败路径 automation.run status = %v, want error", m["status"])
		}
		if s, _ := m["err"].(string); s == "" {
			t.Error("失败路径 automation.run 应附 err 字段")
		}
	}
	if !foundErr {
		t.Fatalf("失败路径未找到 automation.run span: %s", buf.String())
	}
}
