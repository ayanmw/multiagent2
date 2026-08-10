// Package scheduler 实现 M4-02「Cron 调度器」：常驻扫描启用的 cron 自动化，
// 按 cron 表达式计算并持久化 next_run；到点创建 Goal Session 启动 Loop；
// 运行失败重试并写审计。
//
// 设计要点：
//   - 调度器只负责「何时跑、跑前建会话、跑后更新时间戳、失败重试+审计」的编排，
//     具体「怎么跑 Loop」通过 AutomationRunner 接口注入（生产实现见 api.EngineLoopRunner，
//     测试可注入 mock），保持本包与 LLM 引擎解耦、可纯单测。
//   - 时间源 Now() 可注入，便于测试；next_run 比较与写入均基于同一时间源，避免时区漂移。
//   - 同一自动化运行期间加 running 锁，防止上一轮 Loop 未结束时被下一轮 tick 重入。
package scheduler

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/cron"
	"github.com/ayanmw/multiagent2/server/internal/executor"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"gorm.io/gorm"
)

// AutomationRunner 执行一次自动化运行（M4-02 调度目标）。
// 给定 Automation 与其归属用户，实现应：为该次运行创建/复用会话、把 goal_prompt
// 作为首条用户消息驱动 Loop 跑到目标收敛。返回 error 表示本次运行失败，
// 调度器据此触发重试与审计。
type AutomationRunner interface {
	Run(ctx context.Context, a *model.Automation, sessionKey string) error
}

// Scheduler 是常驻的 cron 调度器。
type Scheduler struct {
	DB     *gorm.DB
	Runner AutomationRunner

	TickInterval time.Duration // 扫描周期，默认 30s
	MaxRetries   int            // 单次运行失败后重试次数，默认 2
	RetryBackoff time.Duration  // 重试间隔（每次重试前等待），默认 0（测试可设）
	RetryDelay   time.Duration  // 运行失败后下次重试的延迟（写入 next_run），默认 1 分钟
	Now          func() time.Time // 时间源（测试注入），默认 time.Now

	running sync.Map // automation id -> true，防止同一自动化并发重入
	logger  *log.Logger
}

// New 构造调度器并套用默认值。
func New(db *gorm.DB, runner AutomationRunner) *Scheduler {
	return &Scheduler{
		DB:           db,
		Runner:       runner,
		TickInterval: 30 * time.Second,
		MaxRetries:   2,
		RetryBackoff: 0,
		RetryDelay:   time.Minute,
		Now:          time.Now,
		logger:       log.Default(),
	}
}

// ComputeNext 按 cron 表达式计算 from 之后的下一次运行时间（写入自动化 next_run 用）。
func (s *Scheduler) ComputeNext(a *model.Automation, from time.Time) (time.Time, error) {
	if a.TriggerType != model.AutomationTriggerCron {
		return time.Time{}, fmt.Errorf("automation %d: 非 cron 触发器，无法计算 next_run", a.ID)
	}
	spec, err := cron.Parse(a.CronExpr)
	if err != nil {
		return time.Time{}, fmt.Errorf("automation %d: 非法 cron 表达式 %q: %w", a.ID, a.CronExpr, err)
	}
	return spec.Next(from)
}

// Start 启动常驻调度循环，直到 ctx 取消。
func (s *Scheduler) Start(ctx context.Context) {
	ticker := time.NewTicker(s.TickInterval)
	defer ticker.Stop()
	// 启动后立即扫描一次，尽快拾取已到期项。
	go s.Tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			go s.Tick(ctx)
		}
	}
}

// Tick 异步扫描并触发到期自动化（每个到期项在独立 goroutine 中运行，避免长 Loop 阻塞扫描）。
func (s *Scheduler) Tick(ctx context.Context) {
	s.scan(ctx, false)
}

// TickSync 同步扫描并运行到期自动化（测试用），返回本次触发（含尝试运行）的数量。
func (s *Scheduler) TickSync(ctx context.Context) int {
	return s.scan(ctx, true)
}

// scan 加载启用 cron 自动化，对到期项执行运行编排。sync=true 时同步运行（测试），
// 否则派发到独立 goroutine（生产，避免长 Loop 阻塞）。
func (s *Scheduler) scan(ctx context.Context, sync bool) int {
	now := s.Now()
	list, err := repo.ListEnabledCronAutomations(s.DB)
	if err != nil {
		s.logger.Printf("[SCHED] 加载启用 cron 自动化失败: %v", err)
		return 0
	}
	triggered := 0
	for i := range list {
		a := list[i]
		// 新创建的自动化（next_run 为空）：先算一次 next_run 持久化，本次不触发。
		if a.NextRun == nil {
			if n, cerr := s.ComputeNext(&a, now); cerr == nil {
				a.NextRun = &n
				if uerr := repo.UpdateAutomation(s.DB, &a); uerr != nil {
					s.logger.Printf("[SCHED] automation %d 持久化 next_run 失败: %v", a.ID, uerr)
				}
			} else {
				s.logger.Printf("[SCHED] automation %d 计算 next_run 失败: %v", a.ID, cerr)
			}
			continue
		}
		if now.Before(*a.NextRun) {
			continue // 未到点
		}
		// 防止重入：同一自动化已在运行时跳过。
		if _, loaded := s.running.LoadOrStore(a.ID, true); loaded {
			continue
		}
		triggered++
		if sync {
			s.runAutomation(ctx, &a)
		} else {
			go func(a model.Automation) {
				s.runAutomation(ctx, &a)
			}(a)
		}
	}
	return triggered
}

// runAutomation 执行单个自动化的完整编排：建会话 → 重试运行 → 更新时间戳。
func (s *Scheduler) runAutomation(ctx context.Context, a *model.Automation) {
	defer s.running.Delete(a.ID)

	// 调度器预先创建会话，使「自动建 session」可观测（验证要求）。
	sessionKey := repo.NewSessionKey()
	if _, err := repo.GetOrCreateSession(s.DB, a.UserID, sessionKey); err != nil {
		s.logger.Printf("[SCHED] automation %d 创建会话失败: %v", a.ID, err)
		s.recordFailure(a, sessionKey, fmt.Errorf("创建会话失败: %w", err), 0)
		s.advanceNext(a, false)
		return
	}

	// 失败重试：最多 MaxRetries 次（共 MaxRetries+1 次尝试）。
	var runErr error
	for attempt := 0; attempt <= s.MaxRetries; attempt++ {
		if attempt > 0 && s.RetryBackoff > 0 {
			select {
			case <-time.After(s.RetryBackoff):
			case <-ctx.Done():
				return
			}
		}
		runErr = s.Runner.Run(ctx, a, sessionKey)
		if runErr == nil {
			break
		}
		s.recordFailure(a, sessionKey, runErr, attempt)
	}

	now := s.Now()
	if runErr == nil {
		a.LastRun = &now
	}
	s.advanceNext(a, runErr == nil)
}

// advanceNext 运行结束后更新 next_run：成功按 cron 推到下次；失败按 RetryDelay 快速重试。
func (s *Scheduler) advanceNext(a *model.Automation, success bool) {
	now := s.Now()
	if success {
		if n, err := s.ComputeNext(a, now); err == nil {
			a.NextRun = &n
		}
	} else {
		next := now.Add(s.RetryDelay)
		a.NextRun = &next
	}
	if err := repo.UpdateAutomation(s.DB, a); err != nil {
		s.logger.Printf("[SCHED] automation %d 更新 next_run 失败: %v", a.ID, err)
	}
}

// recordFailure 把一次运行失败写入审计日志（M3-01 审计体系复用，owner 隔离到该自动化归属用户）。
func (s *Scheduler) recordFailure(a *model.Automation, sessionKey string, err error, attempt int) {
	auditor := repo.NewDBAuditor(s.DB, a.UserID)
	auditor.Record(executor.AuditEntry{
		Timestamp: s.Now(),
		Command:   "automation:" + a.Name,
		Workdir:   "",
		Decision:  executor.DecisionDeny,
		Reason:    fmt.Sprintf("自动化运行失败(attempt %d): %v", attempt, err),
		Allowed:   false,
		Note:      "scheduler",
	})
}
