package model

import "gorm.io/gorm"

// UserStatus represents the account status of a user.
type UserStatus string

const (
	UserStatusActive   UserStatus = "active"
	UserStatusDisabled UserStatus = "disabled"
)

// User represents a platform user.
type User struct {
	gorm.Model
	Username     string     `gorm:"uniqueIndex;size:64;not null" json:"username"`
	Email        string     `gorm:"uniqueIndex;size:128;not null" json:"email"`
	PasswordHash string     `gorm:"size:256;not null" json:"-"`
	DisplayName  string     `gorm:"size:128" json:"display_name"`
	AvatarURL    string     `gorm:"size:512" json:"avatar_url"`
	RoleID       uint       `gorm:"not null;default:3" json:"role_id"`
	Role         Role       `gorm:"foreignKey:RoleID" json:"role,omitempty"`
	Status       UserStatus `gorm:"size:16;not null;default:active" json:"status"`
	// TenantID 是可空的租户归属（M8-09 多租户隔离）。nil=独立用户（不参与租户聚合），
	// 非空=该用户归属租户，与租户内其他用户共享租户级预算上限（tenant 作用域）。
	TenantID *uint `gorm:"index" json:"tenant_id,omitempty"`
}

// TableName overrides the default table name.
func (User) TableName() string {
	return "users"
}
