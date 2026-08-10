package api

import (
	"context"

	"github.com/ayanmw/multiagent2/server/internal/engine"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/scheduler"
)

// engineLoopRunner 是 scheduler.AutomationRunner 的生产实现（M4-04）：
// 经统一 Gateway 跑 Goal Loop，与 Web 对话共用同一引擎管道与会话串行锁。
type engineLoopRunner struct {
	gw       *Gateway
	goalTeam engine.TeamConfig
	channel  Channel
}

// NewAutomationLoopRunner 构造自主化 Loop 运行器（实现 scheduler.AutomationRunner）。
// gw 为全局统一网关（与 Web 对话共享会话串行锁，使「同一 Goal 从不同 Channel 进入不串会话」）；
// goalTeam 强制开启子代理 + 目标契约（Goal Session 语义）；channel 标记触发来源
// （cron/webhook），便于审计与追踪，未来可扩展 IM/邮件。
func NewAutomationLoopRunner(gw *Gateway, goalTeam engine.TeamConfig, channel Channel) scheduler.AutomationRunner {
	return &engineLoopRunner{gw: gw, goalTeam: goalTeam, channel: channel}
}

// Run 执行一次自动化：复用统一网关把 goal_prompt 作为首条消息跑 Loop
// （含会话串行锁、用量计量、助手消息落库），不再各自重复引擎构建逻辑。
func (r *engineLoopRunner) Run(ctx context.Context, a *model.Automation, sessionKey string) error {
	_, err := r.gw.Run(ctx, Request{
		Channel:      r.channel,
		UserID:       a.UserID,
		SessionKey:   sessionKey,
		Message:      a.GoalPrompt,
		TeamOverride: &r.goalTeam,
	})
	return err
}
