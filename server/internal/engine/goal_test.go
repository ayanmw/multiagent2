package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	goalpkg "github.com/ayanmw/multiagent2/server/internal/goal"
	codeagent "github.com/ayanmw/multiagent2/server/internal/agent"
)

// mockGoalServer 模拟 OpenAI 流式端点，脚本化驱动 M1-11 目标契约：
//   - 首轮（未立目标）给出过早的 final 文本 → 应被 GoalEnforcer 拦截；
//   - 被拦截注入催办后，调用 create_goal 建立目标；
//   - 目标 in_progress 时再给一次过早 final → 仍应被拦截；
//   - 被拦截后调用 update_goal(status=complete) 收敛目标；
//   - 目标已 complete → 给出最终答复，应被放行。
//
// 全程不依赖真实 LLM（见 docs/loop/LEARNINGS「M1 集成测试指引」）。
func mockGoalServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		msgs, _ := req["messages"].([]any)
		// 目标契约的 BeforeModel 会在「未收敛目标」时关闭流式（见 goal.go），
		// 因此 mock 必须同时支持流式(SSE)与非流式(单对象 JSON)两种回包，
		// 否则非流式客户端会因无法解析 SSE 而报错（与真实上游行为一致）。
		wantStream, _ := req["stream"].(bool)

		hasNudge := false
		created := false
		completed := false
		for _, m := range msgs {
			mm, ok := m.(map[string]any)
			if !ok {
				continue
			}
			role, _ := mm["role"].(string)
			if role == "user" {
				if c, _ := mm["content"].(string); strings.Contains(c, "已被拦截") {
					hasNudge = true
				}
				continue
			}
			if role == "assistant" {
				for _, tc := range mm["tool_calls"].([]any) {
					tcm, ok := tc.(map[string]any)
					if !ok {
						continue
					}
					fn, ok := tcm["function"].(map[string]any)
					if !ok {
						continue
					}
					name, _ := fn["name"].(string)
					args, _ := fn["arguments"].(string)
					switch {
					case name == "create_goal":
						created = true
					case name == "update_goal" && strings.Contains(args, "complete"):
						completed = true
					}
				}
			}
		}

		textResp := func(text string) {
			if wantStream {
				writeSSEText(w, w.(http.Flusher), text)
			} else {
				writeJSONText(w, text)
			}
		}
		toolResp := func(name, args string) {
			if wantStream {
				writeSSEToolCall(w, w.(http.Flusher), name, args)
			} else {
				writeJSONToolCall(w, name, args)
			}
		}

		switch {
		case completed:
			// 目标已收敛：最终答复应被放行。
			textResp("FINAL_DONE：目标已达成，可收工。")
		case created && hasNudge:
			// 目标未收敛且已被拦截过 → 收敛目标。
			toolResp("update_goal", `{"status":"complete","progress":"已达成验收标准"}`)
		case created:
			// 目标 in_progress 但本轮未被拦截过 → 故意再给一次过早 final，验证仍被拦截。
			textResp("PREMATURE_OPEN：我觉得做完了。")
		case hasNudge:
			// 首轮被拦截注入催办后 → 建立目标。
			toolResp("create_goal",
				`{"title":"完成任务X","description":"M1-11 验收","acceptance_criteria":["达成"]}`)
		default:
			// 首轮（未立目标）直接收工 → 应被拦截。
			textResp("PREMATURE_EARLY：我先说做完了。")
		}
	}))
}

// writeJSONText 写出非流式（单对象 JSON）的纯文本响应（finish_reason=stop）。
func writeJSONText(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"id":"t","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":%s},"finish_reason":"stop"}]}`,
		mustJSON(text))
}

// writeJSONToolCall 写出非流式（单对象 JSON）的 tool_call 响应（finish_reason=tool_calls）。
func writeJSONToolCall(w http.ResponseWriter, name, args string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"id":"t","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":%s,"arguments":%s}}]},"finish_reason":"tool_calls"}]}`,
		mustJSON(name), mustJSON(args))
}

// TestEngine_GoalContract_BlocksPrematureFinal 验证 M1-11 端到端验收：
// Orchestrator 在目标未达成时给的最终答复被拦截，必须推进到 complete 才放行。
// 不调用真实 LLM。
func TestEngine_GoalContract_BlocksPrematureFinal(t *testing.T) {
	workdir := t.TempDir()
	store := goalpkg.NewStore(0)

	srv := mockGoalServer(t)
	defer srv.Close()

	eng, err := New(ModelConfig{
		ModelID:  "mock-model",
		BaseURL:  srv.URL,
		APIKey:   "test-key",
		Protocol: "openai",
		Team: codeagent.TeamConfig{
			EnableSubAgents: true,
			EnableGoal:      true,
			GoalStore:       store,
		},
		Workdir: workdir,
	})
	if err != nil {
		t.Fatalf("New engine with goal contract failed: %v", err)
	}
	defer eng.Close()

	reply, err := eng.Chat(context.Background(), "sess-goal", "请完成任务X", nil)
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}

	// 过早的 final 文本绝不能出现在最终回复里（证明被拦截、未泄漏）。
	for _, leaked := range []string{"PREMATURE_EARLY", "PREMATURE_OPEN"} {
		if strings.Contains(reply, leaked) {
			t.Fatalf("过早的 final 答复泄漏到了最终回复（%q）；reply=%q", leaked, reply)
		}
	}
	if !strings.Contains(reply, "FINAL_DONE") {
		t.Fatalf("目标收敛后的最终答复未出现；reply=%q", reply)
	}

	// 目标契约确实走到了 complete（而非卡死或停在 in_progress）。
	g, gerr := store.Get("sess:sess-goal")
	if gerr != nil {
		t.Fatalf("目标契约未在 store 中落盘（key=sess:sess-goal）：%v；store.Len=%d", gerr, store.Len())
	}
	if g.Status != goalpkg.StatusComplete {
		t.Fatalf("目标契约未收敛到 complete，当前状态=%q", g.Status)
	}
	t.Logf("✅ 目标契约端到端验收通过：reply=%q, 最终状态=%q", reply, g.Status)
}

// TestEngine_GoalContract_DisabledWhenNoSubAgents 验证：未开子代理（单代理）时
// 目标契约不生效，Agent 可直接给出最终答复（契约只装在 Orchestrator 上）。
func TestEngine_GoalContract_DisabledWhenNoSubAgents(t *testing.T) {
	workdir := t.TempDir()

	srv := mockGoalServer(t)
	defer srv.Close()

	// EnableGoal=true 但 EnableSubAgents=false → goalEnabled()=false，契约不装。
	eng, err := New(ModelConfig{
		ModelID:  "mock-model",
		BaseURL:  srv.URL,
		APIKey:   "test-key",
		Protocol: "openai",
		Team: codeagent.TeamConfig{
			EnableSubAgents: false,
			EnableGoal:      true,
		},
		Workdir: workdir,
	})
	if err != nil {
		t.Fatalf("New engine failed: %v", err)
	}
	defer eng.Close()

	reply, err := eng.Chat(context.Background(), "sess-goal-single", "请完成任务X", nil)
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}
	// 单代理无契约：首轮 PREMATURE_EARLY 不被拦截，直接成为最终答复。
	if !strings.Contains(reply, "PREMATURE_EARLY") {
		t.Fatalf("单代理（无契约）下过早答复应被直接放行；reply=%q", reply)
	}
	t.Logf("✅ 单代理模式目标契约不生效：reply=%q", reply)
}
