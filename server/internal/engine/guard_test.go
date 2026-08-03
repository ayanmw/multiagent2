package engine

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"

	codeagent "github.com/ayanmw/multiagent2/server/internal/agent"
)

// mockLoopServer 是一个「永远发起 tool_call」的 OpenAI 流式桩：每次请求都返回一段
// 文本 + 对 echo 工具的调用，迫使 Agent 反复迭代，从而触发护栏熔断（M1-13 验证用）。
// 不调用真实 LLM。
func mockLoopServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_, _ = io.ReadAll(r.Body) // 丢弃请求体，本桩不需要根据上下文决策
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		f, _ := w.(http.Flusher)
		writeSSETextThenToolCall(w, f, "PARTIAL_DRAFT", "echo", `{"message":"ping"}`)
	}))
}

// mockFinalServer 是一个「只回终帧文本、不再调工具」的桩，用于验证未被熔断的正常请求
// 不会误加熔断提示。
func mockFinalServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		f, _ := w.(http.Flusher)
		writeSSEText(w, f, "正常完成的答复。")
	}))
}

// writeSSETextThenToolCall 写出一个「同时带文本增量与 tool_call」的流式响应。
func writeSSETextThenToolCall(w http.ResponseWriter, f http.Flusher, text, name, args string) {
	for _, ch := range []string{
		fmt.Sprintf(`data: {"id":"t","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":%s,"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":%s,"arguments":%s}}]},"finish_reason":null}]}`, mustJSON(text), mustJSON(name), mustJSON(args)),
		`data: {"id":"t","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"content":""},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	} {
		fmt.Fprintf(w, "%s\n\n", ch)
		if f != nil {
			f.Flush()
		}
	}
}

// TestEngine_Guardrail_CircuitBreak_PartialResult 验证 M1-13 核心验收：
// 单代理模式下，模型陷入反复调工具的死循环时，护栏熔断（LLM 调用/工具迭代上限）触发，
// 运行优雅终止并返回「截至熔断前已产出的 partial 结果 + 熔断提示」，不产生 error / panic。
func TestEngine_Guardrail_CircuitBreak_PartialResult(t *testing.T) {
	srv := mockLoopServer(t)
	defer srv.Close()

	eng, err := New(ModelConfig{
		ModelID:  "mock-model",
		BaseURL:  srv.URL,
		APIKey:   "test-key",
		Protocol: "openai",
		// 单代理模式（默认 AGENT_MODE=single，也是 24h 循环的实际运行模式），
		// 收紧预算使死循环立即熔断。
		Guardrail: codeagent.GuardrailConfig{MaxLLMCalls: 1, MaxToolIterations: 1},
	})
	if err != nil {
		t.Fatalf("New engine failed: %v", err)
	}
	defer eng.Close()

	reply, err := eng.Chat(context.Background(), "sess-break", "请反复调用工具", nil)
	if err != nil {
		t.Fatalf("护栏熔断应优雅终止（err==nil），但收到 error: %v；reply=%q", err, reply)
	}
	// partial 结果：熔断前应已流式产出部分文本，不应被丢弃。
	if !strings.Contains(reply, "PARTIAL_DRAFT") {
		t.Fatalf("护栏熔断后未保留 partial 文本；reply=%q", reply)
	}
	// 运行级兜底提示：应附加明确的熔断说明。
	if !strings.Contains(reply, "[护栏熔断]") {
		t.Fatalf("护栏熔断后未追加熔断提示；reply=%q", reply)
	}
	t.Logf("✅ 护栏熔断优雅终止并产出 partial 结果：%q", reply)
}

// TestEngine_Guardrail_NoBreach_NormalCompletion 验证未被熔断的正常请求不会误加提示。
func TestEngine_Guardrail_NoBreach_NormalCompletion(t *testing.T) {
	srv := mockFinalServer(t)
	defer srv.Close()

	eng, err := New(ModelConfig{
		ModelID:  "mock-model",
		BaseURL:  srv.URL,
		APIKey:   "test-key",
		Protocol: "openai",
		// 默认预算（零值=启用默认上限），正常一轮答复不会触及上限。
		Guardrail: codeagent.GuardrailConfig{},
	})
	if err != nil {
		t.Fatalf("New engine failed: %v", err)
	}
	defer eng.Close()

	reply, err := eng.Chat(context.Background(), "sess-ok", "请直接回答", nil)
	if err != nil {
		t.Fatalf("正常请求不应报错: %v", err)
	}
	if !strings.Contains(reply, "正常完成的答复") {
		t.Fatalf("正常答复丢失；reply=%q", reply)
	}
	if strings.Contains(reply, "[护栏熔断]") {
		t.Fatalf("正常请求不应出现熔断提示；reply=%q", reply)
	}
	t.Logf("✅ 正常请求未被熔断误伤：%q", reply)
}

// TestIsCircuitBreakEvent 单测 IsCircuitBreakEvent 对护栏熔断事件 vs 普通错误的区分。
func TestIsCircuitBreakEvent(t *testing.T) {
	stopEv := event.NewErrorEvent("inv", "agent", agent.ErrorTypeStopAgentError, "max LLM calls (1) exceeded")
	toolEv := event.NewErrorEvent("inv", "agent", model.ErrorTypeFlowError, "max tool iterations (1) exceeded")
	normalEv := event.NewErrorEvent("inv", "agent", model.ErrorTypeFlowError, "模型调用失败：context deadline exceeded")

	if !IsCircuitBreakEvent(stopEv) {
		t.Fatalf("MaxLLMCalls 熔断事件应被识别为 circuit-break")
	}
	if !IsCircuitBreakEvent(toolEv) {
		t.Fatalf("MaxToolIterations 熔断事件应被识别为 circuit-break")
	}
	if IsCircuitBreakEvent(normalEv) {
		t.Fatalf("普通运行错误不应被误判为 circuit-break")
	}
	if IsCircuitBreakEvent(nil) {
		t.Fatalf("nil 事件不应判定为 circuit-break")
	}
	t.Log("✅ IsCircuitBreakEvent 正确区分护栏熔断与普通错误")
}
