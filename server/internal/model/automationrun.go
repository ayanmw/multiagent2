package model

import (
	"time"
)

// AutomationRun 记录一次「自动化触发 → Loop 运行」的生命周期（M4-05 跨天恢复用）。
//
// 调度器（cron）与 Webhook 入口在启动一次自动化 Loop 前写入一条 status=running 的记录；
// Loop 正常结束（目标收敛或被 fail-open 放行）标记 done，失败标记 failed。
//
// 进程重启/中断后，status 仍为 running 的行即「未收敛 Goal Session」——
// 恢复扫描据此把它们续跑（读回 PLAN/PROGRESS/LEARNINGS，由 StateEnforcer 自动回灌），
// 与 M2-04 持久化 session 协同（历史消息与子任务 transcript 跨重启保留）。
//
// 设计要点：目标契约（M1-11）的收敛状态原本只活在内存 GoalEnforcer store 里，
// 进程一重启就丢失，无法判断「哪些 Goal Session 还没跑完」。本表把「运行是否结束」
// 这一事实持久化下来，作为跨重启恢复的唯一真相源，避免依赖易失的内存状态。
type AutomationRun struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	AutomationID uint      `gorm:"not null;index" json:"automation_id"`
	UserID       uint      `gorm:"not null;index" json:"user_id"`
	SessionKey   string    `gorm:"size:64;not null;index" json:"session_key"`
	Channel      string    `gorm:"size:16;not null" json:"channel"` // cron / webhook / recover
	Status       string    `gorm:"size:16;not null;index" json:"status"` // running / done / failed
	Error        string    `gorm:"type:text" json:"error,omitempty"`
	Attempts     int       `gorm:"not null;default:0" json:"attempts"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// TableName 固定运行记录表名，避免 GORM 复数化规则变化影响既有库。
func (AutomationRun) TableName() string { return "automation_runs" }

// 自动化运行的生命周期状态（M4-05）。
const (
	// RunStatusRunning 运行中（Loop 尚未结束）。进程重启后仍为 running 的行即待恢复项。
	RunStatusRunning = "running"
	// RunStatusDone Loop 正常收敛结束（目标 complete/blocked 或 fail-open 放行）。
	RunStatusDone = "done"
	// RunStatusFailed 运行失败且已放弃（超过恢复重试上限，或所属 automation 已不存在）。
	RunStatusFailed = "failed"
)

// 自动化运行的触发来源（与 api.ChannelKind 语义对齐，但此处用纯字符串以免跨包耦合）。
const (
	RunChannelCron    = "cron"    // 定时调度触发（M4-02）
	RunChannelWebhook = "webhook" // 外部事件触发（M4-03）
	RunChannelRecover = "recover" // 进程重启后的跨天恢复续跑（M4-05）
)
