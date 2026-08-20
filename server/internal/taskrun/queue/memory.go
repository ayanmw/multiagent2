package queue

import (
	"context"
	"sort"
	"sync"
	"time"

	taskrun "trpc.group/trpc-go/trpc-agent-go/agent/taskrun"
)

// MemoryQueue 是基于内存的 Queue 实现，供单测与无外部存储的轻量场景。
// 注意：多节点语义依赖「多个调用方共享同一实例」——单测中两个 QueueService
// 共享同一 MemoryQueue 即可模拟两节点（真实部署请用 SQLiteQueue 共享文件）。
type MemoryQueue struct {
	mu   sync.Mutex
	rows map[string]*Row
	now  func() time.Time
}

var _ Queue = (*MemoryQueue)(nil)

// NewMemoryQueue 创建内存队列。
func NewMemoryQueue() *MemoryQueue {
	return &MemoryQueue{rows: make(map[string]*Row), now: time.Now}
}

// SetClock 注入时钟（测试用）。
func (q *MemoryQueue) SetClock(now func() time.Time) {
	if q != nil && now != nil {
		q.now = now
	}
}

func (q *MemoryQueue) Enqueue(ctx context.Context, row Row) error {
	if q == nil {
		return nil
	}
	if row.Run.ID == "" {
		return ErrRunAlreadyExists
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, ok := q.rows[row.Run.ID]; ok {
		return ErrRunAlreadyExists
	}
	copied := cloneRow(row)
	q.rows[row.Run.ID] = &copied
	return nil
}

func (q *MemoryQueue) ClaimNext(ctx context.Context, workerID string, leaseUntil time.Time) (*Row, error) {
	if q == nil {
		return nil, nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	// 按 created_at 升序找第一个 queued。
	ids := make([]string, 0, len(q.rows))
	for id := range q.rows {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		return q.rows[ids[i]].Run.CreatedAt.Before(q.rows[ids[j]].Run.CreatedAt)
	})
	for _, id := range ids {
		row := q.rows[id]
		if row.Run.Status != taskrun.StatusQueued {
			continue
		}
		now := q.now()
		row.Run.Status = taskrun.StatusRunning
		row.Run.UpdatedAt = now
		row.WorkerID = workerID
		row.LeaseExpiresAt = leaseUntil
		out := cloneRow(*row)
		return &out, nil
	}
	return nil, nil
}

func (q *MemoryQueue) UpdateStatusIf(ctx context.Context, runID string, expected, newStatus taskrun.Status, row *Row) (bool, error) {
	if q == nil {
		return false, nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	cur, ok := q.rows[runID]
	if !ok || cur.Run.Status != expected {
		return false, nil
	}
	if row != nil {
		copied := cloneRow(*row)
		q.rows[runID] = &copied
	} else {
		cur.Run.Status = newStatus
		cur.Run.UpdatedAt = q.now()
		cur.WorkerID = ""
		cur.LeaseExpiresAt = time.Time{}
	}
	return true, nil
}

func (q *MemoryQueue) RequeueExpired(ctx context.Context, now time.Time, maxAttempts int) (int, error) {
	if q == nil {
		return 0, nil
	}
	if maxAttempts < 0 {
		maxAttempts = 0
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	total := 0
	for _, row := range q.rows {
		expired := row.LeaseExpiresAt.IsZero() || row.LeaseExpiresAt.Before(now)
		if !expired {
			continue
		}
		switch row.Run.Status {
		case taskrun.StatusCanceling:
			row.Run.Status = taskrun.StatusCanceled
			row.Run.UpdatedAt = now
			row.WorkerID = ""
			row.LeaseExpiresAt = time.Time{}
			total++
		case taskrun.StatusRunning:
			if row.Attempts >= maxAttempts {
				row.Run.Status = taskrun.StatusFailed
				row.Run.UpdatedAt = now
				row.WorkerID = ""
				row.LeaseExpiresAt = time.Time{}
			} else {
				row.Run.Status = taskrun.StatusQueued
				row.Attempts++
				row.Run.UpdatedAt = now
				row.WorkerID = ""
				row.LeaseExpiresAt = time.Time{}
			}
			total++
		}
	}
	return total, nil
}

func (q *MemoryQueue) Get(ctx context.Context, runID string) (*Row, error) {
	if q == nil {
		return nil, ErrRunNotFound
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	row, ok := q.rows[runID]
	if !ok {
		return nil, ErrRunNotFound
	}
	out := cloneRow(*row)
	return &out, nil
}

func (q *MemoryQueue) List(ctx context.Context, filter taskrun.ListFilter) ([]taskrun.Run, error) {
	if q == nil {
		return nil, nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]taskrun.Run, 0, len(q.rows))
	for _, row := range q.rows {
		run := row.Run
		if filter.Status != "" && run.Status != filter.Status {
			continue
		}
		if filter.OwnerUserID != "" && run.OwnerUserID != filter.OwnerUserID {
			continue
		}
		if filter.ParentSessionID != "" && run.ParentSessionID != filter.ParentSessionID {
			continue
		}
		if filter.ParentAppName != "" && run.ParentAppName != filter.ParentAppName {
			continue
		}
		out = append(out, run)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

func cloneRow(row Row) Row {
	out := row
	out.Run.Progress = nil // 不深拷贝 progress（本实现不维护中间 progress）
	return out
}
