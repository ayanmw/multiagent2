package repo

import (
	"testing"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// TestGetRoleIDByName 验证按名称查角色 id（M0.5-05：注册默认角色改为按名称查询，
// 不再硬编码 RoleID=3），缺失角色时应返回错误。
func TestGetRoleIDByName(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Role{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.Create(&model.Role{Name: "developer", Description: "dev"}).Error; err != nil {
		t.Fatalf("create role: %v", err)
	}

	id, err := GetRoleIDByName(db, "developer")
	if err != nil {
		t.Fatalf("GetRoleIDByName(developer): %v", err)
	}
	if id == 0 {
		t.Fatal("期望非 0 的角色 id")
	}

	if _, err := GetRoleIDByName(db, "nonexistent"); err == nil {
		t.Fatal("缺失角色应返回错误")
	}
}
