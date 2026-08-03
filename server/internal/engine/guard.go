// guard.go 实现护栏熔断的「运行级兜底」（M1-13）。
//
// 配置层在 internal/agent/guard.go（GuardrailConfig → 框架选项 WithMaxLLMCalls /
// WithMaxToolIterations / WithToolCallRetryPolicy）。本文件处理「超限后如何优雅收尾」：
//
// 框架 v1.10.0 在两类超限时都会发射一个 IsError()==true 的错误事件（见 event/event.go）：
//   - MaxLLMCalls 超限：invocation.IncLLMCallCount 返回 StopError，消息形如
//     "max LLM calls (N) exceeded"（agent.ErrorTypeStopAgentError）；
//   - MaxToolIterations 超限：FunctionCallResponseProcessor 发射 model.ErrorTypeFlowError
//     事件，消息形如 "max tool iterations (N) exceeded"。
//
// 这二者本质都是「预算耗尽、本轮应提前结束」，是**优雅熔断**而非「运行失败」。但 SSE
// 转换器（sse.go）与方法 Chat 默认会把 IsError 事件当作运行错误：丢弃已产出的 partial
// 文本、下发泛化的 RUN_ERROR。故本文件提供：
//   - IsCircuitBreakEvent(ev)：把护栏熔断事件与普通错误区分开；
//   - CircuitBreakNotice()：追加在 partial 结果末尾的明确提示；
// 并在 engine.Chat 与 api 的 AG-UI 转换器中据此「保留 partial 文本 + 友好提示」，
// 满足 PLAN 验收「超限后优雅终止并产出 partial 结果」。
package engine

import (
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
)

// circuitBreakSentinels 是框架护栏熔断错误事件中文案片段（v1.10.0，大小写不敏感匹配）。
// 同时覆盖 MaxLLMCalls（StopError）与 MaxToolIterations（flow_error）两类触发。
var circuitBreakSentinels = []string{
	"max llm calls",
	"max tool iterations",
}

// IsCircuitBreakEvent reports whether the event signals a guardrail circuit break
// (budget exhausted) rather than a genuine runtime failure. Callers should preserve
// the partial text already streamed and surface a clear notice instead of dropping it.
func IsCircuitBreakEvent(ev *event.Event) bool {
	if ev == nil || ev.Response == nil {
		return false
	}
	// StopError 类型的 LLM 调用上限触发（agent.ErrorTypeStopAgentError）。
	if ev.Error != nil && ev.Error.Type == agent.ErrorTypeStopAgentError {
		return true
	}
	// 工具迭代上限走 model.ErrorTypeFlowError，二者消息均含上文案片段；
	// 用文案匹配覆盖所有护栏相关错误事件，更稳妥。
	msg := ""
	if ev.Response.Error != nil {
		msg = ev.Response.Error.Message
	}
	msgLower := strings.ToLower(msg)
	for _, s := range circuitBreakSentinels {
		if strings.Contains(msgLower, s) {
			return true
		}
	}
	return false
}

// CircuitBreakNotice 是护栏熔断后追加在 partial 结果末尾的提示（M1-13 运行级兜底），
// 告知用户本轮因预算耗尽提前结束、以上为截至熔断前已产出的部分结果。
func CircuitBreakNotice() string {
	return "\n\n[护栏熔断] 本轮已达到预算上限（LLM 调用次数 / 工具迭代轮数）。" +
		"以上内容为止熔断前已产出的部分结果；如任务未完成，可缩小范围或调整请求后重试。"
}
