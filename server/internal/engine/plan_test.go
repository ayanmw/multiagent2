package engine

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	planpkg "github.com/ayanmw/multiagent2/server/internal/plan"
	codeagent "github.com/ayanmw/multiagent2/server/internal/agent"
)

// mockPlanServer 模拟 OpenAI 流式端点，脚本化驱动 M1-12 Plan-Execute 循环：
//   - 首轮调用 create_plan 建立 2 步计划；
//   - 计划未收敛时给过早 final → 被 PlanEnforcer 拦截；
//   - 被拦截注入催办后，调用 update_step 把当前步骤置 done；
//   - 全部步骤完成后给出最终答复 → 放行。
//
// 全程不依赖真实 LLM（见 docs/loop/LEARNINGS「M1 集成测试指引」）。
func mockPlanServer(t *testing.T) *httptest.Server {
	t.Helper()
	var s1Done, s2Done, created bool
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		// PlanEnforcer 的 BeforeModel 会在「计划未收敛」时关闭流式（见 plan.go），
		// 因此 mock 必须同时支持流式(SSE)与非流式(单对象 JSON)两种回包，
		// 否则非流式客户端会因无法解析 SSE 而报错（与真实上游行为一致）。
		wantStream, _ := req["stream"].(bool)

		msgs, _ := req["messages"].([]any)
		hasNudge := false
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
					case name == "create_plan":
						created = true
					case name == "update_step" && strings.Contains(args, `"status":"done"`):
						if strings.Contains(args, `"step_id":"s1"`) || strings.Contains(args, `"step_id":"1"`) {
							s1Done = true
						} else {
							s2Done = true
						}
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

		planClosed := s1Done && s2Done
		switch {
		case planClosed:
			// 计划已收敛：最终答复应被放行。
			textResp("FINAL_DONE：计划已全部完成，可收工。")
		case !created:
			// 首轮：建立 2 步计划。
			toolResp("create_plan",
				`{"title":"完成任务X","steps":[{"title":"写文件A"},{"title":"写文件B"}]}`)
		case hasNudge && !s1Done:
			// 被拦截注入催办后 → 完成步骤一。
			toolResp("update_step", `{"step_id":"s1","status":"done","note":"已写A"}`)
		case hasNudge && s1Done && !s2Done:
			// 被拦截注入催办后 → 完成步骤二。
			toolResp("update_step", `{"step_id":"s2","status":"done","note":"已写B"}`)
		default:
			// 计划未收敛且本轮未被拦截过 → 故意给过早 final，验证被拦截。
			textResp("PREMATURE_OPEN：我觉得做完了。")
		}
	}))
}

// TestEngine_PlanExecute_ForcesStepByStep 验证 M1-12 端到端验收：
// Orchestrator 在计划未执行完时给的最终答复被拦截，必须逐项做完才放行。
// 不调用真实 LLM。
func TestEngine_PlanExecute_ForcesStepByStep(t *testing.T) {
	workdir := t.TempDir()
	store := planpkg.NewStore(0)

	srv := mockPlanServer(t)
	defer srv.Close()

	eng, err := New(ModelConfig{
		ModelID:  "mock-model",
		BaseURL:  srv.URL,
		APIKey:   "test-key",
		Protocol: "openai",
		Team: codeagent.TeamConfig{
			EnableSubAgents: true,
			EnablePlan:      true,
			MaxPlanNudges:   5,
			PlanStore:       store,
		},
		Workdir: workdir,
	})
	if err != nil {
		t.Fatalf("New engine failed: %v", err)
	}
	defer eng.Close()

	reply, err := eng.Chat(context.Background(), "sess-plan", "请完成任务X（多步）", nil)
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}

	// 过早的 final 文本绝不能出现在最终回复里（证明被拦截、未泄漏）。
	for _, leaked := range []string{"PREMATURE_OPEN"} {
		if strings.Contains(reply, leaked) {
			t.Fatalf("过早的 final 答复泄漏到了最终回复（%q）；reply=%q", leaked, reply)
		}
	}
	if !strings.Contains(reply, "FINAL_DONE") {
		t.Fatalf("计划收敛后的最终答复未出现；reply=%q", reply)
	}

	// 计划确实走到了全收敛（而非卡死或停在 open）。
	p, perr := store.Get("sess:sess-plan")
	if perr != nil {
		t.Fatalf("计划未在 store 中落盘（key=sess:sess-plan）：%v；store.Len=%d", perr, store.Len())
	}
	if p.IsOpen() {
		t.Fatalf("计划未收敛，仍有未完成步骤：%s", p.Render())
	}
	c := p.Counts()
	if c.Done != 2 {
		t.Fatalf("期望 2 步都 done，got %+v", c)
	}
	t.Logf("✅ Plan-Execute 端到端验收通过：reply=%q, 最终步数=%d done", reply, c.Done)
}

// TestEngine_PlanExecute_DisabledWhenNoSubAgents 验证：未开子代理（单代理）时
// Plan-Execute 不生效，Agent 可直接给出最终答复（契约只装在 Orchestrator 上）。
func TestEngine_PlanExecute_DisabledWhenNoSubAgents(t *testing.T) {
	workdir := t.TempDir()

	// 单代理模式：始终返回一句最终答复，不涉及任何计划工具。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		wantStream, _ := req["stream"].(bool)
		if wantStream {
			writeSSEText(w, w.(http.Flusher), "SINGLE_DONE：单代理直接收工。")
		} else {
			writeJSONText(w, "SINGLE_DONE：单代理直接收工。")
		}
	}))
	defer srv.Close()

	// EnablePlan=true 但 EnableSubAgents=false → planEnabled()=false，扩展不装。
	eng, err := New(ModelConfig{
		ModelID:  "mock-model",
		BaseURL:  srv.URL,
		APIKey:   "test-key",
		Protocol: "openai",
		Team: codeagent.TeamConfig{
			EnableSubAgents: false,
			EnablePlan:      true,
		},
		Workdir: workdir,
	})
	if err != nil {
		t.Fatalf("New engine failed: %v", err)
	}
	defer eng.Close()

	reply, err := eng.Chat(context.Background(), "sess-plan-single", "请完成任务X", nil)
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}
	if !strings.Contains(reply, "SINGLE_DONE") {
		t.Fatalf("单代理（无 Plan 扩展）下不应被计划循环拦截；reply=%q", reply)
	}
	t.Logf("✅ 单代理模式 Plan-Execute 不生效：reply=%q", reply)
}
