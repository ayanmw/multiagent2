package api

import (
	"context"
	"log"

	"github.com/ayanmw/multiagent2/server/internal/engine"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/notify"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"gorm.io/gorm"
)

// RecoveryRunner 是恢复续跑所需的「执行一次对话」能力；*Gateway 天然满足该接口
// （Run(ctx, Request)），故生产环境直接传入统一网关即可，便于单测注入 mock。
type RecoveryRunner interface {
	Run(ctx context.Context, req Request) (*Result, error)
}

// DefaultRecoveryMaxAttempts 是单次跨重启恢复对同一运行记录的最大尝试次数，
// 超过后标记为 failed 不再续跑，避免一个永远无法收敛的 Loop 在每次重启时无限续跑。
const DefaultRecoveryMaxAttempts = 3

// RecoverUnfinishedRuns 在进程启动后扫描「未收敛 Goal Session」（automation_runs.status=running）
// 并续跑：重发恢复提示 + 经 StateEnforcer 自动回灌 PLAN/PROGRESS/LEARNINGS，用目标契约
// TeamOverride 经统一 Gateway 重建上下文续跑。与 M2-04 持久化 session 协同（历史消息与
// 子任务 transcript 跨重启保留，使 Agent 接着已有进展而非从头开始）。
//
// 返回本次实际恢复（重新执行）的运行数量。logger 为 nil 时回退到 log.Default()。
//
// 边界处理：
//   - 所属 automation 已删除/查不到：标记该运行 failed 并跳过（无法重建目标提示）；
//   - runner 返回错误且重试次数未达上限：保留 running（写回错误与 attempts），待下次重启再续跑；
//   - runner 返回错误且已达上限：标记 failed，停止续跑；
//   - runner 成功：标记 done。
func RecoverUnfinishedRuns(ctx context.Context, db *gorm.DB, runner RecoveryRunner, goalTeam engine.TeamConfig, maxAttempts int, logger *log.Logger) int {
	if logger == nil {
		logger = log.Default()
	}
	if maxAttempts <= 0 {
		maxAttempts = DefaultRecoveryMaxAttempts
	}
	runs, err := repo.ListUnfinishedAutomationRuns(db)
	if err != nil {
		logger.Printf("[RECOVER] 扫描未收敛运行失败: %v", err)
		return 0
	}
	if len(runs) == 0 {
		return 0
	}
	logger.Printf("[RECOVER] 发现 %d 条未收敛运行，开始跨天恢复", len(runs))

	recovered := 0
	for i := range runs {
		run := runs[i]
		// 取回自动化目标提示词，用于重建 Loop 上下文（M4-02/M4-03 同款）。
		a, aerr := repo.GetAutomationByID(db, run.UserID, run.AutomationID)
		if aerr != nil {
			logger.Printf("[RECOVER] automation %d 查找失败(运行 %d 放弃): %v", run.AutomationID, run.ID, aerr)
			if merr := repo.MarkAutomationRunStatus(db, run.ID, model.RunStatusFailed, "automation not found: "+aerr.Error(), run.Attempts); merr != nil {
				logger.Printf("[RECOVER] 标记运行 %d failed 失败: %v", run.ID, merr)
			}
			continue
		}

		recovered++
		req := Request{
			Channel:      ChannelRecover,
			UserID:       run.UserID,
			SessionKey:   run.SessionKey,
			Message:      recoveryMessage(a.GoalPrompt),
			TeamOverride: &goalTeam,
		}
		_, rerr := runner.Run(ctx, req)
		if rerr != nil {
			run.Attempts++
			if run.Attempts >= maxAttempts {
				if merr := repo.MarkAutomationRunStatus(db, run.ID, model.RunStatusFailed, rerr.Error(), run.Attempts); merr != nil {
					logger.Printf("[RECOVER] 标记运行 %d failed 失败: %v", run.ID, merr)
				}
				logger.Printf("[RECOVER] 运行 %d 恢复失败且达上限(%d)，标记 failed: %v", run.ID, maxAttempts, rerr)
			} else {
				if merr := repo.MarkAutomationRunStatus(db, run.ID, model.RunStatusRunning, rerr.Error(), run.Attempts); merr != nil {
					logger.Printf("[RECOVER] 更新运行 %d 重试次数失败: %v", run.ID, merr)
				}
				logger.Printf("[RECOVER] 运行 %d 恢复失败（第 %d 次），保留 running 待下次重启续跑: %v", run.ID, run.Attempts, rerr)
			}
			continue
		}
		if merr := repo.MarkAutomationRunStatus(db, run.ID, model.RunStatusDone, "", run.Attempts); merr != nil {
			logger.Printf("[RECOVER] 标记运行 %d done 失败: %v", run.ID, merr)
		}
		logger.Printf("[RECOVER] 运行 %d（session=%s, automation=%d）已续跑完成", run.ID, run.SessionKey, run.AutomationID)
		// M4-07：跨天恢复成功续跑完成 → 站内信通知（best-effort）。
		if notifier != nil {
			_ = notifier.Notify(ctx, notify.NewSuccess(run.UserID, run.AutomationID, a.Name, run.SessionKey, "中断任务已跨重启恢复完成"))
		}
	}
	return recovered
}

// notifier 是可选的恢复结果通知出口（M4-07）。由 main.go 注入；测试传 nil。
var notifier notify.Notifier

// SetRecoveryNotifier 注入恢复续跑结果的通知出口（生产 main.go 调用）。
func SetRecoveryNotifier(n notify.Notifier) { notifier = n }

// recoveryMessage 包裹自动化的目标提示词，明确告知这是「中断后恢复」，
// 引导模型先读状态再续跑（StateEnforcer 会进一步把 PLAN/PROGRESS/LEARNINGS 回灌进上下文）。
func recoveryMessage(goal string) string {
	return "[系统恢复] 检测到上一次自主任务在进程重启/中断前尚未完成。请先调用 read_state 读取已落盘的工作状态" +
		"（PLAN/PROGRESS/LEARNINGS），基于已有进展继续推进以下目标，不要从头开始：\n\n" + goal
}
