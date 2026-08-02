package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"encoding/json"
	"io"

	codeagent "github.com/ayanmw/multiagent2/server/internal/agent"
)

// reviewerMock 是 M1-10 的脚本化 LLM 桩：驱动 Reviewer 依次
//
//	① 尝试调用 file_write（越权写入）→ 应被拒绝
//	② 改用 grep 检索      → 只读检索成功
//	③ 用 file_read 读文件 → 只读读取成功
//	④ 给出「需修改」结论
//
// 并记录 Reviewer 那次请求下发的工具清单与写入尝试被拒的原文，供断言使用。
type reviewerMock struct {
	mu sync.Mutex
	// toolNames 是 Reviewer 请求中携带的工具名（应恰好为 file_read + grep）。
	toolNames []string
	// writeRejection 是 Reviewer 调用 file_write 后收到的工具消息（应为拒绝信息）。
	writeRejection string
	// grepResult 是 grep 工具返回的检索结果。
	grepResult string
	// writeAttempts 记录 Reviewer 发起写入尝试的次数。
	writeAttempts int
}

const (
	reviewerTargetFile    = "hello.go"
	reviewerTargetContent = "package main\n\n// TODO: 补充单元测试\nfunc main() {}\n"
	reviewerSneakyFile    = "sneaky.txt"
	reviewerFinding       = "需修改\n1. hello.go:3 仍有未处理的 TODO（缺少单元测试）。"
)

// newReviewerMockServer 启动脚本化的 OpenAI 兼容流式端点。
func newReviewerMockServer(t *testing.T, mock *reviewerMock) *httptest.Server {
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
		case names["grep"]:
			// —— Reviewer（只读子代理）——
			mock.mu.Lock()
			if mock.toolNames == nil {
				mock.toolNames = sortedKeys(names)
			}
			step := countToolMessages(msgs)
			mock.mu.Unlock()

			switch step {
			case 0:
				// ① 越权尝试：Reviewer 试图写文件，框架应拒绝（工具未下发）。
				mock.mu.Lock()
				mock.writeAttempts++
				mock.mu.Unlock()
				writeSSEToolCall(w, f, "file_write",
					mustJSON(map[string]string{
						"path":    reviewerSneakyFile,
						"content": "reviewer 越权写入",
					}))
			case 1:
				// ② 记录拒绝原文，改走只读检索。
				mock.mu.Lock()
				mock.writeRejection = lastToolContent(msgs)
				mock.mu.Unlock()
				writeSSEToolCall(w, f, "grep", `{"pattern":"TODO"}`)
			case 2:
				mock.mu.Lock()
				mock.grepResult = lastToolContent(msgs)
				mock.mu.Unlock()
				writeSSEToolCall(w, f, "file_read",
					mustJSON(map[string]string{"path": reviewerTargetFile}))
			default:
				writeSSEText(w, f, reviewerFinding)
			}

		case names["reviewer"]:
			// —— Orchestrator：委托 Reviewer 审阅，拿到结论即收尾 ——
			if countToolMessages(msgs) == 0 {
				writeSSEToolCall(w, f, "reviewer",
					mustJSON(map[string]string{
						"request": "请审阅 " + reviewerTargetFile + " 是否存在未处理的 TODO",
					}))
				return
			}
			writeSSEText(w, f, "审阅完成，已收到 Reviewer 结论。")

		default:
			writeSSEText(w, f, "无可用工具，结束。")
		}
	}))
}

// lastToolContent 返回最后一条 role=tool 消息的文本内容。
func lastToolContent(msgs []map[string]any) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i]["role"] == "tool" {
			s, _ := msgs[i]["content"].(string)
			return s
		}
	}
	return ""
}

// TestEngine_ReviewerReadOnly_WriteDenied 验证 M1-10 验收标准「reviewer 调 write 被拒」：
//   - Reviewer 的工具清单恰为 file_read + grep，不含任何写/执行工具；
//   - Reviewer 主动调用 file_write 时被框架拒绝（返回 tool not found），文件未被创建；
//   - 被拒后 Reviewer 仍可用 grep/file_read 完成只读审阅并给出结论。
func TestEngine_ReviewerReadOnly_WriteDenied(t *testing.T) {
	workdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workdir, reviewerTargetFile),
		[]byte(reviewerTargetContent), 0o644); err != nil {
		t.Fatalf("准备被审阅文件失败: %v", err)
	}

	mock := &reviewerMock{}
	srv := newReviewerMockServer(t, mock)
	defer srv.Close()

	eng, err := New(ModelConfig{
		ModelID:  "mock-model",
		BaseURL:  srv.URL,
		APIKey:   "test-key",
		Protocol: "openai",
		Team: TeamConfig{
			EnableSubAgents: true,
			EnableReviewer:  true,
		},
		Workdir: workdir,
	})
	if err != nil {
		t.Fatalf("New engine with Reviewer failed: %v", err)
	}
	defer eng.Close()

	reply, err := eng.Chat(context.Background(), "sess-reviewer", "请审阅 hello.go", nil)
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}

	mock.mu.Lock()
	toolNames := mock.toolNames
	rejection := mock.writeRejection
	grepResult := mock.grepResult
	attempts := mock.writeAttempts
	mock.mu.Unlock()

	// 1) 工具清单：恰好 file_read + grep。
	if len(toolNames) == 0 {
		t.Fatalf("未捕获 Reviewer 的工具清单（reply=%q）", reply)
	}
	if got, want := strings.Join(toolNames, ","), "file_read,grep"; got != want {
		t.Fatalf("Reviewer 工具清单不符：got=%q want=%q", got, want)
	}

	// 2) 写入尝试被拒：收到拒绝性工具消息，且文件确实没有被创建。
	if attempts == 0 {
		t.Fatal("脚本未发起写入尝试，验收无效")
	}
	if rejection == "" {
		t.Fatal("Reviewer 调用 file_write 后未收到任何工具消息")
	}
	if !strings.Contains(strings.ToLower(rejection), "not found") &&
		!strings.Contains(strings.ToLower(rejection), "error") {
		t.Fatalf("写入尝试未被拒绝，返回内容=%q", rejection)
	}
	if _, err := os.Stat(filepath.Join(workdir, reviewerSneakyFile)); !os.IsNotExist(err) {
		t.Fatalf("Reviewer 越权写入成功了（%s 存在），只读约束失效", reviewerSneakyFile)
	}

	// 3) 只读能力仍然可用：grep 命中目标文件的 TODO。
	if !strings.Contains(grepResult, reviewerTargetFile) || !strings.Contains(grepResult, "TODO") {
		t.Fatalf("Reviewer 的 grep 检索结果异常：%q", grepResult)
	}

	t.Logf("✅ Reviewer 只读约束生效：工具=%v，写入被拒=%q，grep 结果=%q，最终回复=%q",
		toolNames, rejection, strings.SplitN(grepResult, "\n", 2)[0], reply)
}

// TestReadOnlyTools_NoMutatingToolLeak 从工厂层直接断言只读工具集的构成，
// 作为「reviewer 永远拿不到写/执行工具」的单元级护栏（与集成测试互补）。
func TestReadOnlyTools_NoMutatingToolLeak(t *testing.T) {
	workdir := t.TempDir()
	tools, err := codeagent.ReadOnlyTools(workdir)
	if err != nil {
		t.Fatalf("ReadOnlyTools: %v", err)
	}
	var names []string
	for _, tl := range tools {
		names = append(names, tl.Declaration().Name)
	}
	if got, want := strings.Join(names, ","), "file_read,grep"; got != want {
		t.Fatalf("只读工具集不符：got=%q want=%q", got, want)
	}
}
