package queue

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	taskrun "trpc.group/trpc-go/trpc-agent-go/agent/taskrun"
)

func newTestSQLite(t *testing.T) *SQLiteQueue {
	t.Helper()
	path := filepath.Join(t.TempDir(), "queue.db")
	q, err := NewSQLiteQueueFile(path, DefaultTableName)
	if err != nil {
		t.Fatalf("NewSQLiteQueueFile: %v", err)
	}
	t.Cleanup(func() { q.Close() })
	return q
}

func enqueueTask(t *testing.T, q Queue, id string, status taskrun.Status, lease time.Time, attempts int) {
	t.Helper()
	now := time.Now()
	err := q.Enqueue(context.Background(), Row{
		Run: taskrun.Run{
			ID: id, OwnerUserID: "1", ParentSessionID: "sess-p", Task: "task-" + id,
			Status: status, CreatedAt: now, UpdatedAt: now,
		},
		LeaseExpiresAt: lease,
		Attempts:       attempts,
	})
	if err != nil {
		t.Fatalf("Enqueue %s: %v", id, err)
	}
}

func TestSQLiteQueue_EnqueueGetList(t *testing.T) {
	q := newTestSQLite(t)
	ctx := context.Background()
	enqueueTask(t, q, "r1", taskrun.StatusQueued, time.Time{}, 0)
	enqueueTask(t, q, "r2", taskrun.StatusQueued, time.Time{}, 0)

	// 重复入队 → ErrRunAlreadyExists
	if err := q.Enqueue(ctx, Row{Run: taskrun.Run{ID: "r1", Status: taskrun.StatusQueued}}); !errors.Is(err, ErrRunAlreadyExists) {
		t.Fatalf("重复入队应返回 ErrRunAlreadyExists, got %v", err)
	}

	row, err := q.Get(ctx, "r1")
	if err != nil {
		t.Fatalf("Get r1: %v", err)
	}
	if row.Run.ID != "r1" || row.Run.Status != taskrun.StatusQueued || row.Run.Task != "task-r1" {
		t.Fatalf("Get 内容不符: %+v", row.Run)
	}

	// Get 不存在
	if _, err := q.Get(ctx, "nope"); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("Get 不存在应返回 ErrRunNotFound, got %v", err)
	}

	// List 全量（按 updated_at 降序）
	runs, err := q.List(ctx, taskrun.ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("List 应有 2 条, got %d", len(runs))
	}

	// List 按状态过滤
	queued, err := q.List(ctx, taskrun.ListFilter{Status: taskrun.StatusQueued})
	if err != nil || len(queued) != 2 {
		t.Fatalf("List queued 应有 2 条: %v", err)
	}
	failed, err := q.List(ctx, taskrun.ListFilter{Status: taskrun.StatusFailed})
	if err != nil || len(failed) != 0 {
		t.Fatalf("List failed 应有 0 条: %v", err)
	}
}

// TestSQLiteQueue_ClaimNext_ConcurrentNoDuplicate 验证「两节点共享同一队列文件
// 并发领取不重复」——这是 M8-03 多节点正确性的根基。
func TestSQLiteQueue_ClaimNext_ConcurrentNoDuplicate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.db")
	q1, err := NewSQLiteQueueFile(path, DefaultTableName)
	if err != nil {
		t.Fatal(err)
	}
	defer q1.Close()
	q2, err := NewSQLiteQueueFile(path, DefaultTableName)
	if err != nil {
		t.Fatal(err)
	}
	defer q2.Close()

	const total = 20
	for i := 0; i < total; i++ {
		enqueueTask(t, q1, fmt.Sprintf("r-%02d", i), taskrun.StatusQueued, time.Time{}, 0)
	}

	var mu sync.Mutex
	claimed := map[string]string{} // runID → workerID
	var wg sync.WaitGroup
	claimAll := func(q *SQLiteQueue, worker string) {
		defer wg.Done()
		for {
			row, err := q.ClaimNext(context.Background(), worker, time.Now().Add(time.Hour))
			if err != nil {
				t.Errorf("%s ClaimNext: %v", worker, err)
				return
			}
			if row == nil {
				return
			}
			mu.Lock()
			if prev, dup := claimed[row.Run.ID]; dup {
				t.Errorf("run %s 被重复领取: %s 与 %s", row.Run.ID, prev, worker)
			} else {
				claimed[row.Run.ID] = worker
			}
			mu.Unlock()
		}
	}
	wg.Add(2)
	go claimAll(q1, "node-a")
	go claimAll(q2, "node-b")
	wg.Wait()

	if len(claimed) != total {
		t.Fatalf("应领取 %d 个任务, 实际 %d（有任务丢失或被重复）", total, len(claimed))
	}
	// 每个任务状态应为 running 且绑定 worker
	for id, worker := range claimed {
		row, err := q1.Get(context.Background(), id)
		if err != nil {
			t.Fatalf("Get %s: %v", id, err)
		}
		if row.Run.Status != taskrun.StatusRunning || row.WorkerID != worker {
			t.Fatalf("run %s 应为 running+worker=%s, got status=%s worker=%s",
				id, worker, row.Run.Status, row.WorkerID)
		}
	}
}

func TestSQLiteQueue_UpdateStatusIf(t *testing.T) {
	q := newTestSQLite(t)
	ctx := context.Background()
	enqueueTask(t, q, "r1", taskrun.StatusQueued, time.Time{}, 0)

	// 匹配 expected → 更新成功
	ok, err := q.UpdateStatusIf(ctx, "r1", taskrun.StatusQueued, taskrun.StatusCanceled, nil)
	if err != nil || !ok {
		t.Fatalf("queued→canceled 应成功: ok=%v err=%v", ok, err)
	}
	row, _ := q.Get(ctx, "r1")
	if row.Run.Status != taskrun.StatusCanceled {
		t.Fatalf("状态应为 canceled, got %s", row.Run.Status)
	}

	// expected 不匹配（已是 canceled）→ 更新失败且状态不变
	ok, err = q.UpdateStatusIf(ctx, "r1", taskrun.StatusQueued, taskrun.StatusCompleted, nil)
	if err != nil || ok {
		t.Fatalf("状态已变时应更新失败: ok=%v err=%v", ok, err)
	}

	// 携带 Row 全量更新（running→completed，清 lease）
	enqueueTask(t, q, "r2", taskrun.StatusRunning, time.Now().Add(time.Hour), 1)
	finalRow := Row{
		Run: taskrun.Run{ID: "r2", Status: taskrun.StatusCompleted, Result: "ok", UpdatedAt: time.Now()},
	}
	ok, err = q.UpdateStatusIf(ctx, "r2", taskrun.StatusRunning, taskrun.StatusCompleted, &finalRow)
	if err != nil || !ok {
		t.Fatalf("running→completed 应成功: ok=%v err=%v", ok, err)
	}
	row, _ = q.Get(ctx, "r2")
	if row.Run.Status != taskrun.StatusCompleted || row.Run.Result != "ok" || row.WorkerID != "" {
		t.Fatalf("终态行不符: status=%s result=%q worker=%q", row.Run.Status, row.Run.Result, row.WorkerID)
	}
}

func TestSQLiteQueue_RequeueExpired(t *testing.T) {
	q := newTestSQLite(t)
	ctx := context.Background()
	now := time.Now()

	// r1: running + lease 过期 + attempts=0 → queued 重拾
	enqueueTask(t, q, "r1", taskrun.StatusRunning, now.Add(-time.Second), 0)
	// r2: running + lease 过期 + attempts=3（>= max 3）→ failed
	enqueueTask(t, q, "r2", taskrun.StatusRunning, now.Add(-time.Second), 3)
	// r3: canceling + 无 lease → canceled（不重试）
	enqueueTask(t, q, "r3", taskrun.StatusCanceling, time.Time{}, 0)
	// r4: running + lease 未过期 → 不动
	enqueueTask(t, q, "r4", taskrun.StatusRunning, now.Add(time.Hour), 0)
	// r5: queued（正常待执行）→ 不动
	enqueueTask(t, q, "r5", taskrun.StatusQueued, time.Time{}, 0)

	n, err := q.RequeueExpired(ctx, now, 3)
	if err != nil {
		t.Fatalf("RequeueExpired: %v", err)
	}
	if n != 3 {
		t.Fatalf("应处理 3 个任务, got %d", n)
	}

	r1, _ := q.Get(ctx, "r1")
	if r1.Run.Status != taskrun.StatusQueued || r1.Attempts != 1 || r1.WorkerID != "" {
		t.Fatalf("r1 应重拾为 queued attempts=1, got %+v", r1)
	}
	r2, _ := q.Get(ctx, "r2")
	if r2.Run.Status != taskrun.StatusFailed {
		t.Fatalf("r2 重试耗尽应为 failed, got %s", r2.Run.Status)
	}
	r3, _ := q.Get(ctx, "r3")
	if r3.Run.Status != taskrun.StatusCanceled {
		t.Fatalf("r3 canceling 应为 canceled, got %s", r3.Run.Status)
	}
	r4, _ := q.Get(ctx, "r4")
	if r4.Run.Status != taskrun.StatusRunning {
		t.Fatalf("r4 lease 未过期不应被处理, got %s", r4.Run.Status)
	}
	r5, _ := q.Get(ctx, "r5")
	if r5.Run.Status != taskrun.StatusQueued {
		t.Fatalf("r5 queued 不应被处理, got %s", r5.Run.Status)
	}

	// 二次运行：重拾后的 r1（attempts=1 < 3）继续重拾，直到超限
	q2 := newTestSQLite(t) // 同文件? 不行——newTestSQLite 是新临时文件
	_ = q2
	n2, err := q.RequeueExpired(ctx, now, 3)
	if err != nil {
		t.Fatal(err)
	}
	_ = n2
	// r1 已变 queued（无 lease），running 的过期任务已处理完；再次调用应处理 0 个
	if n2 != 0 {
		t.Fatalf("二次 RequeueExpired 应处理 0, got %d", n2)
	}
}

func TestMemoryQueue_ClaimNoDuplicate(t *testing.T) {
	q := NewMemoryQueue()
	ctx := context.Background()
	const total = 10
	for i := 0; i < total; i++ {
		enqueueTask(t, q, fmt.Sprintf("m-%02d", i), taskrun.StatusQueued, time.Time{}, 0)
	}
	var mu sync.Mutex
	claimed := map[string]string{}
	var wg sync.WaitGroup
	claimAll := func(worker string) {
		defer wg.Done()
		for {
			row, err := q.ClaimNext(ctx, worker, time.Now().Add(time.Hour))
			if err != nil {
				t.Errorf("%s: %v", worker, err)
				return
			}
			if row == nil {
				return
			}
			mu.Lock()
			if _, dup := claimed[row.Run.ID]; dup {
				t.Errorf("run %s 重复领取", row.Run.ID)
			} else {
				claimed[row.Run.ID] = worker
			}
			mu.Unlock()
		}
	}
	wg.Add(2)
	go claimAll("node-a")
	go claimAll("node-b")
	wg.Wait()
	if len(claimed) != total {
		t.Fatalf("应领取 %d, 实际 %d", total, len(claimed))
	}
}
