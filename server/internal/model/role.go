package model

import "gorm.io/gorm"

// Predefined role names.
const (
	RoleAdmin     = "admin"
	RoleDeveloper = "developer"
	RoleViewer    = "viewer"
)

// Role represents a user role.
type Role struct {
	gorm.Model
	Name        string           `gorm:"uniqueIndex;size:32;not null" json:"name"`
	Description string           `gorm:"size:256" json:"description"`
	Permissions []RolePermission `gorm:"foreignKey:RoleID" json:"permissions,omitempty"`
}

// TableName overrides the default table name.
func (Role) TableName() string {
	return "roles"
}

// RolePermission defines a specific permission assigned to a role.
type RolePermission struct {
	gorm.Model
	RoleID   uint   `gorm:"not null;index" json:"role_id"`
	Resource string `gorm:"size:128;not null" json:"resource"`
	Action   string `gorm:"size:64;not null" json:"action"`
}

// TableName overrides the default table name.
func (RolePermission) TableName() string {
	return "role_permissions"
}

// SeedRoles returns the default roles with their permissions.
func SeedRoles() []Role {
	return []Role{
		{
			Name:        RoleAdmin,
			Description: "Administrator with full access",
			Permissions: []RolePermission{
				{Resource: "*", Action: "*"},
			},
		},
		{
			Name:        RoleDeveloper,
			Description: "Developer with workspace and agent access",
			Permissions: []RolePermission{
				{Resource: "providers", Action: "read"},
				{Resource: "models", Action: "read"},
				{Resource: "sessions", Action: "*"},
				{Resource: "chat", Action: "*"},
				{Resource: "tools", Action: "read"},
				{Resource: "skills", Action: "read"},
				{Resource: "mcp", Action: "read"},
			},
		},
		{
			Name:        RoleViewer,
			Description: "Read-only access",
			Permissions: []RolePermission{
				{Resource: "providers", Action: "read"},
				{Resource: "models", Action: "read"},
				{Resource: "chat", Action: "read"},
			},
		},
	}
}
