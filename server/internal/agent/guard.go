// guard.go 实现护栏熔断的「配置层」（M1-13）。
//
// 背景：自主推进的 Agent 会自己决定「再想一轮 / 再调一次工具」，一旦模型陷入
// 死循环（反复调同一个工具、反复自问自答），既会烧掉预算，也会把无人值守的
// 24h 循环卡死。故 M1 阶段就必须把三件套护栏配好（docs/03 §2.2 M1-13）：
//
//	MaxLLMCalls        —— 单次 invocation 内最多允许多少次 LLM 调用；
//	MaxToolIterations  —— 单次 invocation 内最多允许多少轮工具调用；
//	MaxToolRetries     —— 单个工具调用失败后最多重试几次（含退避）。
//
// 框架能力（v1.10.0 已内置，无需自实现）：
//   - llmagent.WithMaxLLMCalls(n)      → invocation.IncLLMCallCount 超限抛 StopError，
//     llmflow 捕获后发一条 stop_agent_error 事件并优雅结束本轮；
//   - llmagent.WithMaxToolIterations(n) → FunctionCallResponseProcessor 在超限时置
//     EndInvocation=true 并发一条 flow_error 事件，不再执行任何工具；
//   - llmagent.WithToolCallRetryPolicy(*tool.RetryPolicy) → 单个工具调用的重试与退避
//     （RetryOn 为空时用框架 tool.DefaultRetryOn：仅对网络类瞬时错误重试）。
//
// 本文件只负责「配置 → 框架选项」的收敛（业务层不直连框架）；
// 「超限后如何优雅收尾并产出 partial 结果」的运行级兜底在 internal/engine/guard.go。
package codeagent

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// 护栏默认预算。取值原则：足够完成一个中型编码任务（建计划→委托 Coder→审阅→修复），
// 又能在模型陷入死循环时于可接受的时间/费用内熔断。
const (
	// DefaultMaxLLMCalls 是单次 invocation 默认的 LLM 调用次数上限。
	DefaultMaxLLMCalls = 32
	// DefaultMaxToolIterations 是单次 invocation 默认的工具迭代轮数上限。
	DefaultMaxToolIterations = 16
	// DefaultMaxToolRetries 是单个工具调用失败后默认的重试次数（不含首次尝试）。
	DefaultMaxToolRetries = 2
	// DefaultToolRetryInitialInterval 是首次重试前的等待时间。
	DefaultToolRetryInitialInterval = 200 * time.Millisecond
	// DefaultToolRetryBackoffFactor 是重试退避的增长倍数。
	DefaultToolRetryBackoffFactor = 2.0
	// DefaultToolRetryMaxInterval 是重试退避的单次等待上限。
	DefaultToolRetryMaxInterval = 5 * time.Second
)

// GuardrailConfig 描述一次运行的护栏预算（M1-13）。
//
// 零值即「按默认预算启用护栏」——这是刻意设计：无人值守场景下忘记配置也必须有兜底，
// 与 executor 的「无人值守默认 deny」同一思路（见 LEARNINGS 2026-07-29 危险命令策略）。
// 只有显式 Disabled=true 才会完全解除限制（仅建议在本地调试时使用）。
type GuardrailConfig struct {
	// Disabled 显式关闭护栏（不下发任何上限，等价于框架默认的「无限制」）。
	// 生产/无人值守场景禁止开启。
	Disabled bool
	// MaxLLMCalls 是单次 invocation 的 LLM 调用次数上限；<=0 时取 DefaultMaxLLMCalls。
	MaxLLMCalls int
	// MaxToolIterations 是单次 invocation 的工具迭代轮数上限；<=0 时取 DefaultMaxToolIterations。
	MaxToolIterations int
	// MaxToolRetries 是单个工具调用失败后的重试次数（不含首次尝试）；
	// <0 时取 DefaultMaxToolRetries，=0 表示不重试。
	MaxToolRetries int
	// RetryInitialInterval / RetryBackoffFactor / RetryMaxInterval 控制重试退避；
	// 取非法值时回落到默认。
	RetryInitialInterval time.Duration
	RetryBackoffFactor   float64
	RetryMaxInterval     time.Duration
}

// Normalized 返回补齐默认值后的副本（幂等）。
func (g GuardrailConfig) Normalized() GuardrailConfig {
	if g.MaxLLMCalls <= 0 {
		g.MaxLLMCalls = DefaultMaxLLMCalls
	}
	if g.MaxToolIterations <= 0 {
		g.MaxToolIterations = DefaultMaxToolIterations
	}
	if g.MaxToolRetries < 0 {
		g.MaxToolRetries = DefaultMaxToolRetries
	}
	if g.RetryInitialInterval <= 0 {
		g.RetryInitialInterval = DefaultToolRetryInitialInterval
	}
	if g.RetryBackoffFactor <= 1 {
		g.RetryBackoffFactor = DefaultToolRetryBackoffFactor
	}
	if g.RetryMaxInterval <= 0 {
		g.RetryMaxInterval = DefaultToolRetryMaxInterval
	}
	return g
}

// Enabled 报告护栏是否生效。
func (g GuardrailConfig) Enabled() bool {
	return !g.Disabled
}

// RetryPolicy 把重试预算映射为框架的工具调用重试策略。
// MaxToolRetries<=0（不重试）或护栏被关闭时返回 nil（框架按「只尝试一次」处理）。
//
// RetryOn 留空：框架会回落到 tool.DefaultRetryOn —— 只对 EOF / 网络超时等
// 瞬时错误重试，业务级错误（如「命令被安全策略拒绝」）不会被无意义地重放。
func (g GuardrailConfig) RetryPolicy() *tool.RetryPolicy {
	if !g.Enabled() {
		return nil
	}
	n := g.Normalized()
	if n.MaxToolRetries <= 0 {
		return nil
	}
	return &tool.RetryPolicy{
		MaxAttempts:     n.MaxToolRetries + 1, // 框架语义：含首次尝试的总次数
		InitialInterval: n.RetryInitialInterval,
		BackoffFactor:   n.RetryBackoffFactor,
		MaxInterval:     n.RetryMaxInterval,
	}
}

// Options 返回应叠加到 llmagent 上的护栏选项（M1-13）。
// 护栏被关闭时返回 nil，调用方可直接 append。
func (g GuardrailConfig) Options() []llmagent.Option {
	if !g.Enabled() {
		return nil
	}
	n := g.Normalized()
	opts := []llmagent.Option{
		llmagent.WithMaxLLMCalls(n.MaxLLMCalls),
		llmagent.WithMaxToolIterations(n.MaxToolIterations),
	}
	if rp := n.RetryPolicy(); rp != nil {
		opts = append(opts, llmagent.WithToolCallRetryPolicy(rp))
	}
	return opts
}
