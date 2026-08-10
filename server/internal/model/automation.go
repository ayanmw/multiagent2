package model

import (
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
)

// AutomationTriggerType 枚举一个 Automation 的触发方式（M4 自主化）。
//   - cron:    定时触发，需 CronExpr（如 "*/1 * * * *"）
//   - webhook: 外部事件触发，需 WebhookToken 匹配（M4-03 接入）
type AutomationTriggerType string

const (
	AutomationTriggerCron    AutomationTriggerType = "cron"
	AutomationTriggerWebhook AutomationTriggerType = "webhook"
)

// ValidAutomationTriggerTypes 列出所有合法 trigger_type 值（供校验与前端提示）。
var ValidAutomationTriggerTypes = []AutomationTriggerType{
	AutomationTriggerCron, AutomationTriggerWebhook,
}

// ParseAutomationTriggerType 校验并归一化 trigger 字符串（大小写/空白容错）。
func ParseAutomationTriggerType(s string) (AutomationTriggerType, bool) {
	t := AutomationTriggerType(strings.TrimSpace(strings.ToLower(s)))
	switch t {
	case AutomationTriggerCron, AutomationTriggerWebhook:
		return t, true
	}
	return "", false
}

// Automation 表示一个用户归属的自主化任务（M4-01 数据模型）。
//
// 它是「定时 / 事件 / 自触发 → 自动 Loop 推进到完成」的载体（M4 目标）：
// 调度器（M4-02）加载启用的 Automation，按 CronExpr 算下次运行时间；Webhook
// 入口（M4-03）按 WebhookToken 匹配外部事件；二者最终都带着 GoalPrompt 启动
// 一个 Goal Session 跑 Loop。LastRun / NextRun 由调度器在运行时维护（持久化）。
//
// 字段说明：
//   - UserID:       归属用户（owner-scoped CRUD，越权即 404）
//   - Name:         展示名（同一用户内唯一）
//   - TriggerType:  cron / webhook 二选一
//   - CronExpr:     cron 表达式（trigger_type=cron 时必填）
//   - WebhookToken: 外部事件匹配的令牌（trigger_type=webhook 时由创建接口生成，M4-03 消费）
//   - GoalPrompt:   驱动 Loop 的目标提示词（自动化要完成的事）
//   - Enabled:      是否启用（默认 true）
//   - LastRun:      最近一次运行时间（调度器维护，可为空）
//   - NextRun:      预计下次运行时间（调度器维护，可为空）
type Automation struct {
	gorm.Model
	UserID         uint                    `gorm:"not null;index" json:"user_id"`
	Name           string                  `gorm:"size:128;not null;uniqueIndex:idx_user_automation,priority:1" json:"name"`
	TriggerType    AutomationTriggerType  `gorm:"size:16;not null" json:"trigger_type"`
	CronExpr       string                  `gorm:"size:128" json:"cron_expr"`
	WebhookToken   string                  `gorm:"size:64" json:"-"` // 仅运行期匹配用，列表/详情不回显
	GoalPrompt     string                  `gorm:"type:text;not null" json:"goal_prompt"`
	Enabled        bool                    `gorm:"not null;default:true" json:"enabled"`
	LastRun        *time.Time              `json:"last_run"`
	NextRun        *time.Time              `json:"next_run"`
}

// TableName overrides the default GORM table name.
func (Automation) TableName() string { return "automations" }

// Validate 校验配置自洽性：名称必填；trigger_type 必为合法值；
// cron 触发器必须有 cron_expr；goal_prompt 必填（驱动 Loop 的目标）。
func (a *Automation) Validate() error {
	if strings.TrimSpace(a.Name) == "" {
		return errors.New("name is required")
	}
	if _, ok := ParseAutomationTriggerType(string(a.TriggerType)); !ok {
		return errors.New("invalid trigger_type (must be cron or webhook)")
	}
	if a.TriggerType == AutomationTriggerCron && strings.TrimSpace(a.CronExpr) == "" {
		return errors.New("cron_expr is required for cron trigger")
	}
	if strings.TrimSpace(a.GoalPrompt) == "" {
		return errors.New("goal_prompt is required")
	}
	return nil
}
