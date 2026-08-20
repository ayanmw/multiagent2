// Package queue 提供 taskrun 多节点运行所需的外部队列存储抽象（M8-03）。
//
// 目标：突破框架 inprocess.Service 的单进程限制。inprocess 把 run 记录与执行
// goroutine 都放在本进程内，多副本部署时各副本互不可见、无法分担任务，节点崩溃
// 后任务也随之丢失。queue 包把 run 记录外置到共享存储（默认 SQLite 表
// taskrun_queue），并借「原子领取 + lease 租约」保证多节点下每个任务恰好被一个
// 节点执行：
//
//   - 原子领取：ClaimNext 用单条 UPDATE（WHERE status='queued'）把任务从 queued
//     置为 running 并绑定 worker_id + lease 到期时间。SQLite 单写者模型保证并发
//     节点不会重复领取——第二个节点执行同一 UPDATE 时匹配 0 行（状态已变），本轮
//     不领取，下一轮自然领到下一个任务。
//   - lease 租约：领取后执行节点必须周期性续约（leaseKeepAlive 刷新
//     lease_expires_at）；若节点崩溃（进程消失、未续约），lease 到期后
//     RequeueExpired 会把任务重新置为 queued（attempts+1）供其他节点重拾，
//     超过 MaxAttempts 则置为 failed；canceling 任务直接置为 canceled（不重试，
//     避免「取消后又被执行」）。
//
// 状态机（与框架 taskrun.Status 对齐）：
//
//	queued --ClaimNext--> running --执行完成--> completed / failed
//	  |                        |
//	  +--Cancel--> canceled    +--Cancel--> canceling --执行节点收尾--> canceled
//	  |                        |
//	  +--RequeueExpired--> queued(重拾) / failed(超限)
//
// 本包不依赖框架 runner/agent 执行层，只做存储；执行编排在 taskrun 包的
// QueueService（queue_service.go）完成。
package queue

import (
	"context"
	"errors"
	"time"

	taskrun "trpc.group/trpc-go/trpc-agent-go/agent/taskrun"
)

// DefaultTableName 是 SQLiteQueue 的默认表名。
const DefaultTableName = "taskrun_queue"

// TimeoutKey 是 Run.Metadata 中保存 SpawnRequest.Timeout（纳秒，字符串）的保留键。
// Run 结构本身没有 Timeout 字段，而跨节点执行需要知道子任务超时，故存入 Metadata；
// 其他 Metadata 键不得使用本前缀（taskrun.queue.），避免与用户元数据冲突。
const TimeoutKey = "taskrun.queue.timeout_ns"

// 错误哨兵。
var (
	// ErrRunAlreadyExists 表示队列中已存在同 ID 任务（幂等 Enqueue 冲突）。
	ErrRunAlreadyExists = errors.New("taskrun/queue: run already exists")
	// ErrRunNotFound 表示队列中不存在指定 ID 的任务。
	ErrRunNotFound = errors.New("taskrun/queue: run not found")
)

// Row 是队列中的一行：完整 run 记录 + 分布式状态机字段。
type Row struct {
	// Run 是任务的控制面视图（含 Status/Result/Error 等，json 序列化落库）。
	Run taskrun.Run
	// WorkerID 是当前持有 lease 的节点标识；空表示未领取/已释放。
	WorkerID string
	// LeaseExpiresAt 是 lease 到期时间；零值表示无租约（queued/canceled 等非运行态）。
	LeaseExpiresAt time.Time
	// Attempts 是「崩溃重拾」次数（领取本身不计，RequeueExpired 重拾时 +1）。
	Attempts int
}

// Queue 是外部队列存储抽象，与具体后端（SQLite/内存）解耦，便于单测与替换
// （未来可换 Redis 等）。所有方法须可并发调用。
type Queue interface {
	// Enqueue 幂等插入一个待执行任务（Run.Status 应为 queued）。
	// 同 ID 已存在返回 ErrRunAlreadyExists。
	Enqueue(ctx context.Context, row Row) error

	// ClaimNext 原子领取一个可执行任务（status=queued），置为 running 并绑定
	// workerID + leaseUntil。没有可领取任务时返回 (nil, nil)。
	ClaimNext(ctx context.Context, workerID string, leaseUntil time.Time) (*Row, error)

	// UpdateStatusIf 条件更新：仅当当前 status == expected 时，把行整体更新为
	// newStatus（run_json/worker_id/lease/attempts 全量覆盖，updated_at=now）。
	// 返回是否实际更新（0 行受影响 = 状态已被并发改变，调用方需按 newStatus 语义
	// 决定是否重试其他分支）。
	// 用于 Cancel（queued→canceled / running→canceling）与终态乐观写
	// （running→completed/failed、canceling→canceled），避免双节点写互相覆盖。
	UpdateStatusIf(ctx context.Context, runID string, expected, newStatus taskrun.Status, row *Row) (bool, error)

	// RequeueExpired 处理 lease 过期的任务（节点崩溃恢复），按当前状态分流：
	//   canceling（lease 为空或已过期）                  → canceled（不重试）
	//   running + lease 过期 + attempts < maxAttempts   → queued（attempts+1，可重拾）
	//   running + lease 过期 + attempts >= maxAttempts  → failed（重试耗尽）
	// 返回处理的任务数。
	RequeueExpired(ctx context.Context, now time.Time, maxAttempts int) (int, error)

	// Get 按 ID 取行；不存在返回 ErrRunNotFound。
	Get(ctx context.Context, runID string) (*Row, error)

	// List 按过滤条件列出 run。status 过滤走 SQL（空串=不过滤），其余字段
	// （OwnerUserID/ParentSessionID/ParentAppName）在内存过滤；按 UpdatedAt 降序。
	List(ctx context.Context, filter taskrun.ListFilter) ([]taskrun.Run, error)
}
