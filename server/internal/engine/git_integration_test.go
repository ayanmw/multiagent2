package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	codectool "github.com/ayanmw/multiagent2/server/internal/tool"
	"github.com/ayanmw/multiagent2/server/internal/executor"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// mockGitServer 模拟 OpenAI 流式端点，脚本化驱动「Orchestrator→Coder→(file_write→git_commit→git_status)」链：
//   - Orchestrator（含 coder 工具）首轮委托 Coder；收到 Coder 返回后给出总结；
//   - Coder（含 file_write + git_* 工具）按已执行的工具结果数顺序推进：
//     第 0 次 → file_write 写文件；第 1 次 → git_commit 提交；第 2 次 → git_status 确认；之后返回文本。
//
// 全程不依赖真实 LLM，纯脚本化驱动（见 docs/loop/LEARNINGS「M1 集成测试指引」）。
func mockGitServer(t *testing.T) *httptest.Server {
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
		tools, _ := req["tools"].([]any)

		// 统计已执行的工具结果数量，并区分当前是哪个代理在调用。
		toolResults := 0
		names := map[string]bool{}
		for _, m := range msgs {
			if mm, ok := m.(map[string]any); ok && mm["role"] == "tool" {
				toolResults++
			}
		}
		for _, tlv := range tools {
			if tt, ok := tlv.(map[string]any); ok {
				if fn, ok := tt["function"].(map[string]any); ok {
					if n, ok := fn["name"].(string); ok {
						names[n] = true
					}
				}
			}
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		f, _ := w.(http.Flusher)

		switch {
		case names["coder"]:
			// Orchestrator：首轮委托 Coder，之后给出总结。
			if toolResults >= 1 {
				writeSSEText(w, f, "已委托 Coder 完成文件创建并提交到 git 仓库。")
				return
			}
			writeSSEToolCall(w, f, "coder",
				`{"request":"请在受限工作目录下创建 hello.txt（内容 hello），然后用 git_commit 提交，最后用 git_status 确认仓库干净"}`)
		case names["file_write"]:
			// Coder：按已执行工具数顺序推进。
			switch toolResults {
			case 0:
				writeSSEToolCall(w, f, "file_write", `{"path":"hello.txt","content":"hello"}`)
			case 1:
				writeSSEToolCall(w, f, "git_commit", `{"message":"add hello.txt"}`)
			case 2:
				writeSSEToolCall(w, f, "git_status", `{}`)
			default:
				writeSSEText(w, f, "已完成文件创建与 git 提交。")
			}
		default:
			writeSSEText(w, f, "无可用工具，结束。")
		}
	}))
}

// TestEngine_CoderGitCommit_Workspace 验证 M2-01 核心验收（team 模式）：
// 建 workspace（自动 git init）→ Coder 写文件 → git_commit 提交 → git_status 干净。
// 不调用真实 LLM，用 mockGitServer 脚本化驱动 Coder 的 file_write→git_commit→git_status 调用链，
// 并断言最终文件已落盘、仓库已提交（git_status 干净 / git_log 含提交说明）。
func TestEngine_CoderGitCommit_Workspace(t *testing.T) {
	workdir := t.TempDir()

	// 模拟 workspace 创建时的自动 git init（M2-01）。
	ex, err := codectool.NewGitExecutor(workdir, nil, nil, executor.ModeUnattended)
	if err != nil {
		t.Fatalf("NewGitExecutor: %v", err)
	}
	if _, err := codectool.GitInit(context.Background(), ex); err != nil {
		t.Fatalf("GitInit: %v", err)
	}
	// 测试环境显式设置本地身份，使 git commit 不依赖机器全局配置。
	// 必须先 git init：无 --global 的 git config 只写当前仓库 .git/config，对 CI（无全局身份）亦然。
	if _, err := ex.Run(context.Background(), "git config user.email test@test.local"); err != nil {
		t.Fatalf("git config email: %v", err)
	}
	if _, err := ex.Run(context.Background(), "git config user.name tester"); err != nil {
		t.Fatalf("git config name: %v", err)
	}

	srv := mockGitServer(t)
	defer srv.Close()

	eng, err := New(ModelConfig{
		ModelID:  "mock-model",
		BaseURL:  srv.URL,
		APIKey:   "test-key",
		Protocol: "openai",
		Team:     TeamConfig{EnableSubAgents: true},
		Workdir:  workdir,
	})
	if err != nil {
		t.Fatalf("New engine with sub-agents failed: %v", err)
	}
	defer eng.Close()

	reply, err := eng.Chat(context.Background(), "sess-git", "请创建 hello.txt 并提交", nil)
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}

	// 1) Coder 确实写出了文件。
	got, rerr := os.ReadFile(filepath.Join(workdir, "hello.txt"))
	if rerr != nil {
		t.Fatalf("Coder 未写入 hello.txt（%v）；Orchestrator 回复=%q", rerr, reply)
	}
	if string(got) != "hello" {
		t.Fatalf("文件内容不符：got=%q", string(got))
	}

	// 2) 仓库已提交：git_status 干净、git_log 含提交说明（用 Git 工具复核，证明 Coder 真提交）。
	gt, gerr := codectool.NewGitTools(workdir, nil, nil, executor.ModeUnattended)
	if gerr != nil {
		t.Fatalf("NewGitTools: %v", gerr)
	}
	byName := map[string]tool.Tool{}
	for _, tl := range gt {
		byName[tl.Declaration().Name] = tl
	}
	call := func(name string, argsJSON string) string {
		ct, ok := byName[name].(tool.CallableTool)
		if !ok {
			t.Fatalf("工具 %s 不是 CallableTool", name)
		}
		out, cerr := ct.Call(context.Background(), []byte(argsJSON))
		if cerr != nil {
			t.Fatalf("%s 失败: %v", name, cerr)
		}
		return fmt.Sprint(out)
	}

	status := strings.TrimSpace(call("git_status", `{}`))
	if status != "" {
		t.Fatalf("提交后 git_status 应为空（干净），got=%q", status)
	}
	logOut := call("git_log", `{"limit":10}`)
	if !strings.Contains(logOut, "add hello.txt") {
		t.Fatalf("git_log 应含提交说明 add hello.txt，got=%q", logOut)
	}
	t.Logf("✅ Coder 写文件并提交成功：content=%q, git_status 干净, git_log=%q, reply=%q",
		string(got), strings.TrimSpace(logOut), reply)
}
