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
	"sync/atomic"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/cron"
	"github.com/ayanmw/multiagent2/server/internal/executor"
	"github.com/ayanmw/multiagent2/server/internal/metrics"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/notify"
	"github.com/ayanmw/multiagent2/server/internal/obslog"
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
	RetryBackoff time.Duration  // 重试指数退避的基准间隔（attempt k 等待 base*2^(k-1)），默认 1s；<=0 表示不退避
	RetryBackoffMax time.Duration // 退避上限，防止高重试次数下等待爆炸，默认 30s
	RetryDelay   time.Duration  // 运行失败后下次重试的延迟（写入 next_run），默认 1 分钟
	Now          func() time.Time // 时间源（测试注入），默认 time.Now
	Notifier     notify.Notifier  // 运行结果通知出口（M4-07，可空：nil 时不发通知）
	// OnBackoff 是每次重试前等待退避时的可选回调（测试注入，用于无等待断言退避序列）；
	// 生产为 nil，仅做实际 sleep。
	OnBackoff func(attempt int, d time.Duration)

	running sync.Map // automation id -> true，防止同一自动化并发重入
	activeCount atomic.Int64 // 当前正在运行的自主 Loop 数（M7-05「Active Loops」gauge 数据源）
	logger  *log.Logger
}

// New 构造调度器并套用默认值。
func New(db *gorm.DB, runner AutomationRunner) *Scheduler {
	return &Scheduler{
		DB:           db,
		Runner:       runner,
		TickInterval: 30 * time.Second,
		MaxRetries:   2,
		RetryBackoff: time.Second,
		RetryBackoffMax: 30 * time.Second,
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

	// M7-06：自主 Loop 根 span（cron 触发）——整条 Loop（建会话→重试→通知）共用
	// 同一 trace_id；runErr 统一在函数内赋值，defer 结束时按最终结果记 status/attempts。
	ctx, endLoop := obslog.StartSpan(ctx, "automation.run",
		"automation_id", a.ID, "automation_name", a.Name, "user_id", a.UserID, "trigger", "cron")
	var runErr error
	var run *model.AutomationRun
	defer func() {
		attempts := 0
		if run != nil {
			attempts = run.Attempts
		}
		endLoop(runErr, "attempts", attempts)
	}()

	// M7-05：进入运行态——并发计数 +1，并即时刷新 metrics 的 Active Loops gauge
	//（供 Grafana 看板「Active Loops」展示此刻并发的自主化负载）。
	cur := s.activeCount.Add(1)
	metrics.SetActiveLoops(cur)
	defer func() {
		left := s.activeCount.Add(-1)
		metrics.SetActiveLoops(left)
	}()

	// M7-04：记录一次自主 Loop 运行（供「Loop 失败率」告警，与下方失败记录配对）。
	metrics.RecordLoopRun(ctx)

	// 调度器预先创建会话，使「自动建 session」可观测（验证要求）。
	sessionKey := repo.NewSessionKey()
	if _, err := repo.GetOrCreateSession(s.DB, a.UserID, sessionKey); err != nil {
		runErr = fmt.Errorf("创建会话失败: %w", err)
		s.logger.Printf("[SCHED] automation %d 创建会话失败: %v", a.ID, err)
		s.recordFailure(a, sessionKey, runErr, 0)
		// M7-04：会话建不起来等同本次 Loop 失败，记录失败指标。
		metrics.RecordLoopFailure(ctx)
		s.advanceNext(a, false)
		return
	}

	// M4-05：记录本次自动化运行（status=running），供进程重启后「跨天恢复」扫描续跑。
	// 建表失败（如测试库未迁移）时仅告警，不阻断本次运行（恢复能力降级）。
	run = &model.AutomationRun{
		AutomationID: a.ID,
		UserID:       a.UserID,
		SessionKey:   sessionKey,
		Channel:      model.RunChannelCron,
		Status:       model.RunStatusRunning,
	}
	if cerr := repo.CreateAutomationRun(s.DB, run); cerr != nil {
		s.logger.Printf("[SCHED] automation %d 创建运行记录失败(恢复不可追踪): %v", a.ID, cerr)
	}

	// 失败重试：最多 MaxRetries 次（共 MaxRetries+1 次尝试），每次重试前指数退避，
	// 避免瞬时故障（如临时网络抖动/模型限流）把自动化直接判失败；退避封顶 RetryBackoffMax
	// 防止高重试次数下等待时间爆炸。
	for attempt := 0; attempt <= s.MaxRetries; attempt++ {
		if attempt > 0 {
			backoff := s.backoffFor(attempt)
			if backoff > 0 {
				if s.OnBackoff != nil {
					s.OnBackoff(attempt, backoff)
				}
				select {
				case <-time.After(backoff):
				case <-ctx.Done():
					return
				}
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
		// M4-05：Loop 收敛结束（或被 fail-open 放行）→ 标记运行 done。
		if run != nil && run.ID != 0 {
			if merr := repo.MarkAutomationRunStatus(s.DB, run.ID, model.RunStatusDone, "", run.Attempts); merr != nil {
				s.logger.Printf("[SCHED] automation %d 标记运行 done 失败: %v", a.ID, merr)
			}
		}
		// M4-07：成功完成 → 站内信通知（best-effort，不阻断主流程）。
		s.notify(context.Background(), notify.NewSuccess(a.UserID, a.ID, a.Name, sessionKey, "自动化已执行完成"))
	} else {
		// M4-05：运行失败 → 标记 failed（供诊断与避免误判为「待恢复」）。
		if run != nil && run.ID != 0 {
			if merr := repo.MarkAutomationRunStatus(s.DB, run.ID, model.RunStatusFailed, runErr.Error(), run.Attempts); merr != nil {
				s.logger.Printf("[SCHED] automation %d 标记运行 failed 失败: %v", a.ID, merr)
			}
		}
		// M4-07：失败 → 站内信通知（best-effort）。
		s.notify(context.Background(), notify.NewFailure(a.UserID, a.ID, a.Name, runErr.Error()))
		// M7-04：本次自主 Loop 运行失败，记录失败指标（与开头的 RecordLoopRun 配对）。
		metrics.RecordLoopFailure(ctx)
	}
	s.advanceNext(a, runErr == nil)
}

// notify 经统一通知出口发送站内信（M4-07）。notifier 为空时静默跳过；
// 任意通知副作用失败已由 Notifier 内部吞掉，这里不关心返回值。
func (s *Scheduler) notify(ctx context.Context, n *model.Notification) {
	if s.Notifier == nil || n == nil {
		return
	}
	_ = s.Notifier.Notify(ctx, n)
}

// backoffFor 计算第 attempt 次重试（attempt>=1）前的指数退避时长：base * 2^(attempt-1)，
// 封顶 RetryBackoffMax。base<=0 表示不退避（立即重试）。该设计在「瞬时故障」与「高重试次数
// 下等待爆炸」之间取得平衡（M6-05 韧性补强）。
func (s *Scheduler) backoffFor(attempt int) time.Duration {
	base := s.RetryBackoff
	if base <= 0 {
		return 0
	}
	max := s.RetryBackoffMax
	// attempt>=1，2^(attempt-1) 用位移实现（避免 math.Pow 浮点误差）。
	d := base * time.Duration(1<<uint(attempt-1))
	if max > 0 && d > max {
		return max
	}
	return d
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
