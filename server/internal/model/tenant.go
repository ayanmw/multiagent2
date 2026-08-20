package model

import (
	"errors"

	"gorm.io/gorm"
)

// TenantStatus enumerates the lifecycle states of a tenant.
type TenantStatus string

const (
	TenantStatusActive   TenantStatus = "active"
	TenantStatusDisabled TenantStatus = "disabled"
)

// Tenant 表示一个多租户隔离单元（M8-09）。
//
// 租户是「一组用户的配额边界」：租户内全部用户共享租户级预算上限
// （BudgetScope=tenant, ScopeKey=<tenant_id>，见 repo.EvaluateBudgets），
// 任一租户超限只拦截该租户内用户的后续 LLM 调用，不影响其他租户
// （验收「租户 A 超配额不影响 B」）。
//
// 用户经 User.TenantID（可空）归属租户；空租户用户视为「独立个体」，
// 不参与任何租户聚合（向后兼容既有部署）。
type Tenant struct {
	gorm.Model
	Name        string       `gorm:"uniqueIndex;size:128;not null" json:"name"`
	Description string       `gorm:"size:512" json:"description"`
	Status      TenantStatus `gorm:"size:16;not null;default:active" json:"status"`
	CreatedBy   uint         `gorm:"not null;default:0" json:"created_by"` // 创建者（平台管理员）用户 id
}

// TableName overrides the default GORM table name.
func (Tenant) TableName() string { return "tenants" }

// Validate 校验租户字段合法性（供 API 层使用）。
func (t Tenant) Validate() error {
	if t.Name == "" {
		return errors.New("name 不能为空")
	}
	switch t.Status {
	case TenantStatusActive, TenantStatusDisabled:
	default:
		return errors.New("status 仅支持 active / disabled")
	}
	return nil
}
