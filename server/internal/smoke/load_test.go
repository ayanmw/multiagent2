// load_test.go 实现 M7.5-03「并发与压测」的验收测试套件。
//
// 四个独立场景，覆盖「多用户并发对话 / SSE 长连接稳定性 / taskrun 扇出 5+ 子任务 /
// SQLite 写锁」四大并发面。验收标准：压测报告（P50/P90/P99/max 时延）、无死锁、
// 无连接泄漏。
//
// 与 M6-06/M7.5-02 冒烟套件一致：无真实模型环境（CI/沙箱）下 LLM 决策脚本化 Mock，
// 但引擎、taskrun 派生、worktree 隔离、git commit/merge、SQLite 落库全部真实执行，
// 因此压测结论反映真实运行路径。设置 SMOKE_LLM_BASE_URL 可切换真实网关（文本层）。
//
// 规模可用环境变量调整（默认值面向 CI 快速全绿，本机压测可调大）：
//
//	LOAD_N_USERS          并发对话用户数（默认 20，每用户 3 轮 = 60 次引擎调用）
//	LOAD_SSE_CONNS        SSE 长连接并发数（默认 10）
//	LOAD_SSE_CHUNKS       单条 SSE 流的分片数（默认 50，模拟长流）
//	LOAD_TASKRUN_FANOUT   扇出子任务数（默认 6，验收要求 ≥5）
//	LOAD_SQLITE_WRITERS   SQLite 并发写者数（默认 16）
//	LOAD_REPORT           压测报告落盘路径（可选；未设置仅输出 t.Log 汇总）
//
// 运行：go test -count=1 -v ./internal/smoke/ -run 'TestLoad_' -timeout 15m
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
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/taskrun/inprocess"
	taskrunruntime "trpc.group/trpc-go/trpc-agent-go/agent/taskrun"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"

	codeagent "github.com/ayanmw/multiagent2/server/internal/agent"
	"github.com/ayanmw/multiagent2/server/internal/engine"
	"github.com/ayanmw/multiagent2/server/internal/executor"
	goalpkg "github.com/ayanmw/multiagent2/server/internal/goal"
	servermodel "github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/ayanmw/multiagent2/server/internal/taskrun"
	codectool "github.com/ayanmw/multiagent2/server/internal/tool"
	"github.com/ayanmw/multiagent2/server/internal/worktree"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ---------------------------------------------------------------------------
// 压测通用工具：环境变量读取 / 时延分位数 / 汇总打印
// ---------------------------------------------------------------------------

// loadEnvInt 读取压测规模环境变量，缺省或非法时回退默认值。
func loadEnvInt(name string, def int) int {
	if v := os.Getenv(name); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// percentile 返回排序后时延序列的 p 分位（p ∈ [0,1]；线性插值最近邻）。
func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(p * float64(len(sorted)-1))
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// loadSummary 汇总一次压测场景的时延/错误/耗时并打印结构化指标（供压测报告引用）。
// 返回汇总文本（报告生成与 t.Log 复用）。
func loadSummary(name string, durs []time.Duration, errs int, wall time.Duration) string {
	sorted := append([]time.Duration(nil), durs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	var total time.Duration
	for _, d := range durs {
		total += d
	}
	avg := time.Duration(0)
	if len(durs) > 0 {
		avg = total / time.Duration(len(durs))
	}
	rate := 0.0
	if n := len(durs) + errs; n > 0 {
		rate = float64(errs) / float64(n) * 100
	}
	line := fmt.Sprintf("[LOAD] 场景=%s 样本=%d 错误=%d 错误率=%.2f%% 墙钟=%s "+
		"avg=%s P50=%s P90=%s P99=%s max=%s",
		name, len(durs), errs, rate, wall.Round(time.Millisecond),
		avg.Round(time.Millisecond), percentile(sorted, 0.50).Round(time.Millisecond),
		percentile(sorted, 0.90).Round(time.Millisecond), percentile(sorted, 0.99).Round(time.Millisecond),
		percentile(sorted, 1.0).Round(time.Millisecond))
	fmt.Println(line)
	return line
}

// writeLoadReport 把压测报告摘要追加到 LOAD_REPORT 指定路径（可选；未设置仅打印）。
// 追加模式：多个场景顺序执行时各自追加一行，最终文件汇总全部场景指标。
func writeLoadReport(t *testing.T, lines []string) {
	path := os.Getenv("LOAD_REPORT")
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Logf("writeLoadReport: mkdir: %v", err)
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Logf("writeLoadReport: open: %v", err)
		return
	}
	defer f.Close()
	if _, err := f.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
		t.Logf("writeLoadReport: write: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 场景 1：多用户并发对话（P50/P90/P99 时延、错误率、无死锁）
// ---------------------------------------------------------------------------

// TestLoad_ConcurrentChat 以 N 个用户并发对话（每用户 3 轮、第 2/3 轮带历史回灌），
// 统计单轮 P50/P90/P99/max 时延并断言：全部成功（无错误、回复非空）、总墙钟 < 上限
// （超时即视为死锁/阻塞，用上限兜底证明无死锁）。
func TestLoad_ConcurrentChat(t *testing.T) {
	users := loadEnvInt("LOAD_N_USERS", 20)
	rounds := 3
	var requests atomic.Int64

	// Mock LLM：流式返回 3 个分片，分片间 sleep 3ms 模拟真实网络/生成延迟。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		f, _ := w.(http.Flusher)
		text := "OK-" + fmt.Sprint(time.Now().UnixNano())
		for i, ch := range []string{
			fmt.Sprintf(`data: {"id":"t","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":%s},"finish_reason":null}]}`, loopMustJSON(text)),
			`data: {"id":"t","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"content":""},"finish_reason":"stop"}]}`,
			`data: [DONE]`,
		} {
			if i > 0 {
				time.Sleep(3 * time.Millisecond)
			}
			fmt.Fprintf(w, "%s\n\n", ch)
			if f != nil {
				f.Flush()
			}
		}
	}))
	defer srv.Close()

	var (
		mu    sync.Mutex
		durs  []time.Duration
		fails int
	)
	start := time.Now()
	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	for i := 1; i <= users; i++ {
		wg.Add(1)
		go func(uid int) {
			defer wg.Done()
			sessionID := fmt.Sprintf("sess-load-%d-%d", uid, time.Now().UnixNano())
			var history []engine.ChatMessage
			for rnd := 1; rnd <= rounds; rnd++ {
				// 每轮新建 Engine（贴近生产 api 层行为：每次请求独立 Runner）。
				eng, err := engine.New(engine.ModelConfig{
					ModelID:  smokeModelID(),
					BaseURL:  srv.URL,
					APIKey:   "test-key",
					Protocol: "openai",
					Timeout:  30 * time.Second,
				})
				if err != nil {
					mu.Lock()
					fails++
					mu.Unlock()
					return
				}
				begin := time.Now()
				reply, cerr := eng.Chat(engine.WithUserID(ctx, fmt.Sprint(uid)), sessionID,
					fmt.Sprintf("第 %d 轮问题（用户 %d）：请确认收到", rnd, uid), history)
				elapsed := time.Since(begin)
				_ = eng.Close()
				if cerr != nil {
					mu.Lock()
					fails++
					mu.Unlock()
					return
				}
				if strings.TrimSpace(reply) == "" {
					mu.Lock()
					fails++
					mu.Unlock()
					return
				}
				mu.Lock()
				durs = append(durs, elapsed)
				mu.Unlock()
				history = append(history,
					engine.ChatMessage{Role: "user", Content: fmt.Sprintf("第 %d 轮问题", rnd)},
					engine.ChatMessage{Role: "assistant", Content: reply},
				)
			}
		}(i)
	}
	wg.Wait()
	wall := time.Since(start)

	line := loadSummary("多用户并发对话", durs, fails, wall)
	writeLoadReport(t, []string{
		"## M7.5-03 压测报告（" + time.Now().Format("2006-01-02 15:04") + "）",
		"", line,
	})

	if fails > 0 {
		t.Fatalf("并发对话失败 %d 次（用户=%d 轮=%d 请求=%d）", fails, users, rounds, requests.Load())
	}
	if wall > 90*time.Second {
		t.Fatalf("并发对话墙钟超上限（90s），疑似死锁/阻塞：%v", wall)
	}
	if requests.Load() != int64(users*rounds) {
		t.Fatalf("Mock LLM 请求数不符：got=%d want=%d（连接泄漏或请求丢失）", requests.Load(), users*rounds)
	}
	t.Logf("✅ 多用户并发对话：%d 用户 × %d 轮 = %d 次引擎调用全部成功，无死锁，无连接泄漏", users, rounds, requests.Load())
}

// ---------------------------------------------------------------------------
// 场景 2：SSE 长连接稳定性（慢速消费、无意外中断、无连接泄漏）
// ---------------------------------------------------------------------------

// TestLoad_SSELongConnection 以多个并发 SSE 长连接（每条流 50 分片）验证：
//   - 流完整：每连接收到全部文本分片且正常收尾（无 IsError 事件）；
//   - 慢速消费不被打断：消费者每个事件处理 2ms（模拟前端渲染节流）；
//   - 无连接泄漏：全部流结束后 Mock 服务器活跃连接计数归零。
func TestLoad_SSELongConnection(t *testing.T) {
	conns := loadEnvInt("LOAD_SSE_CONNS", 10)
	chunks := loadEnvInt("LOAD_SSE_CHUNKS", 50)

	var active atomic.Int32 // Mock 服务器「进行中连接」计数：进入 +1，写完 -1
	var totalReqs atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		active.Add(1)
		totalReqs.Add(1)
		defer active.Add(-1)
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		f, _ := w.(http.Flusher)
		for i := 0; i < chunks; i++ {
			fmt.Fprintf(w, `data: {"id":"t","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"c%d "},"finish_reason":null}]}`+"\n\n", i)
			if f != nil {
				f.Flush()
			}
			time.Sleep(3 * time.Millisecond) // 模拟真实流式节奏
		}
		fmt.Fprintf(w, `data: {"id":"t","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"content":""},"finish_reason":"stop"}]}%s`, "\n\n")
		fmt.Fprintf(w, `data: [DONE]%s`, "\n\n")
		if f != nil {
			f.Flush()
		}
	}))
	defer srv.Close()

	var (
		mu       sync.Mutex
		durs     []time.Duration
		events   []int // 每连接收到的事件数
		textLens []int // 每连接累计文本长度
		errs     int
	)
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	for i := 1; i <= conns; i++ {
		wg.Add(1)
		go func(uid int) {
			defer wg.Done()
			eng, err := engine.New(engine.ModelConfig{
				ModelID:  smokeModelID(),
				BaseURL:  srv.URL,
				APIKey:   "test-key",
				Protocol: "openai",
				Timeout:  30 * time.Second,
			})
			if err != nil {
				mu.Lock()
				errs++
				mu.Unlock()
				return
			}
			defer eng.Close()
			begin := time.Now()
			ch, serr := eng.Stream(engine.WithUserID(ctx, fmt.Sprint(uid)),
				fmt.Sprintf("sess-sse-%d", uid), "长连接压测", nil)
			if serr != nil {
				mu.Lock()
				errs++
				mu.Unlock()
				return
			}
			nEv, nChunk := 0, 0
			var sb strings.Builder
			for ev := range ch {
				nEv++
				if ev.IsError {
					mu.Lock()
					errs++
					mu.Unlock()
					return
				}
				for _, c := range ev.Choices {
					if t2 := c.DeltaContent; t2 != "" {
						nChunk++
						sb.WriteString(t2)
					}
				}
				time.Sleep(2 * time.Millisecond) // 慢速消费：模拟前端渲染节流
			}
			elapsed := time.Since(begin)
			mu.Lock()
			durs = append(durs, elapsed)
			events = append(events, nEv)
			textLens = append(textLens, sb.Len())
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	wall := time.Since(start)

	line := loadSummary("SSE 长连接", durs, errs, wall)
	writeLoadReport(t, []string{line})

	if errs > 0 {
		t.Fatalf("SSE 长连接错误 %d 次", errs)
	}
	// 每条流必须收到 ≥ chunks 个文本分片（+终帧），且文本非空。
	for i, n := range textLens {
		if n < chunks {
			t.Fatalf("连接 %d 文本不完整：len=%d want≥%d（事件数=%d）", i, n, chunks, events[i])
		}
	}
	// 无连接泄漏：全部流结束后活跃连接必须归零（handler 已全部退出）。
	if n := active.Load(); n != 0 {
		t.Fatalf("SSE 连接泄漏：结束后仍有 %d 个活跃连接", n)
	}
	t.Logf("✅ SSE 长连接：%d 并发 × %d 分片全部完整收尾，慢速消费未被打断，活跃连接归零", conns, chunks)
}

// ---------------------------------------------------------------------------
// 场景 3：taskrun 扇出 5+ 子任务（async 并发派发 → worktree 隔离 → 全部收敛）
// ---------------------------------------------------------------------------

// TestLoad_TaskrunFanout5Plus 以 async 模式扇出 ≥5 个后台子任务，验证：
//   - 并发扇出：一个 Orchestrator 依次 start_task_run(mode=async) 派发 N 个子任务；
//   - 全部收敛：list_task_runs 轮询至 N 个 run 全部进入终态（completed）；
//   - worktree 隔离：每个子任务在独立 worktree 写 task-k.txt 并提交，主分支最终
//     merge 回 N 个文件且内容正确；
//   - 无泄漏：.taskrun-worktrees 目录清理为空；goal 契约收敛 complete。
//
// LLM 决策脚本化 Mock（Orchestrator 与 worker 双链路），taskrun/worktree/git 全真实。
func TestLoad_TaskrunFanout5Plus(t *testing.T) {
	fanout := loadEnvInt("LOAD_TASKRUN_FANOUT", 6)
	if fanout < 5 {
		t.Fatalf("LOAD_TASKRUN_FANOUT 必须 ≥5（验收标准），当前 %d", fanout)
	}

	repoDir := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	initGitRepo(t, repoDir)

	srv := mockFanoutLLM(t)
	defer srv.Close()

	wtMgr := worktree.NewManager()
	resolver := taskrun.WorkerResolver{
		ResolveModel: func(_ context.Context, _ string) (model.Model, error) {
			return openai.New(smokeModelID(), openai.WithAPIKey("test-key"), openai.WithBaseURL(srv.URL)), nil
		},
		ResolveWorkdir: func(_ context.Context, _ string) (string, error) { return repoDir, nil },
		Worktree:       &taskrun.WorktreeHook{Enabled: true, Manager: wtMgr},
	}
	workerFactory := taskrun.BuildAgentFactory(codeagent.GuardrailConfig{}, resolver, executor.ModeUnattended, executor.BackendHost, executor.DockerOptions{})
	rawCtrl, err := taskrun.NewController(context.Background(), codeagent.RoleCoder, workerFactory, inprocess.NewMemoryStore(), nil, resolver.Worktree)
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	defer rawCtrl.Close()
	ctrl := taskrun.WithWorkerIdentity(rawCtrl) // v1.10.0：worker 工厂拿不到 invocation，必须透传身份

	gstore := goalpkg.NewStore(0)
	sessionID := "sess-load-fanout-" + fmt.Sprint(time.Now().UnixNano())
	eng, err := engine.New(engine.ModelConfig{
		ModelID:           smokeModelID(),
		BaseURL:           srv.URL,
		APIKey:            "test-key",
		Protocol:          "openai",
		Timeout:           120 * time.Second,
		Team:              engine.TeamConfig{EnableSubAgents: true, EnableGoal: true, GoalStore: gstore, MaxGoalNudges: 20},
		Workdir:           repoDir,
		Guardrail:         codeagent.GuardrailConfig{MaxLLMCalls: 120, MaxToolIterations: 200},
		TaskRunController: ctrl,
		ExecutorMode:      executor.ModeUnattended,
	})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	defer eng.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	begin := time.Now()
	reply, cerr := eng.Chat(engine.WithUserID(ctx, "1"), sessionID, fanoutPrompt(fanout), nil)
	wall := time.Since(begin)
	if cerr != nil {
		t.Fatalf("Chat（扇出 %d）失败: %v", fanout, cerr)
	}
	t.Logf("Orchestrator 汇报（截断）：%s", truncateLog(reply))

	// 断言 1：主分支出现 N 个 task-k.txt，内容 DONE-k（全部 merge 回主分支）。
	// 注意：run 标记 completed 与 Observer 异步 Finalize（worktree merge）存在
	// 最终一致性时差——Orchestrator 轮询到全部 run 终态时，最后一个 merge 可能
	// 尚未落盘。故此处轮询等待（带墙钟上限兜底：等待超时即视为 merge 未收敛/死锁）。
	deadline := time.Now().Add(60 * time.Second)
	for {
		missing := 0
		for k := 1; k <= fanout; k++ {
			if _, serr := os.Stat(filepath.Join(repoDir, fmt.Sprintf("task-%d.txt", k))); serr != nil {
				missing++
			}
		}
		if missing == 0 {
			break
		}
		if time.Now().After(deadline) {
			// 失败诊断：打印主分支 git log 全量、工作区文件、worktree 分支状态。
			t.Logf("DIAG 等待超时：仍有 %d/%d 个 task 文件未 merge 回主分支", missing, fanout)
			t.Logf("DIAG 主分支文件：%v", listDir(repoDir))
			t.Logf("DIAG git log --oneline --all -20：\n%s", gitLog(t, repoDir, 20))
			t.Logf("DIAG git branch -a：\n%s", gitBranch(t, repoDir))
			t.Fatalf("等待超时：主分支未出现全部 task 文件（Finalize merge 未收敛？缺 %d 个）", missing)
		}
		time.Sleep(100 * time.Millisecond)
	}
	for k := 1; k <= fanout; k++ {
		fname := fmt.Sprintf("task-%d.txt", k)
		got, rerr := os.ReadFile(filepath.Join(repoDir, fname))
		if rerr != nil {
			t.Fatalf("主分支读取 %s 失败：%v", fname, rerr)
		}
		want := fmt.Sprintf("DONE-%d", k)
		if strings.TrimSpace(string(got)) != want {
			t.Fatalf("%s 内容不符：got=%q want=%q", fname, strings.TrimSpace(string(got)), want)
		}
	}

	// 断言 2：git log 含 N 条子任务提交说明（真实 commit）。
	logOut := gitLog(t, repoDir, fanout*2+2)
	for k := 1; k <= fanout; k++ {
		sub := fmt.Sprintf("add task-%d.txt", k)
		if !strings.Contains(logOut, sub) {
			t.Fatalf("git log 未含 %q；log=%q", sub, logOut)
		}
	}

	// 断言 3：worktree 全部清理（merge 成功后 Finalize 移除，隔离收尾完整）。
	// 与断言 1 同理：Finalize 清理紧随 merge，轮询等待目录清空（墙钟上限兜底）。
	wtParent := filepath.Join(filepath.Dir(repoDir), ".taskrun-worktrees")
	{
		dline := time.Now().Add(30 * time.Second)
		for {
			entries, derr := os.ReadDir(wtParent)
			if derr != nil || len(entries) == 0 {
				break
			}
			if time.Now().After(dline) {
				names := make([]string, 0, len(entries))
				for _, e := range entries {
					names = append(names, e.Name())
				}
				t.Fatalf("worktree 目录未清理：%v", names)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	// 断言 4：goal 契约收敛 complete。
	g, gerr := gstore.Get("sess:" + sessionID)
	if gerr != nil {
		t.Fatalf("未找到目标契约（%v）", gerr)
	}
	if g.Status != goalpkg.StatusComplete {
		t.Fatalf("目标契约未收敛 complete，实际 %s", g.Status)
	}

	// 断言 5：控制器侧 N 个 run 全部进入终态（completed）——无悬挂子任务。
	runs, lerr := ctrl.List(ctx, taskrunruntime.ListFilter{OwnerUserID: "1", ParentSessionID: sessionID})
	if lerr != nil {
		t.Fatalf("List runs: %v", lerr)
	}
	if len(runs) != fanout {
		t.Fatalf("run 数量不符：got=%d want=%d（存在未注册/丢失的子任务）", len(runs), fanout)
	}
	for _, r := range runs {
		if !r.Status.IsTerminal() {
			t.Fatalf("run %s 未收敛终态：status=%s", r.ID, r.Status)
		}
		if r.Status != taskrunruntime.StatusCompleted {
			t.Fatalf("run %s 非 completed：status=%s", r.ID, r.Status)
		}
	}

	line := fmt.Sprintf("[LOAD] 场景=taskrun扇出 子任务=%d 主分支文件=%d 全部完成 墙钟=%s",
		fanout, fanout, wall.Round(time.Millisecond))
	fmt.Println(line)
	writeLoadReport(t, []string{line})
	t.Logf("✅ taskrun 扇出：%d 个子任务 async 并发派发、全部收敛、worktree 隔离合并回主分支、无泄漏", fanout)
}

// fanoutPrompt 是驱动扇出的用户指令（脚本化 mock 依工具结果数决策，prompt 语义仅供参考）。
func fanoutPrompt(fanout int) string {
	return fmt.Sprintf("请完成一次后台任务扇出验收：建立目标契约后，用 start_task_run(mode=async) 连续派发 %d 个后台子任务（task-1 到 task-%d，各自在独立 worktree 创建文件并提交），全部派发后轮询直至所有子任务 completed，最后把目标契约置为 complete。", fanout, fanout)
}

// mockFanoutLLM 脚本化驱动「Orchestrator 扇出 N + N 个并发 worker」双链路。
//
// Orchestrator（tools 含 start_task_run/list_task_runs）：
//
//	create_goal → start_task_run(task-k, async)×N → list_task_runs 轮询
//	（全部终态 → update_goal complete）→ 总结文本。
//
// Worker（tools 含 file_write/git_commit/git_status）：从任务描述解析 task-k 编号，
//
//	file_write(task-k.txt, DONE-k) → git_commit(add task-k.txt) → git_status → 文本。
//
// 工具调用全部由框架真实执行（N 个 worker 并行跑在各自 worktree 上），仅 LLM 决策被脚本化。
func mockFanoutLLM(t *testing.T) *httptest.Server {
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

		d := decideFanoutCall(msgs, tools)
		// 诊断日志：打印每个 LLM 请求的代理身份（工具集）、工具结果数、决策。
		if os.Getenv("LOAD_FANOUT_DEBUG") != "" {
			var names []string
			for _, tlv := range tools {
				if tt, ok := tlv.(map[string]any); ok {
					if fn, ok := tt["function"].(map[string]any); ok {
						if n, ok := fn["name"].(string); ok {
							names = append(names, n)
						}
					}
				}
			}
			tr := 0
			for _, m := range msgs {
				if mm, ok := m.(map[string]any); ok && mm["role"] == "tool" {
					tr++
				}
			}
			act := d.text
			if d.isTool {
				act = fmt.Sprintf("TOOL %s %s", d.name, d.args)
			}
			t.Logf("FANOUT-DEBUG tools=%v toolResults=%d → %s", names, tr, act)
		}
		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			f, _ := w.(http.Flusher)
			if d.isTool {
				loopToolCall(w, f, d.name, d.args)
			} else {
				loopText(w, f, d.text)
			}
			return
		}
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

// taskNoRe 从任务描述中提取 task-<N> 编号（worker 判断写入哪个文件）。
var taskNoRe = regexp.MustCompile(`task-(\d+)`)

// decideFanoutCall 依据「代理身份 + 工具结果历史」脚本化下一步决策。
func decideFanoutCall(msgs, tools []any) loopDecision {
	toolResults := 0
	var lastToolContent string
	for _, m := range msgs {
		if mm, ok := m.(map[string]any); ok && mm["role"] == "tool" {
			toolResults++
			if c, ok := mm["content"].(string); ok && c != "" {
				lastToolContent = c
			}
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
		// Orchestrator：goal → 扇出 N 个 async 子任务 → list 轮询收敛 → complete。
		switch {
		case toolResults == 0:
			return loopDecision{isTool: true, name: "create_goal",
				args: `{"title":"后台任务扇出压测","description":"async 扇出 5+ 子任务并全部收敛","acceptance_criteria":["全部子任务 completed","主分支含全部 task 文件"]}`}
		case alreadyCalled(msgs, "update_goal"):
			// 目标已收敛：输出总结文本（goal enforcer 放行最终答复）。
			return loopDecision{text: "全部后台子任务已完成并合并回主分支，目标契约已收敛为 complete。"}
		case toolResults <= fanoutCount(msgs):
			k := toolResults // 第 k 次 spawn 对应 task-k
			return loopDecision{isTool: true, name: "start_task_run",
				args: fmt.Sprintf(`{"task":"请创建文件 task-%d.txt（内容为 DONE-%d），然后调用 git_commit 提交（message 为 add task-%d.txt），完成后用中文简要说明","mode":"async"}`, k, k, k)}
		default:
			// 已扇出完毕：分析最近一次 list_task_runs 结果决定继续轮询还是收敛。
			if allRunsTerminal(lastToolContent) {
				return loopDecision{isTool: true, name: "update_goal",
					args: `{"status":"complete","progress":"全部子任务已完成并合并回主分支"}`}
			}
			return loopDecision{isTool: true, name: "list_task_runs", args: `{}`}
		}
	case names["file_write"]:
		// Worker：task-k 编号从任务描述解析 → 写文件 → 提交 → 确认。
		k := 1
		for _, m := range msgs {
			if mm, ok := m.(map[string]any); ok && mm["role"] == "user" {
				if c, ok := mm["content"].(string); ok {
					if m2 := taskNoRe.FindStringSubmatch(c); len(m2) > 1 {
						fmt.Sscanf(m2[1], "%d", &k)
					}
				}
			}
		}
		switch toolResults {
		case 0:
			return loopDecision{isTool: true, name: "file_write",
				args: fmt.Sprintf(`{"path":"task-%d.txt","content":"DONE-%d"}`, k, k)}
		case 1:
			return loopDecision{isTool: true, name: "git_commit",
				args: fmt.Sprintf(`{"message":"add task-%d.txt"}`, k)}
		case 2:
			return loopDecision{isTool: true, name: "git_status", args: `{}`}
		default:
			return loopDecision{text: fmt.Sprintf("已完成 task-%d：文件已写入并提交。", k)}
		}
	default:
		return loopDecision{text: "无可用工具，结束。"}
	}
}

// fanoutCount 从 Orchestrator 的 user 指令里解析扇出总数（prompt 含 task-1 到 task-N）。
func fanoutCount(msgs []any) int {
	n := 6
	for _, m := range msgs {
		if mm, ok := m.(map[string]any); ok && mm["role"] == "user" {
			if c, ok := mm["content"].(string); ok {
				parts := strings.Split(c, "task-")
				if len(parts) >= 2 {
					var last int
					for _, p := range parts[1:] {
						var v int
						if _, err := fmt.Sscanf(p, "%d", &v); err == nil && v > 0 {
							last = v
						}
					}
					if last > 0 {
						n = last
					}
				}
			}
		}
	}
	return n
}

// alreadyCalled 报告当前会话历史中是否已调用过指定工具（防脚本重复决策）。
func alreadyCalled(msgs []any, toolName string) bool {
	for _, m := range msgs {
		mm, ok := m.(map[string]any)
		if !ok {
			continue
		}
		// assistant 消息的 tool_calls（函数名）。
		if tcs, ok := mm["tool_calls"].([]any); ok {
			for _, tc := range tcs {
				if tcm, ok := tc.(map[string]any); ok {
					if fn, ok := tcm["function"].(map[string]any); ok {
						if n, ok := fn["name"].(string); ok && n == toolName {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

// allRunsTerminal 解析 list_task_runs 的 JSON 结果（{"runs":[{status:...}]}），
// 判断是否所有 run 均进入终态（completed/canceled/failed）。无 runs 视为未收敛。
func allRunsTerminal(content string) bool {
	if content == "" {
		return false
	}
	var res struct {
		Runs []struct {
			Status string `json:"status"`
		} `json:"runs"`
	}
	if err := json.Unmarshal([]byte(content), &res); err != nil {
		// 工具结果可能是纯文本包装；兜底按「是否含 completed」计数判断。
		return strings.Contains(content, `"completed"`) && !strings.Contains(content, `"running"`) &&
			!strings.Contains(content, `"queued"`)
	}
	if len(res.Runs) == 0 {
		return false
	}
	for _, r := range res.Runs {
		switch r.Status {
		case "completed", "canceled", "failed":
			continue
		default:
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// 场景 4：SQLite 写锁（多写者并发写 session/message：无死锁、无 lock 错误）
// ---------------------------------------------------------------------------

// TestLoad_SQLiteWriteContention 以文件型 SQLite（对齐生产 SetMaxOpenConns(1)，
// 单写者连接池串行化）验证并发写安全：
//   - 场景 A：N 个 writer 各写自己的 session（GetOrCreateSession + AppendMessage×K）；
//   - 场景 B：8 个 writer 并发写同一 session（模拟同会话多消息同时落库）。
//
// 断言：全部成功、无 "database is locked" / "database table is locked" 错误、
// 行数精确（无丢写）、墙钟 < 上限（无死锁）。多连接写锁行为在报告中说明
// （SQLite 单写者固有，生产单副本 + 单连接池规避）。
func TestLoad_SQLiteWriteContention(t *testing.T) {
	writers := loadEnvInt("LOAD_SQLITE_WRITERS", 16)
	perWriter := 25

	db := newLoadDB(t)
	defer closeLoadDB(t, db)

	var (
		mu        sync.Mutex
		lockErrs  int
		otherErrs int
		msgRows   int64
	)

	// 场景 A：各写各的 session。
	start := time.Now()
	var wg sync.WaitGroup
	for i := 1; i <= writers; i++ {
		wg.Add(1)
		go func(uid int) {
			defer wg.Done()
			key := fmt.Sprintf("sess-lock-%d", uid)
			sess, err := repo.GetOrCreateSession(db, uint(uid), key)
			if err != nil {
				mu.Lock()
				otherErrs++
				mu.Unlock()
				return
			}
			for j := 0; j < perWriter; j++ {
				if err := repo.AppendMessage(db, sess.ID, "user", fmt.Sprintf("msg-%d-%d", uid, j)); err != nil {
					if isSQLiteLockErr(err) {
						mu.Lock()
						lockErrs++
						mu.Unlock()
					} else {
						mu.Lock()
						otherErrs++
						mu.Unlock()
					}
				}
			}
		}(i)
	}
	// 场景 B：8 个 writer 并发写同一 session（同会话高并发落库）。
	var sharedSessID uint
	{
		sess, err := repo.GetOrCreateSession(db, uint(writers+1), "sess-shared")
		if err != nil {
			t.Fatalf("创建共享 session: %v", err)
		}
		sharedSessID = sess.ID
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(wid int) {
			defer wg.Done()
			for j := 0; j < perWriter; j++ {
				if err := repo.AppendMessage(db, sharedSessID, "assistant", fmt.Sprintf("shared-%d-%d", wid, j)); err != nil {
					if isSQLiteLockErr(err) {
						mu.Lock()
						lockErrs++
						mu.Unlock()
					} else {
						mu.Lock()
						otherErrs++
						mu.Unlock()
					}
				}
			}
		}(i)
	}
	wg.Wait()
	wall := time.Since(start)

	if err := db.Model(&servermodel.Message{}).Count(&msgRows).Error; err != nil {
		t.Fatalf("统计消息行数: %v", err)
	}
	wantRows := int64(writers*perWriter + 8*perWriter)
	line := fmt.Sprintf("[LOAD] 场景=SQLite写锁 写者=%d+8 样本=%d 消息行=%d/%d lock错误=%d 其他错误=%d 墙钟=%s",
		writers, wantRows, msgRows, wantRows, lockErrs, otherErrs, wall.Round(time.Millisecond))
	fmt.Println(line)
	writeLoadReport(t, []string{line})

	if otherErrs > 0 {
		t.Fatalf("SQLite 并发写其他错误 %d 次", otherErrs)
	}
	if lockErrs > 0 {
		t.Fatalf("SQLite 并发写出现 %d 次 lock 错误（生产单连接池下不应发生）", lockErrs)
	}
	if msgRows != wantRows {
		t.Fatalf("消息行数不符：got=%d want=%d（存在丢写）", msgRows, wantRows)
	}
	if wall > 60*time.Second {
		t.Fatalf("SQLite 并发写墙钟超上限（60s），疑似死锁：%v", wall)
	}
	t.Logf("✅ SQLite 写锁：%d 写者各自会话 + 8 写者共享会话共 %d 条消息全部落库，无死锁、无 lock 错误", writers, wantRows)
}

// newLoadDB 建一个文件型 SQLite（生产同款：SetMaxOpenConns(1)），迁移 Session/Message 表。
func newLoadDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "load.db")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open load db: %v", err)
	}
	if s, err := db.DB(); err == nil {
		s.SetMaxOpenConns(1) // 生产配置：SQLite 单写者，连接池串行化
	}
	if err := db.AutoMigrate(&servermodel.Session{}, &servermodel.Message{}); err != nil {
		t.Fatalf("migrate load db: %v", err)
	}
	return db
}

// closeLoadDB 关闭底层连接池（Windows 下 gorm 持有文件句柄，不关会导致 t.TempDir 清理失败）。
func closeLoadDB(t *testing.T, db *gorm.DB) {
	if s, err := db.DB(); err == nil {
		_ = s.Close()
	}
}

// isSQLiteLockErr 判定 SQLite 写锁错误（"database is locked" / "database table is locked"）。
func isSQLiteLockErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "database is locked") || strings.Contains(msg, "database table is locked")
}

// listDir 列出目录下全部条目（压测失败诊断用）。
func listDir(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []string{"<read err: " + err.Error() + ">"}
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// gitBranch 返回仓库全部分支（含 worktree 分支，压测失败诊断用）。
func gitBranch(t *testing.T, repoDir string) string {
	t.Helper()
	ex, err := codectool.NewGitExecutor(repoDir, nil, nil, executor.ModeUnattended)
	if err != nil {
		return "<NewGitExecutor: " + err.Error() + ">"
	}
	out, rerr := ex.Run(context.Background(), "git branch -a")
	if rerr != nil {
		return "<git branch -a: " + rerr.Error() + ">"
	}
	return out.Stdout
}
