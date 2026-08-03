package engine

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	codeagent "github.com/ayanmw/multiagent2/server/internal/agent"
	goalpkg "github.com/ayanmw/multiagent2/server/internal/goal"
)

// m1FullChainMock 是 M1-17 集成验证用的脚本化 LLM 桩，在单条 engine.Chat 内
// 串起 M1 全部协作特性：Team（Orchestrator→Coder 写文件→Reviewer 只读审阅→Coder 修复）
// + Goal 契约（create_goal → 协作 → update_goal(complete)）。不依赖真实 LLM。
//
// 各角色由「请求携带的工具清单」区分（见 team_test.go 的 newTeamMockServer）：
//   - Orchestrator（根 Agent）携带 coder / reviewer / create_goal / update_goal 工具；
//   - Coder 携带 file_write 工具；
//   - Reviewer 携带 file_read 工具。
//
// 关键：Goal 契约的 BeforeModel 会在「目标未收敛」时关闭流式（见 goal.go），因此
// Orchestrator 的请求可能是非流式（单对象 JSON）；而 Coder/Reviewer 子代理请求通常
// 是流式（SSE）。为兼容两种情形，所有分支都通过 textResp/toolResp 闭包按请求
// 的 stream 字段自适应回包（与 goal_test.go 的 mockGoalServer 一致）。
type m1FullChainMock struct {
	mu            sync.Mutex
	reviewerCalls int
	coderWrites   int
	reviewerTools []string
	reviewFinding string
}

// newM1FullChainServer 启动脚本化的 OpenAI 兼容流式/非流式端点。
func newM1FullChainServer(t *testing.T, mock *m1FullChainMock) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		msgs := messagesOf(req)
		names := toolNamesOf(req)
		wantStream, _ := req["stream"].(bool)

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		f, _ := w.(http.Flusher)

		textResp := func(text string) {
			if wantStream {
				writeSSEText(w, f, text)
			} else {
				writeJSONText(w, text)
			}
		}
		toolResp := func(name, args string) {
			if wantStream {
				writeSSEToolCall(w, f, name, args)
			} else {
				writeJSONToolCall(w, name, args)
			}
		}

		switch {
		case names["coder"]:
			// —— Orchestrator（根 Agent）——
			// 按「已完成的工具结果条数」推进协作链：立目标 → 委托 Coder 写 →
			// 委托 Reviewer 审阅 → 委托 Coder 按意见修复 → 收敛目标 → 收工。
			switch countToolMessages(msgs) {
			case 0:
				toolResp("create_goal",
					`{"title":"创建并审阅 hello.txt","description":"M1-17 全链路验收","acceptance_criteria":["hello.txt 存在且以换行结尾"]}`)
			case 1:
				toolResp("coder", `{"request":"请在工作目录创建 hello.txt，内容为 hello from coder"}`)
			case 2:
				toolResp("reviewer", `{"request":"请审阅 hello.txt 是否符合规范（文件必须以换行符结尾）"}`)
			case 3:
				mock.mu.Lock()
				finding := mock.reviewFinding
				mock.mu.Unlock()
				toolResp("coder", mustJSON(map[string]string{
					"request": "请根据 Reviewer 意见修复 hello.txt：" + finding,
				}))
			case 4:
				toolResp("update_goal", `{"status":"complete","progress":"hello.txt 已创建并经审阅修复"}`)
			default:
				textResp("FINAL：已完成 hello.txt 的创建、审阅与修复，可收工。")
			}

		case names["file_write"]:
			// —— Coder 子代理 ——
			if lastRole(msgs) == "tool" {
				textResp("已完成文件改动。")
				return
			}
			content := teamInitialContent
			if strings.Contains(lastUserContent(msgs), "修复") {
				content = teamFixedContent
			}
			mock.mu.Lock()
			mock.coderWrites++
			mock.mu.Unlock()
			toolResp("file_write", mustJSON(map[string]string{"path": "hello.txt", "content": content}))

		case names["file_read"]:
			// —— Reviewer 子代理（只读）——
			mock.mu.Lock()
			if mock.reviewerTools == nil {
				mock.reviewerTools = sortedKeys(names)
			}
			mock.mu.Unlock()
			if lastRole(msgs) == "tool" {
				mock.mu.Lock()
				mock.reviewerCalls++
				mock.reviewFinding = teamReviewFinding
				mock.mu.Unlock()
				textResp(teamReviewFinding)
				return
			}
			toolResp("file_read", `{"path":"hello.txt"}`)

		default:
			textResp("无可用工具，结束。")
		}
	}))
}

// TestEngine_M1_FullChain_TeamGoalComplete 是 M1-17 在引擎层的集成验证：
// 一条 engine.Chat 内同时验证 M1-04..16 的协同成果——
//   - Team 编排：Orchestrator 委托 Coder 写文件、Reviewer 只读审阅指出问题、Coder 修复；
//   - Reviewer 严格只读（工具清单不含 file_write/file_edit/shell_exec/coder）；
//   - Goal 契约：目标被推进到 complete 才结束（而非卡死或泄漏过早 final）；
//   - 最终 hello.txt 落盘为修复后内容。
//
// Plan-Execute（M1-12）有独立的引擎端到端测试 TestEngine_PlanExecute_ForcesStepByStep，
// 本用例聚焦「Coder/Reviewer 协同改文件 + Goal 循环到 complete」这条最关键的协作链。
// 全程不调用真实 LLM。
func TestEngine_M1_FullChain_TeamGoalComplete(t *testing.T) {
	workdir := t.TempDir()
	mock := &m1FullChainMock{}
	store := goalpkg.NewStore(0)

	srv := newM1FullChainServer(t, mock)
	defer srv.Close()

	eng, err := New(ModelConfig{
		ModelID:  "mock-model",
		BaseURL:  srv.URL,
		APIKey:   "test-key",
		Protocol: "openai",
		Team: codeagent.TeamConfig{
			EnableSubAgents: true,
			EnableReviewer:  true,
			MaxReviewRounds: 2,
			EnableGoal:      true,
			GoalStore:       store,
		},
		Workdir: workdir,
	})
	if err != nil {
		t.Fatalf("New engine (M1 full chain) failed: %v", err)
	}
	defer eng.Close()

	reply, err := eng.Chat(context.Background(), "sess-m1", "请创建并审阅 hello.txt", nil)
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}

	mock.mu.Lock()
	reviewerCalls, coderWrites := mock.reviewerCalls, mock.coderWrites
	reviewerTools, finding := mock.reviewerTools, mock.reviewFinding
	mock.mu.Unlock()

	// 1) Reviewer 确实参与并给出「需修改」结论（Coder/Reviewer 协同）。
	if reviewerCalls == 0 {
		t.Fatalf("Reviewer 未被委托；reply=%q", reply)
	}
	if !strings.Contains(finding, "需修改") {
		t.Fatalf("Reviewer 未指出问题；finding=%q", finding)
	}

	// 2) Reviewer 只读性：工具清单不得含任何写/执行类工具（M1-10 硬护栏）。
	for _, forbidden := range []string{"file_write", "file_edit", "shell_exec", "coder"} {
		for _, got := range reviewerTools {
			if got == forbidden {
				t.Fatalf("Reviewer 持有禁止的工具 %q；全部工具=%v", forbidden, reviewerTools)
			}
		}
	}

	// 3) 回环生效：Coder 被驱动两次（初版 + 按 review 意见修复），最终文件为修复后内容。
	if coderWrites < 2 {
		t.Fatalf("回环未发生：Coder 写入次数=%d（期望 >=2）；reply=%q", coderWrites, reply)
	}
	got, rerr := os.ReadFile(filepath.Join(workdir, "hello.txt"))
	if rerr != nil {
		t.Fatalf("Coder 未写入文件：%v；reply=%q", rerr, reply)
	}
	if string(got) != teamFixedContent {
		t.Fatalf("修复后内容不符：got=%q want=%q；reply=%q", string(got), teamFixedContent, reply)
	}

	// 4) Goal 契约收敛到 complete（不是卡死在 in_progress，也不是泄漏过早 final）。
	for _, leaked := range []string{"PREMATURE_EARLY", "PREMATURE_OPEN"} {
		if strings.Contains(reply, leaked) {
			t.Fatalf("过早的 final 答复泄漏到了最终回复（%q）；reply=%q", leaked, reply)
		}
	}
	if !strings.Contains(reply, "FINAL") {
		t.Fatalf("目标收敛后的最终答复未出现；reply=%q", reply)
	}
	g, gerr := store.Get("sess:sess-m1")
	if gerr != nil {
		t.Fatalf("目标契约未在 store 中落盘（key=sess:sess-m1）：%v", gerr)
	}
	if g.Status != goalpkg.StatusComplete {
		t.Fatalf("目标契约未收敛到 complete，当前状态=%q", g.Status)
	}

	t.Logf("✅ M1 引擎全链路验收：coder 写 %d 次、reviewer 审阅 %d 次、reviewer 工具=%v、最终内容=%q、目标=%q、reply=%q",
		coderWrites, reviewerCalls, reviewerTools, string(got), g.Status, reply)
}
