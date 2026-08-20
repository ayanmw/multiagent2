package queue

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/glebarez/sqlite" // 纯 Go SQLite 驱动（项目已用，无 CGO）
	taskrun "trpc.group/trpc-go/trpc-agent-go/agent/taskrun"
)

// SQLiteQueue 是基于 SQLite 的外部队列实现（M8-03）。
//
// 连接模型：独立连接池（不共享 repo 的 GORM 连接），DSN 带 WAL + busy_timeout(10s)，
// 进程内 SetMaxOpenConns(1) 串行化本进程写入；多节点（多进程/多机）共享同一 SQLite
// 文件时由 SQLite 文件锁 + busy_timeout 兜底并发写。这是「突破单进程」的最小
// 多节点存储（同一份 DB 文件即可两节点实测），未来可平滑替换为 Redis 队列。
//
// 原子性：ClaimNext 在单条 UPDATE 的 WHERE 中带 status='queued' 条件，SQLite
// 单写者模型保证并发节点不会重复领取（见 queue.go 包注释）。
type SQLiteQueue struct {
	db    *sql.DB
	table string
	now   func() time.Time
}

var _ Queue = (*SQLiteQueue)(nil)

// NewSQLiteQueueFile 打开（不存在则创建）指定 SQLite 文件作为外部队列存储，
// 使用默认表名 DefaultTableName。
func NewSQLiteQueueFile(path string, table string) (*SQLiteQueue, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("taskrun/queue: 空 SQLite 文件路径")
	}
	if err := validateTableName(table); err != nil {
		return nil, err
	}
	// WAL：读写并发友好；busy_timeout：多进程并发写不立即报 SQLITE_BUSY；
	// _txlock=immediate：BeginTx 立即获取写锁（RESERVED），避免「先读快照后写」
	// 的 SQLITE_BUSY_SNAPSHOT 冲突——ClaimNext 事务从开始就是写事务，读到的
	// 始终是最新数据（多节点原子领取的根基）。
	dsn := fmt.Sprintf("file:%s?_txlock=immediate&_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)", path)
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("taskrun/queue: 打开 SQLite: %w", err)
	}
	sqldb.SetMaxOpenConns(1) // SQLite 单写者：进程内串行化，避免同进程写锁竞争
	q := &SQLiteQueue{db: sqldb, table: table, now: time.Now}
	if err := q.ensureTable(context.Background()); err != nil {
		sqldb.Close()
		return nil, err
	}
	return q, nil
}

// Close 释放底层连接池。
func (q *SQLiteQueue) Close() error {
	if q == nil || q.db == nil {
		return nil
	}
	return q.db.Close()
}

// validateTableName 仅允许内部常量表名（字母数字下划线），杜绝注入。
func validateTableName(name string) error {
	if name == "" {
		return fmt.Errorf("taskrun/queue: 空表名")
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			return fmt.Errorf("taskrun/queue: 非法表名 %q", name)
		}
	}
	return nil
}

func (q *SQLiteQueue) ensureTable(ctx context.Context) error {
	// 表结构：run_json 承载完整 Run 记录；status 冗余列加速原子领取/过滤；
	// worker_id/lease_expires_at 是分布式 lease；attempts 是崩溃重拾计数。
	ddl := fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %[1]s (
  "id" TEXT PRIMARY KEY,
  "run_json" TEXT NOT NULL,
  "status" TEXT NOT NULL,
  "worker_id" TEXT,
  "lease_expires_at" DATETIME,
  "attempts" INTEGER NOT NULL DEFAULT 0,
  "created_at" DATETIME NOT NULL,
  "updated_at" DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_%[1]s_status ON %[1]s("status","created_at");`, q.table)
	if _, err := q.db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("taskrun/queue: 建表 %s: %w", q.table, err)
	}
	return nil
}

const rowColumns = `"id","run_json","status","worker_id","lease_expires_at","attempts","created_at","updated_at"`

func (q *SQLiteQueue) Enqueue(ctx context.Context, row Row) error {
	if strings.TrimSpace(row.Run.ID) == "" {
		return fmt.Errorf("taskrun/queue: 空 run id")
	}
	runJSON, err := json.Marshal(row.Run)
	if err != nil {
		return fmt.Errorf("taskrun/queue: 序列化 run: %w", err)
	}
	now := q.now()
	_, err = q.db.ExecContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (%s) VALUES (?,?,?,?,?,?,?,?)`, q.table, rowColumns),
		row.Run.ID, string(runJSON), string(row.Run.Status), nullableString(row.WorkerID),
		nullableTime(row.LeaseExpiresAt), row.Attempts, now, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return ErrRunAlreadyExists
		}
		return fmt.Errorf("taskrun/queue: 入队 %s: %w", row.Run.ID, err)
	}
	return nil
}

func (q *SQLiteQueue) ClaimNext(ctx context.Context, workerID string, leaseUntil time.Time) (*Row, error) {
	if q == nil || q.db == nil {
		return nil, nil
	}
	now := q.now()
	// _txlock=immediate：BEGIN 即持写锁，SELECT→UPDATE 全程最新数据、无快照冲突。
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("taskrun/queue: 开始领取事务: %w", err)
	}
	defer tx.Rollback()

	// 1) 选一个待领取行（created_at 升序 = FIFO）。
	var id string
	err = tx.QueryRowContext(ctx, fmt.Sprintf(
		`SELECT "id" FROM %s WHERE "status"='queued' ORDER BY "created_at" ASC, "rowid" ASC LIMIT 1`,
		q.table)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil // 无可领取任务
	}
	if err != nil {
		return nil, fmt.Errorf("taskrun/queue: 选取待领取任务: %w", err)
	}

	// 2) 原子领取：WHERE status='queued' 防御并发节点重复（写锁下本不会发生，
	//    保留条件兜底：若状态已变则匹配 0 行，本轮放弃、下轮再试）。
	res, err := tx.ExecContext(ctx, fmt.Sprintf(
		`UPDATE %s SET "status"=?, "worker_id"=?, "lease_expires_at"=?, "updated_at"=?
		 WHERE "id"=? AND "status"='queued'`, q.table),
		string(taskrun.StatusRunning), workerID, leaseUntil, now, id)
	if err != nil {
		return nil, fmt.Errorf("taskrun/queue: 领取失败: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, nil // 已被并发节点领取
	}

	// 3) 按精确 id 读回（避免「按 worker 取最新 running」在多行下歧义）。
	row := tx.QueryRowContext(ctx, fmt.Sprintf(
		`SELECT %s FROM %s WHERE "id"=?`, rowColumns, q.table), id)
	claimed, err := scanRow(row)
	if err != nil {
		return nil, fmt.Errorf("taskrun/queue: 读取领取结果: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return claimed, nil
}

func (q *SQLiteQueue) UpdateStatusIf(ctx context.Context, runID string, expected, newStatus taskrun.Status, row *Row) (bool, error) {
	if q == nil || q.db == nil {
		return false, fmt.Errorf("taskrun/queue: 未初始化")
	}
	now := q.now()
	// 不携带 Row 时只更新状态列（用于 canceling→canceled 等轻量转换）。
	if row == nil {
		res, err := q.db.ExecContext(ctx, fmt.Sprintf(
			`UPDATE %s SET "status"=?, "updated_at"=? WHERE "id"=? AND "status"=?`,
			q.table), string(newStatus), now, runID, string(expected))
		if err != nil {
			return false, fmt.Errorf("taskrun/queue: 状态更新 %s: %w", runID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return false, err
		}
		return n > 0, nil
	}
	runJSON, err := json.Marshal(row.Run)
	if err != nil {
		return false, fmt.Errorf("taskrun/queue: 序列化 run: %w", err)
	}
	res, err := q.db.ExecContext(ctx, fmt.Sprintf(
		`UPDATE %s SET "run_json"=?, "status"=?, "worker_id"=?, "lease_expires_at"=?, "attempts"=?, "updated_at"=?
		 WHERE "id"=? AND "status"=?`, q.table),
		string(runJSON), string(newStatus), nullableString(row.WorkerID),
		nullableTime(row.LeaseExpiresAt), row.Attempts, now, runID, string(expected))
	if err != nil {
		return false, fmt.Errorf("taskrun/queue: 条件更新 %s: %w", runID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (q *SQLiteQueue) RequeueExpired(ctx context.Context, now time.Time, maxAttempts int) (int, error) {
	if q == nil || q.db == nil {
		return 0, nil
	}
	if maxAttempts < 0 {
		maxAttempts = 0
	}
	total := 0

	// 1) canceling + 无有效 lease → canceled（不重试：取消意图必须收敛）。
	n, err := q.execCount(ctx,
		`UPDATE %[1]s SET "status"='canceled', "worker_id"=NULL, "lease_expires_at"=NULL, "updated_at"=?
		 WHERE "status"='canceling' AND ("lease_expires_at" IS NULL OR "lease_expires_at" < ?)`,
		now, now)
	if err != nil {
		return total, err
	}
	total += n

	// 2) running + lease 过期 + 重拾未超限 → 重新 queued（attempts+1，可被其他节点领取）。
	n, err = q.execCount(ctx,
		`UPDATE %[1]s SET "status"='queued', "worker_id"=NULL, "lease_expires_at"=NULL, "attempts"="attempts"+1, "updated_at"=?
		 WHERE "status"='running' AND "lease_expires_at" IS NOT NULL AND "lease_expires_at" < ? AND "attempts" < ?`,
		now, now, maxAttempts)
	if err != nil {
		return total, err
	}
	total += n

	// 3) running + lease 过期 + 重拾超限 → failed（重试耗尽，不再执行）。
	n, err = q.execCount(ctx,
		`UPDATE %[1]s SET "status"='failed', "worker_id"=NULL, "lease_expires_at"=NULL, "updated_at"=?
		 WHERE "status"='running' AND "lease_expires_at" IS NOT NULL AND "lease_expires_at" < ? AND "attempts" >= ?`,
		now, now, maxAttempts)
	if err != nil {
		return total, err
	}
	total += n
	return total, nil
}

func (q *SQLiteQueue) execCount(ctx context.Context, sqlTpl string, args ...any) (int, error) {
	stmt := fmt.Sprintf(sqlTpl, q.table)
	res, err := q.db.ExecContext(ctx, stmt, args...)
	if err != nil {
		return 0, fmt.Errorf("taskrun/queue: 过期重拾失败: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

func (q *SQLiteQueue) Get(ctx context.Context, runID string) (*Row, error) {
	if q == nil || q.db == nil {
		return nil, ErrRunNotFound
	}
	row := q.db.QueryRowContext(ctx, fmt.Sprintf(
		`SELECT %s FROM %s WHERE "id"=?`, rowColumns, q.table), runID)
	out, err := scanRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRunNotFound
		}
		return nil, err
	}
	return out, nil
}

func (q *SQLiteQueue) List(ctx context.Context, filter taskrun.ListFilter) ([]taskrun.Run, error) {
	if q == nil || q.db == nil {
		return nil, nil
	}
	query := fmt.Sprintf(`SELECT %s FROM %s`, rowColumns, q.table)
	args := []any{}
	if filter.Status != "" {
		query += ` WHERE "status"=?`
		args = append(args, string(filter.Status))
	}
	query += ` ORDER BY "updated_at" DESC`
	rows, err := q.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("taskrun/queue: 列表查询: %w", err)
	}
	defer rows.Close()

	out := make([]taskrun.Run, 0, 8)
	for rows.Next() {
		r, err := scanRow(rows)
		if err != nil {
			return nil, err
		}
		run := r.Run
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// scanRow 从一行结果扫描出 Row（与 rowColumns 列序一致）。
func scanRow(sc interface {
	Scan(dest ...any) error
}) (*Row, error) {
	var (
		id, runJSON, status string
		workerID            sql.NullString
		leaseExpiresAt      sql.NullTime
		attempts            int
		createdAt, updatedAt time.Time
	)
	if err := sc.Scan(&id, &runJSON, &status, &workerID, &leaseExpiresAt, &attempts, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	var run taskrun.Run
	if err := json.Unmarshal([]byte(runJSON), &run); err != nil {
		return nil, fmt.Errorf("taskrun/queue: 反序列化 run %s: %w", id, err)
	}
	if run.ID == "" {
		run.ID = id
	}
	// status 列是状态机权威：轻量转换（UpdateStatusIf nil 分支 / ClaimNext /
	// RequeueExpired）只更新 status 列，run_json 内的 Status 可能滞后，故以列为准。
	if status != "" {
		run.Status = taskrun.Status(status)
	}
	out := &Row{
		Run:            run,
		WorkerID:       workerID.String,
		LeaseExpiresAt: leaseExpiresAt.Time,
		Attempts:       attempts,
	}
	return out, nil
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
