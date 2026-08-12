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
				{Resource: "providers", Action: "write"},
				{Resource: "models", Action: "read"},
				{Resource: "models", Action: "write"},
				{Resource: "sessions", Action: "*"},
				{Resource: "chat", Action: "*"},
				{Resource: "apikeys", Action: "write"},
				{Resource: "workspaces", Action: "write"},
				{Resource: "tools", Action: "read"},
				{Resource: "skills", Action: "read"},
				{Resource: "skills", Action: "write"},
				{Resource: "mcp", Action: "read"},
				{Resource: "mcp", Action: "write"},
				{Resource: "taskruns", Action: "read"},
				{Resource: "taskruns", Action: "write"},
				{Resource: "audit", Action: "read"},
				{Resource: "usage", Action: "read"},
				{Resource: "budgets", Action: "read"},
				{Resource: "budgets", Action: "write"},
				{Resource: "checkpoints", Action: "read"},
				{Resource: "checkpoints", Action: "write"},
				{Resource: "automations", Action: "read"},
				{Resource: "automations", Action: "write"},
				{Resource: "notifications", Action: "read"},
				{Resource: "notifications", Action: "write"},
				{Resource: "knowledge", Action: "read"},
				{Resource: "knowledge", Action: "write"},
				{Resource: "skill_candidates", Action: "read"},
				{Resource: "skill_candidates", Action: "write"},
			},
		},
		{
			Name:        RoleViewer,
			Description: "Read-only access",
			Permissions: []RolePermission{
				{Resource: "providers", Action: "read"},
				{Resource: "models", Action: "read"},
				{Resource: "workspaces", Action: "read"},
				{Resource: "chat", Action: "read"},
				{Resource: "mcp", Action: "read"},
				{Resource: "skills", Action: "read"},
				{Resource: "taskruns", Action: "read"},
				{Resource: "audit", Action: "read"},
				{Resource: "usage", Action: "read"},
				{Resource: "budgets", Action: "read"},
				{Resource: "checkpoints", Action: "read"},
				{Resource: "automations", Action: "read"},
				{Resource: "notifications", Action: "read"},
				{Resource: "knowledge", Action: "read"},
				{Resource: "skill_candidates", Action: "read"},
			},
		},
	}
}
