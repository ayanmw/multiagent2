// loop_e2e_test.go 实现 M7.5-02「真实模型端到端冒烟」的核心验证：
// 在真实 LLM（本地网关 127.0.0.1:8088，SMOKE_LLM_BASE_URL 开启）或 Mock 端点下，
// 跑一条完整自主 Loop 全链路：goal（目标契约）→ taskrun（后台子任务扇出）→
// worktree（子任务隔离分支）→ merge（终态自动合并回主分支），并断言链路产物：
//  1. 主分支根目录出现子任务创建的文件（内容正确）——证明 worktree 内改动已 merge 回主分支；
//  2. 该文件已 git 提交（提交说明存在）——证明子任务真实 commit；
//  3. worktree 检出目录已被清理（merge 成功后 Finalize 移除）——证明隔离与收尾完整；
//  4. 目标契约收敛为 complete——证明 goal 契约全程约束且正常闭环。
//
// 验收标准「真实 LLM 下 Loop 全链路成功 ≥2 次」：测试以 loop-1/loop-2 两个独立
// 子测试各跑一遍完整链路（各自独立临时仓库/引擎/会话），两次都必须成功。
//
// 无真实模型环境（CI / 沙箱）下自动回落脚本化 Mock：LLM 决策被脚本化驱动，
// 但 taskrun 派生、worktree 隔离、git commit/merge 全部真实执行，套件始终可绿，
// 同时完整覆盖「全链路真跑通」而非仅验证类型。
package smoke

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
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/taskrun/inprocess"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"

	codeagent "github.com/ayanmw/multiagent2/server/internal/agent"
	"github.com/ayanmw/multiagent2/server/internal/engine"
	"github.com/ayanmw/multiagent2/server/internal/executor"
	goalpkg "github.com/ayanmw/multiagent2/server/internal/goal"
	"github.com/ayanmw/multiagent2/server/internal/taskrun"
	codectool "github.com/ayanmw/multiagent2/server/internal/tool"
	"github.com/ayanmw/multiagent2/server/internal/worktree"
)

// loopFileName 是子任务在 worktree 内创建并提交的文件名。
// 每次 Loop 子测试使用独立临时仓库，文件名可固定。
const loopFileName = "task-output.txt"

// loopFileContent 是子任务写入的文件内容。
const loopFileContent = "DONE-LOOP"

// TestSmoke_Loop_GoalTaskrunWorktreeMerge 是 M7.5-02 的核心验收测试：
// 真实 LLM 下完整自主 Loop（goal→taskrun→worktree→merge）连续成功 ≥2 次。
// 设置 SMOKE_LLM_BASE_URL/SMOKE_LLM_API_KEY/SMOKE_LLM_MODEL 即走真实网关；
// 未设置时回落脚本化 Mock（链路真实执行，LLM 决策脚本化）。
func TestSmoke_Loop_GoalTaskrunWorktreeMerge(t *testing.T) {
	for i := 1; i <= 2; i++ {
		t.Run(fmt.Sprintf("loop-%d", i), func(t *testing.T) {
			runLoopE2E(t)
		})
	}
}

// runLoopE2E 执行一次完整自主 Loop 链路并做全部断言。
func runLoopE2E(t *testing.T) {
	t.Helper()
	real := os.Getenv("SMOKE_LLM_BASE_URL") != ""
	t.Logf("LLM 模式：%s", map[bool]string{true: "真实网关（" + os.Getenv("SMOKE_LLM_BASE_URL") + "）", false: "脚本化 Mock"}[real])

	// 1. 模拟 workspace：独立临时 git 仓库（自动 git init + 初始提交，M2-01）。
	repoDir := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	initGitRepo(t, repoDir)

	// 2. LLM 端点（真实或 Mock）。
	baseURL, apiKey := "", ""
	stop := func() {}
	if real {
		baseURL = os.Getenv("SMOKE_LLM_BASE_URL")
		apiKey = os.Getenv("SMOKE_LLM_API_KEY")
	} else {
		srv := mockLoopLLM(t)
		baseURL, apiKey, stop = srv.URL, "test-key", srv.Close
	}
	defer stop()

	// 3. taskrun 控制面（M2-04）+ worktree 隔离钩子（M2-05）。
	wtMgr := worktree.NewManager()
	resolver := taskrun.WorkerResolver{
		ResolveModel: func(_ context.Context, _ string) (model.Model, error) {
			return openai.New(smokeModelID(), openai.WithAPIKey(apiKey), openai.WithBaseURL(baseURL)), nil
		},
		ResolveWorkdir: func(_ context.Context, _ string) (string, error) { return repoDir, nil },
		Worktree:       &taskrun.WorktreeHook{Enabled: true, Manager: wtMgr},
	}
	workerFactory := taskrun.BuildAgentFactory(codeagent.GuardrailConfig{}, resolver, executor.ModeUnattended)
	rawCtrl, err := taskrun.NewController(context.Background(), codeagent.RoleCoder, workerFactory, inprocess.NewMemoryStore(), nil, resolver.Worktree)
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	defer rawCtrl.Close()
	// M7.5-02：包装 Controller 透传 worker 用户身份（v1.10.0 下 worker 工厂拿不到
	// invocation，必须经 RunContext 钩子注入，否则子任务派生即失败）。
	ctrl := taskrun.WithWorkerIdentity(rawCtrl)

	// 4. 目标契约存储（注入以便断言收敛状态）。
	gstore := goalpkg.NewStore(0)

	// 5. 引擎：Orchestrator（子代理 + 目标契约 + 后台任务工具）。
	sessionID := "sess-loop-e2e-" + fmt.Sprint(time.Now().UnixNano())
	eng, err := engine.New(engine.ModelConfig{
		ModelID:           smokeModelID(),
		BaseURL:           baseURL,
		APIKey:            apiKey,
		Protocol:          "openai",
		Timeout:           300 * time.Second,
		Team:              engine.TeamConfig{EnableSubAgents: true, EnableGoal: true, GoalStore: gstore, MaxGoalNudges: 8},
		Workdir:           repoDir,
		Guardrail:         codeagent.GuardrailConfig{MaxLLMCalls: 60, MaxToolIterations: 40},
		TaskRunController: ctrl,
		ExecutorMode:      executor.ModeUnattended,
	})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	defer eng.Close()

	// 6. 驱动完整自主 Loop。
	ctx, cancel := context.WithTimeout(context.Background(), 330*time.Second)
	defer cancel()
	reply := ""
	// SMOKE_LOOP_DEBUG=1 时用 Stream 逐事件打印工具调用序列（诊断真实模型行为用）。
	if os.Getenv("SMOKE_LOOP_DEBUG") != "" {
		ch, serr := eng.Stream(engine.WithUserID(ctx, "1"), sessionID, loopPrompt(), nil)
		if serr != nil {
			t.Fatalf("Stream: %v", serr)
		}
		step := 0
		for ev := range ch {
			step++
			if ev.IsError {
				t.Logf("[event %d] error=%q circuit=%v", step, ev.ErrorMsg, ev.CircuitBreak)
				continue
			}
			for _, c := range ev.Choices {
				for _, tc := range c.ToolCalls {
					t.Logf("[event %d] TOOL_CALL %s %s", step, tc.Name, tc.Arguments)
				}
				if t2 := c.DeltaContent; t2 != "" {
					t.Logf("[event %d] delta: %s", step, truncateLog(t2))
				} else if t2 := c.MessageContent; t2 != "" {
					t.Logf("[event %d] message: %s", step, truncateLog(t2))
				}
			}
		}
	} else {
		var cerr error
		reply, cerr = eng.Chat(engine.WithUserID(ctx, "1"), sessionID, loopPrompt(), nil)
		if cerr != nil {
			t.Fatalf("Chat（自主 Loop）失败: %v", cerr)
		}
		t.Logf("Orchestrator 最终汇报：%s", truncateLog(reply))
	}

	// 7. 全链路断言。
	// 7.1 主分支存在子任务创建的文件（worktree 改动已 merge 回主分支）。
	got, rerr := os.ReadFile(filepath.Join(repoDir, loopFileName))
	if rerr != nil {
		t.Fatalf("主分支未发现 %s（worktree 改动未 merge 回主分支？）：%v；汇报=%q", loopFileName, rerr, reply)
	}
	if strings.TrimSpace(string(got)) != loopFileContent {
		t.Fatalf("%s 内容不符：got=%q want=%q", loopFileName, strings.TrimSpace(string(got)), loopFileContent)
	}

	// 7.2 文件已 git 提交（git log 含提交说明）。
	logOut := gitLog(t, repoDir, 15)
	if !strings.Contains(logOut, "add "+loopFileName) {
		t.Fatalf("git log 未含子任务的提交说明（add %s），log=%q", loopFileName, logOut)
	}

	// 7.3 worktree 检出目录已清理（merge 成功后 Finalize 移除，隔离收尾完整）。
	wtParent := filepath.Join(filepath.Dir(repoDir), ".taskrun-worktrees")
	if entries, derr := os.ReadDir(wtParent); derr == nil && len(entries) > 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("worktree 目录未清理：%v", names)
	}

	// 7.4 目标契约收敛为 complete（goal 全程约束且正常闭环）。
	g, gerr := gstore.Get("sess:" + sessionID)
	if gerr != nil {
		t.Fatalf("未找到目标契约（%v）；goal 未建立或作用域不符", gerr)
	}
	if g.Status != goalpkg.StatusComplete {
		t.Fatalf("目标契约未收敛为 complete，实际 %s（progress=%q blocker=%q）", g.Status, g.Progress, g.Blocker)
	}

	t.Logf("✅ 完整自主 Loop 全链路成功：%s 已写入主分支并提交，worktree 已清理，goal=complete", loopFileName)
}

// initGitRepo 初始化一个含初始提交的 git 仓库（模拟 workspace 自动 git init + 首次提交）。
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	ex, err := codectool.NewGitExecutor(dir, nil, nil, executor.ModeUnattended)
	if err != nil {
		t.Fatalf("NewGitExecutor: %v", err)
	}
	if _, err := codectool.GitInit(context.Background(), ex); err != nil {
		t.Fatalf("GitInit: %v", err)
	}
	// 先 init 再设本地身份（CI 无全局身份时必须仓库级配置；worktree 共享同一 .git/config）。
	if _, err := ex.Run(context.Background(), "git config user.email test@test.local"); err != nil {
		t.Fatalf("git config email: %v", err)
	}
	if _, err := ex.Run(context.Background(), "git config user.name tester"); err != nil {
		t.Fatalf("git config name: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# loop e2e demo\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	if _, err := ex.Run(context.Background(), "git add README.md"); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if _, err := ex.Run(context.Background(), `git commit -m "init"`); err != nil {
		t.Fatalf("git commit init: %v", err)
	}
}

// gitLog 返回仓库最近 N 条提交日志（--oneline）。
func gitLog(t *testing.T, repoDir string, n int) string {
	t.Helper()
	ex, err := codectool.NewGitExecutor(repoDir, nil, nil, executor.ModeUnattended)
	if err != nil {
		t.Fatalf("NewGitExecutor: %v", err)
	}
	out, rerr := ex.Run(context.Background(), fmt.Sprintf("git log --oneline -%d", n))
	if rerr != nil {
		t.Fatalf("git log: %v", rerr)
	}
	return out.Stdout
}

// loopPrompt 是驱动完整自主 Loop 的用户指令（真实模型友好、步骤明确）。
// 注意：真实模型在工具丰富的环境中偶尔会「过度谦虚」（声称缺少能力而不调用工具），
// 故开头显式声明必要工具均已装配、可直接调用，避免模型拒绝执行。
func loopPrompt() string {
	return "请完成一次完整的自主开发闭环，严格按以下流程执行（这是验收测试，每一步都不可跳过）。" +
		"注意：create_goal、get_goal、update_goal、start_task_run 等必要工具都已装配在你的工具列表中，直接调用即可完成，不需要任何外部能力或额外授权；不要声称缺少工具。" +
		"\n1. 第一步必须调用 create_goal 建立目标契约：title 填「后台子任务隔离开发」，" +
		"description 填「通过后台子任务在独立 worktree 中创建文件并提交，随后自动合并回主分支」，" +
		"acceptance_criteria 至少包含两项：主分支根目录存在 " + loopFileName + " 且内容为 " + loopFileContent + "；子任务状态为 completed。\n" +
		"2. 调用 start_task_run 派发后台子任务，参数：task 填「请在工作目录创建文件 " + loopFileName +
		"（内容为 " + loopFileContent + "），然后调用 git_commit 提交（message 为 add " + loopFileName + "），完成后用中文简要说明」；" +
		"mode 必须填 sync（同步等待子任务完成，start_task_run 会阻塞到子任务结束并返回最终状态）。\n" +
		"3. 确认 start_task_run 返回的 run 状态为 completed（若未完成则继续等待/查询，不要臆断）。\n" +
		"4. 子任务确认完成后，调用 update_goal 把状态改为 complete。\n" +
		"5. 最后用中文汇报：文件是否写入、是否已提交、是否已合并回主分支。"
}

// truncateLog 截断超长汇报（日志可读性）。
func truncateLog(s string) string {
	r := []rune(s)
	if len(r) <= 300 {
		return s
	}
	return string(r[:300]) + "...(截断)"
}

// ---------------------------------------------------------------------------
// Mock 端点（无真实模型时脚本化驱动 orchestrator 与 worker 两条链路）
// ---------------------------------------------------------------------------

// mockLoopLLM 脚本化 Mock：按「当前请求属于哪个代理 + 已执行工具结果数」驱动决策。
//   - Orchestrator（tools 含 start_task_run）：create_goal → start_task_run(mode=sync)
//     → update_goal(complete) → 总结文本；
//   - Worker（tools 含 file_write）：file_write → git_commit → git_status → 文本。
//
// 工具调用由框架真实执行（taskrun 派生 / worktree 隔离 / git commit-merge 全真实），
// 仅 LLM 的「下一步决策」被脚本化，保证 CI 无真实模型时套件仍全链路可绿。
//
// 双模式响应：目标契约未收敛时 goal enforcer 会关闭流式（stream=false，请求普通
// JSON 响应），收敛后恢复流式——与真实模型 API 行为一致，故 mock 同时支持两种格式。
func mockLoopLLM(t *testing.T) *httptest.Server {
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
		stream, _ := req["stream"].(bool)

		d := decideLoopCall(msgs, tools)
		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			f, _ := w.(http.Flusher)
			if d.isTool {
				loopToolCall(w, f, d.name, d.args)
			} else {
				loopText(w, f, d.text)
			}
			return
		}
		// 非流式：普通 JSON 响应。
		w.Header().Set("Content-Type", "application/json")
		var msg map[string]any
		finish := "stop"
		if d.isTool {
			msg = map[string]any{
				"role": "assistant",
				"tool_calls": []any{map[string]any{
					"id": "call_1", "type": "function",
					"function": map[string]any{"name": d.name, "arguments": d.args},
				}},
			}
			finish = "tool_calls"
		} else {
			msg = map[string]any{"role": "assistant", "content": d.text}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "t", "object": "chat.completion", "created": 1, "model": "m",
			"choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": finish}},
		})
	}))
}

// loopDecision 是一次脚本化决策：要么调用工具（name+args），要么输出文本。
type loopDecision struct {
	isTool bool
	name   string
	args   string
	text   string
}

// decideLoopCall 依据「代理身份 + 已执行工具结果数」返回脚本化的下一步决策。
func decideLoopCall(msgs, tools []any) loopDecision {
	toolResults := 0
	for _, m := range msgs {
		if mm, ok := m.(map[string]any); ok && mm["role"] == "tool" {
			toolResults++
		}
	}
	names := map[string]bool{}
	for _, tlv := range tools {
		if tt, ok := tlv.(map[string]any); ok {
			if fn, ok := tt["function"].(map[string]any); ok {
				if n, ok := fn["name"].(string); ok {
					names[n] = true
				}
			}
		}
	}

	switch {
	case names["start_task_run"]:
		// Orchestrator：goal → 派发（sync 等待完成）→ 收敛。
		switch {
		case toolResults == 0:
			return loopDecision{isTool: true, name: "create_goal",
				args: `{"title":"后台子任务隔离开发","description":"worktree 隔离 + 后台子任务","acceptance_criteria":["主分支含 task-output.txt","子任务 completed"]}`}
		case toolResults == 1:
			return loopDecision{isTool: true, name: "start_task_run",
				args: `{"task":"请创建 task-output.txt（内容 DONE-LOOP）并 git_commit 提交（message: add task-output.txt）","mode":"sync"}`}
		case toolResults == 2:
			return loopDecision{isTool: true, name: "update_goal",
				args: `{"status":"complete","progress":"子任务已完成并合并回主分支"}`}
		default:
			return loopDecision{text: "已完成：后台子任务在独立 worktree 创建并提交文件，已自动合并回主分支，目标完成。"}
		}
	case names["file_write"]:
		// Worker（Coder 子代理）：写文件 → 提交 → 确认干净。
		switch toolResults {
		case 0:
			return loopDecision{isTool: true, name: "file_write",
				args: `{"path":"task-output.txt","content":"DONE-LOOP"}`}
		case 1:
			return loopDecision{isTool: true, name: "git_commit",
				args: `{"message":"add task-output.txt"}`}
		case 2:
			return loopDecision{isTool: true, name: "git_status", args: `{}`}
		default:
			return loopDecision{text: "已在 worktree 中创建文件并提交。"}
		}
	default:
		return loopDecision{text: "无可用工具，结束。"}
	}
}

// loopText 写出纯文本流式响应。
func loopText(w http.ResponseWriter, f http.Flusher, text string) {
	for _, ch := range []string{
		fmt.Sprintf(`data: {"id":"t","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":%s},"finish_reason":null}]}`, loopMustJSON(text)),
		`data: {"id":"t","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"content":""},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	} {
		fmt.Fprintf(w, "%s\n\n", ch)
		if f != nil {
			f.Flush()
		}
	}
}

// loopToolCall 写出一个 tool_call 流式响应。
func loopToolCall(w http.ResponseWriter, f http.Flusher, name, args string) {
	for _, ch := range []string{
		fmt.Sprintf(`data: {"id":"t","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":%s,"arguments":%s}}]},"finish_reason":null}]}`, loopMustJSON(name), loopMustJSON(args)),
		`data: {"id":"t","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"content":""},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	} {
		fmt.Fprintf(w, "%s\n\n", ch)
		if f != nil {
			f.Flush()
		}
	}
}

// loopMustJSON 序列化为合法 JSON 字面量（嵌入外层 JSON 时自动转义）。
func loopMustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `""`
	}
	return string(b)
}
