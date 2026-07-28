package model

import (
	"time"

	"gorm.io/gorm"
)

// APIKeyStatus represents the lifecycle status of an API key.
type APIKeyStatus string

const (
	APIKeyStatusActive  APIKeyStatus = "active"
	APIKeyStatusRevoked APIKeyStatus = "revoked"
)

// APIKey is a personal access token scoped to a user, presented via the
// X-API-Key header. Only the SHA-256 hash of the raw key is stored; the raw
// key is returned to the user exactly once at creation time.
type APIKey struct {
	gorm.Model
	UserID     uint         `gorm:"not null;index" json:"user_id"`
	Name       string       `gorm:"size:128;not null" json:"name"`
	KeyHash    string       `gorm:"size:64;not null;uniqueIndex" json:"-"`
	Prefix     string       `gorm:"size:16" json:"prefix"`
	LastUsedAt *time.Time   `json:"last_used_at,omitempty"`
	Status     APIKeyStatus `gorm:"size:16;not null;default:active" json:"status"`
	ExpiresAt  *time.Time   `json:"expires_at,omitempty"`
}

// TableName overrides the default table name.
func (APIKey) TableName() string {
	return "api_keys"
}
