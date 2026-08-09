package model

import (
	"errors"

	"gorm.io/gorm"
)

// 预算护栏作用域（M3-04 平台级预算护栏）。
const (
	// BudgetScopeUser 按用户维度限制累计 token（最常用：平台级配额）。
	BudgetScopeUser = "user"
	// BudgetScopeSession 按会话维度限制累计 token（单轮对话/单次任务上限）。
	BudgetScopeSession = "session"
	// BudgetScopeAutomation 按自动化维度限制累计 token（M4 自主 Loop 接入后按 automation_id 统计）。
	BudgetScopeAutomation = "automation"
)

// 预算统计窗口（M3-04）。
const (
	// BudgetWindowDaily token 累计按自然日重置（默认）。
	BudgetWindowDaily = "daily"
	// BudgetWindowTotal 不重置，全周期累计。
	BudgetWindowTotal = "total"
)

// BudgetPolicy 是一条平台级预算护栏策略（M3-04）。
// 作用域由 (Scope, ScopeKey) 唯一确定：
//   - Scope=user,   ScopeKey=""          → 全局默认用户预算（对所有用户生效，除非有更具体的用户策略）
//   - Scope=user,   ScopeKey="<uid>"     → 该用户的特定预算（覆盖全局默认）
//   - Scope=session,ScopeKey="<key>"     → 该会话的预算上限
//   - Scope=automation, ScopeKey="<id>"  → 该自动化的预算上限（M4 接入后按 automation_id 统计）
//
// MaxTokens 为该窗口内累计 token 的硬上限；达到即视为「预算耗尽」，
// 暂停该 session/automation 的后续 LLM 调用（返回「预算耗尽，待恢复」并写审计）。
type BudgetPolicy struct {
	gorm.Model
	Scope     string `gorm:"uniqueIndex:budget_scope_key;size:32;not null" json:"scope"`
	ScopeKey  string `gorm:"uniqueIndex:budget_scope_key;size:128;not null;default:''" json:"scope_key"`
	MaxTokens int64  `gorm:"not null" json:"max_tokens"`
	Window    string `gorm:"size:16;not null;default:'daily'" json:"window"`
}

// TableName overrides the default GORM table name.
func (BudgetPolicy) TableName() string { return "budget_policies" }

// Validate 校验预算策略字段合法性（供 API 层使用）。
func (p BudgetPolicy) Validate() error {
	switch p.Scope {
	case BudgetScopeUser, BudgetScopeSession, BudgetScopeAutomation:
	default:
		return errors.New("scope 仅支持 user / session / automation")
	}
	switch p.Window {
	case BudgetWindowDaily, BudgetWindowTotal:
	default:
		return errors.New("window 仅支持 daily / total")
	}
	if p.MaxTokens <= 0 {
		return errors.New("max_tokens 必须为正整数")
	}
	return nil
}
