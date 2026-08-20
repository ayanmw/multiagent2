package taskrun

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	taskrunruntime "trpc.group/trpc-go/trpc-agent-go/agent/taskrun"

	"github.com/ayanmw/multiagent2/server/internal/taskrun/queue"
)

// fakeRunFunc 生成一个「执行 N ms 后返回固定文本」的 fake worker 执行函数，
// 记录执行次数与结果。
func fakeRunFunc(execCount *map[string]int, mu *sync.Mutex, delay time.Duration) func(ctx context.Context, run taskrunruntime.Run) (string, error) {
	return func(ctx context.Context, run taskrunruntime.Run) (string, error) {
		mu.Lock()
		(*execCount)[run.ID]++
		mu.Unlock()
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(delay):
		}
		return "ok:" + run.ID, nil
	}
}

// waitAllTerminal 轮询队列直到全部 run 终态（墙钟上限兜底死锁）。
func waitAllTerminal(t *testing.T, ctrl *QueueService, expect int) []taskrunruntime.Run {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		runs, err := ctrl.List(context.Background(), taskrunruntime.ListFilter{})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(runs) == expect {
			all := true
			for _, r := range runs {
				if !r.Status.IsTerminal() {
					all = false
					break
				}
			}
			if all {
				return runs
			}
		}
		time.Sleep(30 * time.Millisecond)
	}
	// 诊断：打印当前状态
	runs, _ := ctrl.List(context.Background(), taskrunruntime.ListFilter{})
	for _, r := range runs {
		t.Logf("未收敛: id=%s status=%s err=%s", r.ID, r.Status, r.Error)
	}
	t.Fatalf("等待 %d 个任务终态超时（当前 %d 个）", expect, len(runs))
	return nil
}

func waitStatus(t *testing.T, q queue.Queue, runID string, want taskrunruntime.Status) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		row, err := q.Get(context.Background(), runID)
		if err == nil && row.Run.Status == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	row, _ := q.Get(context.Background(), runID)
	status := ""
	if row != nil {
		status = string(row.Run.Status)
	}
	t.Fatalf("等待 run %s 状态 %s 超时（当前 %q）", runID, want, status)
}

// recordingObserver 收集 OnRunUpdate 通知（模拟 M2-05 worktree 钩子的观察者）。
type recordingObserver struct {
	mu       sync.Mutex
	terminal []string
}

func (o *recordingObserver) OnRunUpdate(ctx context.Context, run taskrunruntime.Run) {
	if !run.Status.IsTerminal() {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.terminal = append(o.terminal, run.ID+":"+string(run.Status))
}

func (o *recordingObserver) terminalCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.terminal)
}

// TestQueueService_TwoNodes_ConcurrentFanout 核心验收：两个节点共享同一外部队列，
// 并发派生子任务全部收敛，每个任务恰好执行一次（无重复领取/执行）。
func TestQueueService_TwoNodes_ConcurrentFanout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.db")
	qA, err := queue.NewSQLiteQueueFile(path, queue.DefaultTableName)
	if err != nil {
		t.Fatal(err)
	}
	defer qA.Close()
	qB, err := queue.NewSQLiteQueueFile(path, queue.DefaultTableName)
	if err != nil {
		t.Fatal(err)
	}
	defer qB.Close()

	execCount := map[string]int{}
	var mu sync.Mutex
	var nodeAExec, nodeBExec atomic.Int64
	obs := &recordingObserver{}

	// 节点 A、B 各自独立的 QueueService，共享同一 SQLite 文件队列。
	sA := NewQueueService(qA, "node-a", QueueOptions{
		PollInterval:  30 * time.Millisecond,
		LeaseDuration: 5 * time.Second,
		MaxAttempts:   3,
		Observer:      obs,
		RunFunc: func(ctx context.Context, run taskrunruntime.Run) (string, error) {
			nodeAExec.Add(1)
			return fakeRunFunc(&execCount, &mu, 20*time.Millisecond)(ctx, run)
		},
	})
	sB := NewQueueService(qB, "node-b", QueueOptions{
		PollInterval:  30 * time.Millisecond,
		LeaseDuration: 5 * time.Second,
		MaxAttempts:   3,
		Observer:      obs,
		RunFunc: func(ctx context.Context, run taskrunruntime.Run) (string, error) {
			nodeBExec.Add(1)
			return fakeRunFunc(&execCount, &mu, 20*time.Millisecond)(ctx, run)
		},
	})
	ctx := context.Background()
	sA.Start(ctx)
	sB.Start(ctx)
	defer sA.Close()
	defer sB.Close()

	const total = 12
	for i := 0; i < total; i++ {
		var src *QueueService
		if i%2 == 0 {
			src = sA
		} else {
			src = sB
		}
		run, err := src.Spawn(ctx, taskrunruntime.SpawnRequest{
			ID:              fmt.Sprintf("run-%02d", i),
			OwnerUserID:     "7",
			ParentSessionID: "sess-parent",
			Task:            fmt.Sprintf("task-%02d", i),
			Timeout:         10 * time.Second,
		})
		if err != nil {
			t.Fatalf("Spawn: %v", err)
		}
		if run.Status != taskrunruntime.StatusQueued {
			t.Fatalf("spawn 后应为 queued, got %s", run.Status)
		}
	}

	runs := waitAllTerminal(t, sA, total)
	for _, r := range runs {
		if r.Status != taskrunruntime.StatusCompleted {
			t.Fatalf("run %s 应为 completed, got %s (err=%s)", r.ID, r.Status, r.Error)
		}
		if r.Result != "ok:"+r.ID {
			t.Fatalf("run %s result=%q 不符", r.ID, r.Result)
		}
	}
	// 每个任务恰好执行一次（两节点无重复领取/执行）。
	mu.Lock()
	defer mu.Unlock()
	for id, n := range execCount {
		if n != 1 {
			t.Errorf("run %s 执行 %d 次（应为 1 次，存在重复执行）", id, n)
		}
	}
	if len(execCount) != total {
		t.Errorf("执行记录 %d/%d", len(execCount), total)
	}
	// 两节点都实际执行过任务（分布负载）。
	if nodeAExec.Load() == 0 || nodeBExec.Load() == 0 {
		t.Errorf("任务未在两节点间分布: A=%d B=%d", nodeAExec.Load(), nodeBExec.Load())
	}
	// observer 收到全部 terminal 通知（worktree 钩子场景）。
	if obs.terminalCount() != total {
		t.Errorf("observer 收到 %d 个终态通知（应为 %d）", obs.terminalCount(), total)
	}
	// Wait 从另一节点视角也应能拿到终态。
	run, err := sB.Wait(ctx, "run-00")
	if err != nil {
		t.Fatalf("Wait run-00: %v", err)
	}
	if run.Status != taskrunruntime.StatusCompleted {
		t.Fatalf("Wait 应返回 completed, got %s", run.Status)
	}
}

// TestQueueService_NodeCrash_Requeue 核心验收：节点 A 领取任务后「进程崩溃」——
// 不续约 lease、不写终态、poller 消失（直接丢弃 A 的实例），lease 过期后任务被
// 节点 B 重拾并成功执行（Result=ok-b，恰好执行 1 次）。
func TestQueueService_NodeCrash_Requeue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.db")
	qA, err := queue.NewSQLiteQueueFile(path, queue.DefaultTableName)
	if err != nil {
		t.Fatal(err)
	}
	defer qA.Close()
	qB, err := queue.NewSQLiteQueueFile(path, queue.DefaultTableName)
	if err != nil {
		t.Fatal(err)
	}
	defer qB.Close()

	ctx := context.Background()
	// 节点 A 领取任务（lease 200ms 后过期）：模拟「A 已领取 → A 崩溃消失」
	//（无续约、无终态写、poller 不启动）。
	if err := qA.Enqueue(ctx, queue.Row{Run: taskrunruntime.Run{
		ID: "run-crash", OwnerUserID: "7", ParentSessionID: "sess-p", Task: "t",
		Status: taskrunruntime.StatusQueued, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}}); err != nil {
		t.Fatal(err)
	}
	claimed, err := qA.ClaimNext(ctx, "node-a", time.Now().Add(200*time.Millisecond))
	if err != nil || claimed == nil {
		t.Fatalf("节点 A 领取失败: row=%v err=%v", claimed, err)
	}
	if claimed.Run.Status != taskrunruntime.StatusRunning {
		t.Fatalf("领取后应为 running, got %s", claimed.Run.Status)
	}
	// 节点 A「崩溃」：实例不再使用（不续约/不写终态/不启动 poller）。

	// 节点 B：正常 poller，检测到 lease 过期后重拾并执行。
	var bExec atomic.Int64
	sB := NewQueueService(qB, "node-b", QueueOptions{
		PollInterval:  20 * time.Millisecond,
		LeaseDuration: 5 * time.Second,
		MaxAttempts:   3,
		RunFunc: func(ctx context.Context, run taskrunruntime.Run) (string, error) {
			bExec.Add(1)
			return "ok-b", nil
		},
	})
	sB.Start(ctx)
	defer sB.Close()

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		row, err := qB.Get(ctx, "run-crash")
		if err == nil && row.Run.Status == taskrunruntime.StatusCompleted {
			if row.Run.Result != "ok-b" {
				t.Fatalf("重拾执行结果应为 ok-b, got %q", row.Run.Result)
			}
			if bExec.Load() != 1 {
				t.Fatalf("节点 B 应执行 1 次, got %d", bExec.Load())
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	row, _ := qB.Get(ctx, "run-crash")
	st := ""
	if row != nil {
		st = string(row.Run.Status)
	}
	t.Fatalf("崩溃任务未被重拾完成（当前状态 %q）", st)
}

// TestQueueService_RequeueExhausted 验证：反复崩溃的任务在重拾次数耗尽后置 failed，
// 不再被执行。
func TestQueueService_RequeueExhausted(t *testing.T) {
	q := queue.NewMemoryQueue()
	ctx := context.Background()
	execCount := map[string]int{}
	var mu sync.Mutex

	s := NewQueueService(q, "node-a", QueueOptions{
		PollInterval:  20 * time.Millisecond,
		LeaseDuration: 100 * time.Millisecond,
		MaxAttempts:   1, // 只允许 1 次重拾
		RunFunc: func(ctx context.Context, run taskrunruntime.Run) (string, error) {
			mu.Lock()
			execCount[run.ID]++
			mu.Unlock()
			runtime.Goexit() // 每次执行都「崩溃」
			return "", nil
		},
	})
	s.Start(ctx)
	defer s.Close()

	if _, err := s.Spawn(ctx, taskrunruntime.SpawnRequest{
		ID: "run-x", OwnerUserID: "7", ParentSessionID: "p", Task: "t",
	}); err != nil {
		t.Fatal(err)
	}
	// 原始执行 1 次 + 重拾 1 次（attempts 0→1，>= maxAttempts=1）→ failed。
	waitStatus(t, q, "run-x", taskrunruntime.StatusFailed)
	mu.Lock()
	defer mu.Unlock()
	if n := execCount["run-x"]; n != 2 {
		t.Fatalf("应执行 2 次（原始+1 次重拾）后失败, got %d", n)
	}
}

// TestQueueService_Cancel 验证取消语义：
//   - running 任务 Cancel → canceling → 执行节点终止 → canceled；
//   - queued 任务 Cancel → 直接 canceled（不执行）。
func TestQueueService_Cancel(t *testing.T) {
	q := queue.NewMemoryQueue()
	ctx := context.Background()
	started := make(chan string, 8)

	s := NewQueueService(q, "node-a", QueueOptions{
		PollInterval:  20 * time.Millisecond,
		LeaseDuration: 5 * time.Second,
		MaxAttempts:   3,
		RunFunc: func(ctx context.Context, run taskrunruntime.Run) (string, error) {
			select {
			case started <- run.ID:
			default:
			}
			<-ctx.Done() // 阻塞等待取消
			return "", ctx.Err()
		},
	})
	s.Start(ctx)
	defer s.Close()

	// 1) running 任务取消
	if _, err := s.Spawn(ctx, taskrunruntime.SpawnRequest{
		ID: "run-cancel", OwnerUserID: "7", ParentSessionID: "p", Task: "t",
	}); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, q, "run-cancel", taskrunruntime.StatusRunning)
	run, changed, err := s.Cancel(ctx, "run-cancel")
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if !changed || run.Status != taskrunruntime.StatusCanceling {
		t.Fatalf("Cancel 应返回 changed=true status=canceling, got changed=%v status=%s", changed, run.Status)
	}
	waitStatus(t, q, "run-cancel", taskrunruntime.StatusCanceled)

	// 2) queued 任务取消（先关掉 poller 领取，或直接构造一个 queued 任务）
	pause := make(chan struct{})
	defer close(pause)
	q2 := queue.NewMemoryQueue()
	s2 := NewQueueService(q2, "node-b", QueueOptions{
		PollInterval:  time.Hour, // 不轮询 → 任务保持 queued
		LeaseDuration: time.Hour,
		MaxAttempts:   3,
		RunFunc: func(ctx context.Context, run taskrunruntime.Run) (string, error) {
			<-pause
			return "should-not-run", nil
		},
	})
	s2.Start(ctx)
	defer s2.Close()
	if _, err := s2.Spawn(ctx, taskrunruntime.SpawnRequest{
		ID: "run-queued", OwnerUserID: "7", ParentSessionID: "p", Task: "t",
	}); err != nil {
		t.Fatal(err)
	}
	run2, changed2, err := s2.Cancel(ctx, "run-queued")
	if err != nil {
		t.Fatalf("Cancel queued: %v", err)
	}
	if !changed2 || run2.Status != taskrunruntime.StatusCanceled {
		t.Fatalf("Cancel queued 应直接 canceled, got changed=%v status=%s", changed2, run2.Status)
	}
	// 终态保持 canceled 且从未执行（RunFunc 未被调用）。
	waitStatus(t, q2, "run-queued", taskrunruntime.StatusCanceled)
}

// TestQueueService_WaitTimeoutCtx 验证 Wait 支持 ctx 取消。
func TestQueueService_WaitTimeoutCtx(t *testing.T) {
	q := queue.NewMemoryQueue()
	ctx := context.Background()
	s := NewQueueService(q, "node-a", QueueOptions{
		PollInterval:  time.Hour, // 不执行
		LeaseDuration: time.Hour,
		MaxAttempts:   3,
	})
	s.Start(ctx)
	defer s.Close()
	if _, err := s.Spawn(ctx, taskrunruntime.SpawnRequest{
		ID: "run-w", OwnerUserID: "7", ParentSessionID: "p", Task: "t",
	}); err != nil {
		t.Fatal(err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	if _, err := s.Wait(waitCtx, "run-w"); err == nil {
		t.Fatal("Wait 应在 ctx 超时后返回错误")
	}
}
