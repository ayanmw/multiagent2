// Package metrics 实现 M3-09「可观测性 telemetry」：基于 OpenTelemetry SDK（已随
// trpc-agent-go 间接引入，版本 v1.29.0）定义并采集运行期指标，对外暴露两类接口：
//
//  1. /metrics（Prometheus 文本格式）：供 Prometheus / Grafana 等抓取，本包内置一个
//     轻量 Prometheus exposition 渲染器，避免引入额外的 prometheus client 依赖
//     （沙箱无法访问 GitHub VCS，go.opentelemetry.io/otel/exporter/prometheus 不可得）。
//  2. Summary()：返回当前进程内聚合的指标快照（JSON），供前端「运行监控」概览卡片使用。
//
// 采集的指标：
//   - codeagent_llm_calls_total        LLM 调用总数（标签 provider / model）
//   - codeagent_llm_call_duration_seconds  LLM 调用时延分布（直方图，标签同上）
//   - codeagent_llm_errors_total       LLM 调用失败数（标签 provider / model）
//   - codeagent_tool_calls_total       工具（代码执行）调用总数
//   - codeagent_tool_errors_total      工具（代码执行）调用失败数（标签 reason）
//   - codeagent_token_prompt_total     提示 token 累计
//   - codeagent_token_completion_total 补全 token 累计
//   - codeagent_token_total            总 token 累计
//
// 所有 Record* 函数在未启用（Init 未调用 / METRICS_ENABLED=false）时为安全空操作，
// 不会因指标系统未初始化而产生 panic。
package metrics

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// Config 控制 metrics 子系统是否启用。
type Config struct {
	// Enabled 为 true 时初始化 OpenTelemetry MeterProvider 并开放 /metrics。
	Enabled bool
}

// Overview 是供前端「运行监控」概览卡片消费的聚合快照。
type Overview struct {
	Enabled         bool  `json:"enabled"`
	LLMCalls        int64 `json:"llm_calls"`
	LLMErrors       int64 `json:"llm_errors"`
	ToolCalls       int64 `json:"tool_calls"`
	ToolErrors      int64 `json:"tool_errors"`
	TokenPrompt     int64 `json:"token_prompt"`
	TokenCompletion int64 `json:"token_completion"`
	TokenTotal      int64 `json:"token_total"`
}

// 进程内原子累加器，作为 Summary() 的唯一数据源（与 OpenTelemetry instruments 同源：
// 每次 Record* 时同时写入 OTel instrument 与这里的原子计数器）。避免从 metricdata 反解。
var (
	enabled         int32 // 0=false, 1=true
	llmCalls        atomic.Int64
	llmErrors       atomic.Int64
	toolCalls       atomic.Int64
	toolErrors      atomic.Int64
	tokenPrompt     atomic.Int64
	tokenCompletion atomic.Int64
	tokenTotal      atomic.Int64
)

// OpenTelemetry instruments（启用时非 nil）。
var (
	meter                  metric.Meter
	llmCallsCounter        metric.Int64Counter
	llmDurationHist        metric.Float64Histogram
	llmErrorsCounter       metric.Int64Counter
	toolCallsCounter       metric.Int64Counter
	toolErrorsCounter      metric.Int64Counter
	tokenPromptCounter     metric.Int64Counter
	tokenCompletionCounter metric.Int64Counter
	tokenTotalCounter      metric.Int64Counter
)

// reader 是 ManualReader 实例，用于 /metrics 抓取时即时聚合当前指标快照。
var reader *sdkmetric.ManualReader

// Init 初始化 metrics 子系统。Enabled=false 时仅标记禁用并直接返回（Record* 均为空操作）。
func Init(cfg Config) error {
	if !cfg.Enabled {
		atomic.StoreInt32(&enabled, 0)
		return nil
	}

	r := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(r))
	m := mp.Meter("codeagent")

	var err error
	if llmCallsCounter, err = m.Int64Counter("codeagent_llm_calls_total", metric.WithDescription("LLM 调用总数")); err != nil {
		return err
	}
	if llmDurationHist, err = m.Float64Histogram("codeagent_llm_call_duration_seconds", metric.WithDescription("LLM 调用时延(秒)"), metric.WithUnit("s")); err != nil {
		return err
	}
	if llmErrorsCounter, err = m.Int64Counter("codeagent_llm_errors_total", metric.WithDescription("LLM 调用失败数")); err != nil {
		return err
	}
	if toolCallsCounter, err = m.Int64Counter("codeagent_tool_calls_total", metric.WithDescription("工具(代码执行)调用总数")); err != nil {
		return err
	}
	if toolErrorsCounter, err = m.Int64Counter("codeagent_tool_errors_total", metric.WithDescription("工具(代码执行)调用失败数")); err != nil {
		return err
	}
	if tokenPromptCounter, err = m.Int64Counter("codeagent_token_prompt_total", metric.WithDescription("提示 token 累计")); err != nil {
		return err
	}
	if tokenCompletionCounter, err = m.Int64Counter("codeagent_token_completion_total", metric.WithDescription("补全 token 累计")); err != nil {
		return err
	}
	if tokenTotalCounter, err = m.Int64Counter("codeagent_token_total", metric.WithDescription("总 token 累计")); err != nil {
		return err
	}

	reader = r
	meter = m
	atomic.StoreInt32(&enabled, 1)
	return nil
}

// Enabled 报告 metrics 子系统当前是否启用。
func Enabled() bool { return atomic.LoadInt32(&enabled) == 1 }

func instrumentAttrs(provider, modelName string) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("provider", provider),
		attribute.String("model", modelName),
	}
}

// RecordLLMCall 在每次 LLM 调用结束后记录调用数、时延与（若有）错误。
// provider / model 用于维度下钻；duration 为本次调用耗时；callErr 非 nil 时累加错误计数。
func RecordLLMCall(ctx context.Context, provider, modelName string, duration time.Duration, callErr error) {
	if !Enabled() {
		return
	}
	attrs := instrumentAttrs(provider, modelName)
	llmCallsCounter.Add(ctx, 1, metric.WithAttributes(attrs...))
	llmDurationHist.Record(ctx, duration.Seconds(), metric.WithAttributes(attrs...))
	if callErr != nil {
		llmErrorsCounter.Add(ctx, 1, metric.WithAttributes(attrs...))
	}

	llmCalls.Add(1)
	if callErr != nil {
		llmErrors.Add(1)
	}
}

// RecordToolCall 在每次工具（代码执行）调用结束后记录调用数与（若有）失败数。
// reason 描述失败原因（allowed/denied/failed/checkpoint），仅当 callErr 非 nil 时计入失败。
func RecordToolCall(ctx context.Context, reason string, callErr error) {
	if !Enabled() {
		return
	}
	toolCallsCounter.Add(ctx, 1)
	if callErr != nil {
		toolErrorsCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", reason)))
	}

	toolCalls.Add(1)
	if callErr != nil {
		toolErrors.Add(1)
	}
}

// RecordTokenUsage 在每次对话结束后记录 token 用量（提示 / 补全 / 总）。
func RecordTokenUsage(ctx context.Context, prompt, completion, total int64) {
	if !Enabled() {
		return
	}
	if prompt > 0 {
		tokenPromptCounter.Add(ctx, prompt)
	}
	if completion > 0 {
		tokenCompletionCounter.Add(ctx, completion)
	}
	if total > 0 {
		tokenTotalCounter.Add(ctx, total)
	}

	tokenPrompt.Add(prompt)
	tokenCompletion.Add(completion)
	tokenTotal.Add(total)
}

// Summary 返回当前进程内的指标聚合快照，供前端「运行监控」概览卡片消费。
func Summary() Overview {
	return Overview{
		Enabled:         Enabled(),
		LLMCalls:        llmCalls.Load(),
		LLMErrors:       llmErrors.Load(),
		ToolCalls:       toolCalls.Load(),
		ToolErrors:      toolErrors.Load(),
		TokenPrompt:     tokenPrompt.Load(),
		TokenCompletion: tokenCompletion.Load(),
		TokenTotal:      tokenTotal.Load(),
	}
}

// Handler 返回 /metrics 的 http.Handler，按 Prometheus 文本格式即时聚合并渲染当前指标。
// 未启用时返回 404，避免暴露空指标端点误导抓取。
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !Enabled() || reader == nil {
			http.Error(w, "metrics disabled", http.StatusNotFound)
			return
		}
		var rm metricdata.ResourceMetrics
		if err := reader.Collect(r.Context(), &rm); err != nil {
			http.Error(w, "collect metrics failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(renderPrometheus(&rm)))
	})
}

// renderPrometheus 把 OpenTelemetry 聚合结果转换为 Prometheus exposition 文本格式。
func renderPrometheus(rm *metricdata.ResourceMetrics) string {
	var b strings.Builder
	for _, sm := range rm.ScopeMetrics {
		for i := range sm.Metrics {
			writeMetric(&b, &sm.Metrics[i])
		}
	}
	return b.String()
}

func writeMetric(b *strings.Builder, m *metricdata.Metrics) {
	switch d := m.Data.(type) {
	case metricdata.Sum[int64]:
		typ := "gauge"
		if d.IsMonotonic {
			typ = "counter"
		}
		writeHelpType(b, m.Name, m.Description, typ)
		for _, dp := range d.DataPoints {
			writeSample(b, m.Name, dp.Attributes, strconv.FormatInt(dp.Value, 10))
		}
	case metricdata.Sum[float64]:
		typ := "gauge"
		if d.IsMonotonic {
			typ = "counter"
		}
		writeHelpType(b, m.Name, m.Description, typ)
		for _, dp := range d.DataPoints {
			writeSample(b, m.Name, dp.Attributes, strconv.FormatFloat(dp.Value, 'g', -1, 64))
		}
	case metricdata.Histogram[int64]:
		writeHelpType(b, m.Name, m.Description, "histogram")
		for _, dp := range d.DataPoints {
			writeHistogram(b, m.Name, dp)
		}
	case metricdata.Histogram[float64]:
		writeHelpType(b, m.Name, m.Description, "histogram")
		for _, dp := range d.DataPoints {
			writeHistogram(b, m.Name, dp)
		}
	}
}

func writeHelpType(b *strings.Builder, name, desc, typ string) {
	if desc != "" {
		b.WriteString("# HELP ")
		b.WriteString(name)
		b.WriteByte(' ')
		b.WriteString(desc)
		b.WriteByte('\n')
	}
	b.WriteString("# TYPE ")
	b.WriteString(name)
	b.WriteByte(' ')
	b.WriteString(typ)
	b.WriteByte('\n')
}

// writeSample 写出一个无额外标签的 sample（指标名 + 属性 + 值）。
func writeSample(b *strings.Builder, name string, attrs attribute.Set, value string) {
	b.WriteString(name)
	b.WriteByte('{')
	b.WriteString(renderLabels(attrs))
	b.WriteByte('}')
	b.WriteByte(' ')
	b.WriteString(value)
	b.WriteByte('\n')
}

// writeSampleLE 写出一个带 le 桶边界的 histogram bucket sample。
func writeSampleLE(b *strings.Builder, name, labels, le, value string) {
	b.WriteString(name)
	b.WriteByte('{')
	if labels != "" {
		b.WriteString(labels)
		b.WriteByte(',')
	}
	b.WriteString("le=\"")
	b.WriteString(le)
	b.WriteString("\"} ")
	b.WriteString(value)
	b.WriteByte('\n')
}

// writeHistogram 渲染单个 histogram 数据点的 _bucket / _sum / _count。
func writeHistogram[N int64 | float64](b *strings.Builder, name string, dp metricdata.HistogramDataPoint[N]) {
	labels := renderLabels(dp.Attributes)
	cum := uint64(0)
	for i, bound := range dp.Bounds {
		cum += dp.BucketCounts[i]
		writeSampleLE(b, name+"_bucket", labels, formatFloat(float64(bound)), strconv.FormatUint(cum, 10))
	}
	// +Inf 桶：累计计数等于总 Count。
	writeSampleLE(b, name+"_bucket", labels, "+Inf", strconv.FormatUint(dp.Count, 10))
	writeSample(b, name+"_sum", dp.Attributes, formatFloat(float64(dp.Sum)))
	writeSample(b, name+"_count", dp.Attributes, strconv.FormatUint(dp.Count, 10))
}

// renderLabels 把 attribute.Set 渲染为 Prometheus 标签串（k="v",k2="v2"），无属性时返回空串。
func renderLabels(attrs attribute.Set) string {
	kvs := attrs.ToSlice()
	if len(kvs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(kvs))
	for _, kv := range kvs {
		parts = append(parts, fmt.Sprintf("%s=\"%s\"", sanitizeLabelName(string(kv.Key)), escapeLabelValue(kv.Value.Emit())))
	}
	return strings.Join(parts, ",")
}

// sanitizeLabelName 将任意字符串规整为合法 Prometheus 标签名（[a-zA-Z_][a-zA-Z0-9_]*）。
func sanitizeLabelName(s string) string {
	var b strings.Builder
	for i, r := range s {
		switch {
		case r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			if i == 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "_"
	}
	return b.String()
}

// escapeLabelValue 转义 Prometheus 标签值中的 \ " 与换行。
func escapeLabelValue(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}

// formatFloat 以最短可解析形式输出浮点数（Inf 走 +Inf 已在调用处特判，这里不处理）。
func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}
