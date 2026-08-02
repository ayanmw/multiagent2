package engine

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

// teamMock 是 M1-09 的脚本化 LLM 桩，负责按「当前请求携带的工具清单」判定
// 是哪个角色在调用模型，并驱动一条完整的 CodeTeam 协作链：
//
//	Orchestrator → coder（写初版）→ reviewer（只读审阅，指出问题）
//	             → coder（按意见修复）→ Orchestrator 汇报
//
// 不依赖真实 LLM（见 docs/loop/PLAN.md「M1 集成测试指引」）。
type teamMock struct {
	mu sync.Mutex
	// reviewerToolNames 记录 Reviewer 那次请求下发的工具名，用于断言其只读性。
	reviewerToolNames []string
	// reviewerCalls / coderWrites 记录各角色被驱动的次数。
	reviewerCalls int
	coderWrites   int
	// reviewFinding 是 Reviewer 给出的问题结论，会被 Orchestrator 转交 Coder 修复。
	reviewFinding string
}

const (
	teamInitialContent = "hello from coder"
	teamFixedContent   = "hello from coder\n"
	teamReviewFinding  = "需修改\n1. hello.txt 缺少末尾换行符，建议补充。"
)

// newTeamMockServer 启动脚本化的 OpenAI 兼容流式端点。
func newTeamMockServer(t *testing.T, mock *teamMock) *httptest.Server {
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

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		f, _ := w.(http.Flusher)

		switch {
		case names["coder"]:
			// —— Orchestrator：按已收到的工具结果条数推进协作流程 ——
			switch countToolMessages(msgs) {
			case 0:
				writeSSEToolCall(w, f, "coder",
					`{"request":"请在工作目录创建 hello.txt，内容为 hello from coder"}`)
			case 1:
				if !names["reviewer"] {
					// Reviewer 未装配（团队配置未开启），直接收尾。
					writeSSEText(w, f, "Coder 已完成改动（未配置 Reviewer）。")
					return
				}
				writeSSEToolCall(w, f, "reviewer",
					`{"request":"请审阅 hello.txt 是否符合规范（文件必须以换行符结尾）"}`)
			case 2:
				mock.mu.Lock()
				finding := mock.reviewFinding
				mock.mu.Unlock()
				writeSSEToolCall(w, f, "coder",
					mustJSON(map[string]string{
						"request": "请根据 Reviewer 意见修复 hello.txt：" + finding,
					}))
			default:
				writeSSEText(w, f, "已完成：Coder 实现 → Reviewer 审阅指出问题 → Coder 修复。")
			}

		case names["file_write"]:
			// —— Coder：拿到工具结果就收尾，否则真正写文件 ——
			if lastRole(msgs) == "tool" {
				writeSSEText(w, f, "已完成文件改动。")
				return
			}
			content := teamInitialContent
			if strings.Contains(lastUserContent(msgs), "修复") {
				content = teamFixedContent
			}
			mock.mu.Lock()
			mock.coderWrites++
			mock.mu.Unlock()
			writeSSEToolCall(w, f, "file_write",
				mustJSON(map[string]string{"path": "hello.txt", "content": content}))

		case names["file_read"]:
			// —— Reviewer：只读审阅，先读文件再给结论 ——
			mock.mu.Lock()
			if mock.reviewerToolNames == nil {
				mock.reviewerToolNames = sortedKeys(names)
			}
			mock.mu.Unlock()
			if lastRole(msgs) == "tool" {
				mock.mu.Lock()
				mock.reviewerCalls++
				mock.reviewFinding = teamReviewFinding
				mock.mu.Unlock()
				writeSSEText(w, f, teamReviewFinding)
				return
			}
			writeSSEToolCall(w, f, "file_read", `{"path":"hello.txt"}`)

		default:
			writeSSEText(w, f, "无可用工具，结束。")
		}
	}))
}

// messagesOf 取出请求中的 messages 数组（每项为 map）。
func messagesOf(req map[string]any) []map[string]any {
	raw, _ := req["messages"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, m := range raw {
		if mm, ok := m.(map[string]any); ok {
			out = append(out, mm)
		}
	}
	return out
}

// toolNamesOf 汇总请求中下发的工具名集合。
func toolNamesOf(req map[string]any) map[string]bool {
	names := map[string]bool{}
	tools, _ := req["tools"].([]any)
	for _, tv := range tools {
		tt, ok := tv.(map[string]any)
		if !ok {
			continue
		}
		fn, ok := tt["function"].(map[string]any)
		if !ok {
			continue
		}
		if n, ok := fn["name"].(string); ok {
			names[n] = true
		}
	}
	return names
}

// countToolMessages 统计对话中 role=tool 的消息条数（= 已完成的工具调用轮次）。
func countToolMessages(msgs []map[string]any) int {
	n := 0
	for _, m := range msgs {
		if m["role"] == "tool" {
			n++
		}
	}
	return n
}

// lastRole 返回最后一条消息的角色。
func lastRole(msgs []map[string]any) string {
	if len(msgs) == 0 {
		return ""
	}
	role, _ := msgs[len(msgs)-1]["role"].(string)
	return role
}

// lastUserContent 返回最后一条 user 消息的文本内容。
func lastUserContent(msgs []map[string]any) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i]["role"] == "user" {
			s, _ := msgs[i]["content"].(string)
			return s
		}
	}
	return ""
}

// sortedKeys 返回集合的有序键列表（便于稳定断言与日志输出）。
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestEngine_CodeTeam_ReviewLoop 验证 M1-09 验收标准：
// 一轮对话内 Coder 产出代码 → Reviewer（只读）独立挑出问题 → 回环让 Coder 修复。
// 同时断言 Reviewer 的工具清单里没有任何写/执行工具（为 M1-10 打底）。
func TestEngine_CodeTeam_ReviewLoop(t *testing.T) {
	workdir := t.TempDir()
	mock := &teamMock{}
	srv := newTeamMockServer(t, mock)
	defer srv.Close()

	eng, err := New(ModelConfig{
		ModelID:  "mock-model",
		BaseURL:  srv.URL,
		APIKey:   "test-key",
		Protocol: "openai",
		Team: TeamConfig{
			EnableSubAgents: true,
			EnableReviewer:  true,
			MaxReviewRounds: 2,
		},
		Workdir: workdir,
	})
	if err != nil {
		t.Fatalf("New engine with CodeTeam failed: %v", err)
	}
	defer eng.Close()

	reply, err := eng.Chat(context.Background(), "sess-team", "请创建 hello.txt 并做代码审阅", nil)
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}

	// 1) Reviewer 确实参与并给出了问题结论。
	mock.mu.Lock()
	reviewerCalls, coderWrites := mock.reviewerCalls, mock.coderWrites
	reviewerTools, finding := mock.reviewerToolNames, mock.reviewFinding
	mock.mu.Unlock()

	if reviewerCalls == 0 {
		t.Fatalf("Reviewer 未被委托（reply=%q）", reply)
	}
	if !strings.Contains(finding, "需修改") {
		t.Fatalf("Reviewer 未指出问题：finding=%q", finding)
	}

	// 2) Reviewer 只读：工具清单不得包含写/执行类工具。
	if len(reviewerTools) == 0 {
		t.Fatal("未捕获 Reviewer 的工具清单")
	}
	for _, forbidden := range []string{"file_write", "file_edit", "shell_exec", "coder"} {
		for _, got := range reviewerTools {
			if got == forbidden {
				t.Fatalf("Reviewer 持有禁止的工具 %q（全部工具=%v）", forbidden, reviewerTools)
			}
		}
	}

	// 3) 回环生效：Coder 被驱动两次（初版 + 按 review 意见修复），文件为修复后内容。
	if coderWrites < 2 {
		t.Fatalf("回环未发生：Coder 写入次数=%d（期望 >=2）", coderWrites)
	}
	got, rerr := os.ReadFile(filepath.Join(workdir, "hello.txt"))
	if rerr != nil {
		t.Fatalf("Coder 未写入文件：%v（reply=%q）", rerr, reply)
	}
	if string(got) != teamFixedContent {
		t.Fatalf("修复后内容不符：got=%q want=%q", string(got), teamFixedContent)
	}

	t.Logf("✅ CodeTeam 回环完成：coder 写入 %d 次，reviewer 审阅 %d 次，reviewer 工具=%v，最终内容=%q，回复=%q",
		coderWrites, reviewerCalls, reviewerTools, string(got), reply)
}

// TestNewTeam_ReviewerDisabled 验证团队「配置化」：关闭 Reviewer 时不装配审阅者，
// 行为退回 M1-08 的 Orchestrator + Coder 二人组（file 仍由 Coder 写出）。
func TestEngine_CodeTeam_ReviewerDisabled(t *testing.T) {
	workdir := t.TempDir()
	mock := &teamMock{}
	srv := newTeamMockServer(t, mock)
	defer srv.Close()

	eng, err := New(ModelConfig{
		ModelID:  "mock-model",
		BaseURL:  srv.URL,
		APIKey:   "test-key",
		Protocol: "openai",
		Team:     TeamConfig{EnableSubAgents: true, EnableReviewer: false},
		Workdir:  workdir,
	})
	if err != nil {
		t.Fatalf("New engine failed: %v", err)
	}
	defer eng.Close()

	if _, err := eng.Chat(context.Background(), "sess-noreview", "请创建 hello.txt", nil); err != nil {
		t.Fatalf("Chat failed: %v", err)
	}

	mock.mu.Lock()
	reviewerCalls, coderWrites := mock.reviewerCalls, mock.coderWrites
	mock.mu.Unlock()

	if reviewerCalls != 0 {
		t.Fatalf("关闭 Reviewer 后仍被调用了 %d 次", reviewerCalls)
	}
	if coderWrites != 1 {
		t.Fatalf("Coder 写入次数=%d（期望 1）", coderWrites)
	}
	if _, err := os.ReadFile(filepath.Join(workdir, "hello.txt")); err != nil {
		t.Fatalf("Coder 未写入文件：%v", err)
	}
}
