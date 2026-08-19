package metrics

import (
	"os"
	"regexp"
	"testing"
)

// TestAlertRulesReferenceKnownMetrics 是 M7-04 的「指标 ↔ 告警规则」契约测试：
// 解析 monitoring/alert-rules.yml，抽取每条 expr 引用的指标名（含 _bucket/_sum/_count
// 派生后缀），剥离后缀后确认其基名确实由本包 /metrics 暴露（KnownMetricNames）。
// 这样能在不依赖 promtool 的沙箱环境下，防止「告警规则写了但指标不存在」的静默失效。
func TestAlertRulesReferenceKnownMetrics(t *testing.T) {
	candidates := []string{
		"../../../monitoring/alert-rules.yml",
		"../../../../monitoring/alert-rules.yml",
		"../../../../../monitoring/alert-rules.yml",
	}
	var data []byte
	var usedPath string
	for _, p := range candidates {
		if b, err := os.ReadFile(p); err == nil {
			data = b
			usedPath = p
			break
		}
	}
	if data == nil {
		t.Fatalf("找不到 monitoring/alert-rules.yml（尝试路径：%v）", candidates)
	}

	known := make(map[string]bool, len(KnownMetricNames))
	for _, n := range KnownMetricNames {
		known[n] = true
	}

	// 本系统的指标均以 codeagent_ 或 process_start_time_seconds 命名，据此精确抽取，
	// 避免误把 PromQL 函数/关键字当指标。
	metricRe := regexp.MustCompile(`codeagent_[A-Za-z0-9_]+|process_start_time_seconds`)
	// 派生后缀：histogram 会生成 _bucket/_sum/_count，需剥离后比对基名。
	suffixRe := regexp.MustCompile(`_(bucket|sum|count)$`)

	found := make(map[string]bool)
	for _, m := range metricRe.FindAllString(string(data), -1) {
		base := suffixRe.ReplaceAllString(m, "")
		found[base] = true
		if !known[base] {
			t.Errorf("alert-rules.yml 引用了未知指标 %q（基名 %q），不在 KnownMetricNames 中；请确认 /metrics 是否暴露该指标。", m, base)
		}
	}

	if len(found) == 0 {
		t.Fatalf("在 %s 中未抽取到任何指标名，可能规则文件为空或格式异常", usedPath)
	}

	// 反向校验：告警应至少覆盖 LLM 错误率、Loop 失败率、预算耗尽、进程重启、P99 时延，
	// 这些正是 M7-04 任务要求的关键信号。
	required := []string{
		"codeagent_llm_errors_total",
		"codeagent_loop_failures_total",
		"codeagent_budget_exhausted_total",
		"process_start_time_seconds",
		"codeagent_llm_call_duration_seconds",
	}
	for _, r := range required {
		if !found[r] {
			t.Errorf("告警规则缺少对关键指标 %q 的引用（任务要求覆盖该信号）", r)
		}
	}
}
