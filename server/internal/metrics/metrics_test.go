package metrics

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestMetricsEnabledFlow(t *testing.T) {
	if err := Init(Config{Enabled: true}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	if !Enabled() {
		t.Fatal("expected Enabled()==true after Init")
	}

	ctx := context.Background()
	RecordLLMCall(ctx, "openai", "gpt-4", 120*time.Millisecond, nil)
	RecordLLMCall(ctx, "openai", "gpt-4", 80*time.Millisecond, nil)
	RecordLLMCall(ctx, "openai", "gpt-4", 200*time.Millisecond, context.DeadlineExceeded)
	RecordToolCall(ctx, "allowed", nil)
	RecordToolCall(ctx, "failed", context.Canceled)
	RecordTokenUsage(ctx, 100, 50, 150)

	// 原子累加器快照（前端概览数据源）
	s := Summary()
	if s.LLMCalls != 3 {
		t.Errorf("LLMCalls=%d want 3", s.LLMCalls)
	}
	if s.LLMErrors != 1 {
		t.Errorf("LLMErrors=%d want 1", s.LLMErrors)
	}
	if s.ToolCalls != 2 {
		t.Errorf("ToolCalls=%d want 2", s.ToolCalls)
	}
	if s.ToolErrors != 1 {
		t.Errorf("ToolErrors=%d want 1", s.ToolErrors)
	}
	if s.TokenPrompt != 100 || s.TokenCompletion != 50 || s.TokenTotal != 150 {
		t.Errorf("token summary mismatch: %+v", s)
	}

	// /metrics Prometheus 文本渲染
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatalf("Collect failed: %v", err)
	}
	out := renderPrometheus(&rm)
	for _, name := range []string{
		"codeagent_llm_calls_total",
		"codeagent_llm_call_duration_seconds",
		"codeagent_llm_errors_total",
		"codeagent_tool_calls_total",
		"codeagent_tool_errors_total",
		"codeagent_token_prompt_total",
		"codeagent_token_completion_total",
		"codeagent_token_total",
	} {
		if !strings.Contains(out, name) {
			t.Errorf("prometheus output missing metric %q:\n%s", name, out)
		}
	}
	// 计数器应为 counter 类型，且至少出现一次非 +Inf 桶
	if !strings.Contains(out, "# TYPE codeagent_llm_calls_total counter") {
		t.Errorf("llm_calls_total not typed as counter:\n%s", out)
	}
	if !strings.Contains(out, "codeagent_llm_calls_total{") {
		t.Errorf("llm_calls_total sample missing labels:\n%s", out)
	}

	// 通过 HTTP Handler 验证 Content-Type 与正文
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("handler status=%d want 200", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "text/plain") {
		t.Errorf("unexpected content-type: %q", w.Header().Get("Content-Type"))
	}
	if !strings.Contains(w.Body.String(), "codeagent_token_total") {
		t.Errorf("handler body missing token_total:\n%s", w.Body.String())
	}
}

// TestMetricsLoopBudgetAndProcessStart 验证 M7-04 新增的三类告警相关指标：
//   - codeagent_loop_runs_total / codeagent_loop_failures_total（自主 Loop 运行/失败）
//   - codeagent_budget_exhausted_total（平台级预算耗尽）
//   - process_start_time_seconds（进程启动时间，供「频繁重启」告警）
// 既出现在 /metrics 文本输出，也反映在 Summary 聚合快照中。
func TestMetricsLoopBudgetAndProcessStart(t *testing.T) {
	if err := Init(Config{Enabled: true}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	ctx := context.Background()
	RecordLoopRun(ctx)
	RecordLoopFailure(ctx)
	RecordBudgetExhausted(ctx)

	s := Summary()
	if s.LoopRuns != 1 {
		t.Errorf("LoopRuns=%d want 1", s.LoopRuns)
	}
	if s.LoopFailures != 1 {
		t.Errorf("LoopFailures=%d want 1", s.LoopFailures)
	}
	if s.BudgetExhausted != 1 {
		t.Errorf("BudgetExhausted=%d want 1", s.BudgetExhausted)
	}

	// /metrics 文本输出应包含新增计数器与进程启动时间 gauge。
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("handler status=%d want 200", w.Code)
	}
	body := w.Body.String()
	for _, name := range []string{
		"codeagent_loop_runs_total",
		"codeagent_loop_failures_total",
		"codeagent_budget_exhausted_total",
		"process_start_time_seconds",
	} {
		if !strings.Contains(body, name) {
			t.Errorf("prometheus output missing metric %q:\n%s", name, body)
		}
	}
	if !strings.Contains(body, "# TYPE process_start_time_seconds gauge") {
		t.Errorf("process_start_time_seconds not typed as gauge:\n%s", body)
	}
	// 进程启动时间应为合法的正数（> 1e9 即 2001 年之后的 Unix 秒）。
	// 用正则精确匹配「样本行」而非 HELP/TYPE 行（避免误匹配 HELP 行中的同名前缀）。
	re := regexp.MustCompile(`process_start_time_seconds ([0-9.eE+-]+)`)
	m := re.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("process_start_time_seconds sample line not found:\n%s", body)
	}
	var val float64
	if _, err := fmt.Sscanf(m[1], "%f", &val); err != nil || val < 1e9 {
		t.Errorf("process_start_time_seconds value invalid: %q", m[1])
	}
}

func TestDisabledNoopAnd404(t *testing.T) {
	if err := Init(Config{Enabled: false}); err != nil {
		t.Fatalf("Init(false) failed: %v", err)
	}
	if Enabled() {
		t.Fatal("expected Enabled()==false")
	}
	// 禁用时 Record* 为安全空操作：计数器不应发生任何变化（全进程共享的原子累加器，
	// 不在此重置，避免误清空运行期真实指标）。
	before := Summary()
	ctx := context.Background()
	RecordLLMCall(ctx, "p", "m", time.Second, nil)
	RecordToolCall(ctx, "allowed", nil)
	RecordTokenUsage(ctx, 1, 2, 3)
	after := Summary()
	if before != after {
		t.Errorf("disabled Record* must not change counters: before=%+v after=%+v", before, after)
	}
	if after.Enabled {
		t.Errorf("disabled Summary should report Enabled=false: %+v", after)
	}
	// /metrics 返回 404
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	Handler().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("disabled handler status=%d want 404", w.Code)
	}
}
