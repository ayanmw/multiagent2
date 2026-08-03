package repo

import (
	"path/filepath"
	"testing"

	"github.com/ayanmw/multiagent2/server/internal/config"
	"github.com/ayanmw/multiagent2/server/internal/model"
)

func newMCPServerTestDB(t *testing.T) *DB {
	t.Helper()
	cfg := &config.Config{DBPath: filepath.Join(t.TempDir(), "mcp.db")}
	db, err := NewDB(cfg)
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, e := db.DB.DB(); e == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

// TestMCPServer_OwnerScopedCRUD 覆盖 M2-02 repo 层：创建/列表/查询/更新/删除，
// 以及 owner 隔离与同名查重。
func TestMCPServer_OwnerScopedCRUD(t *testing.T) {
	db := newMCPServerTestDB(t)
	uid := uint(1)

	// 创建 stdio 配置（含 args/env，验证 serializer:json 往返）。
	m := &model.MCPServer{
		UserID:    uid,
		Name:      "fs",
		Transport: model.MCPTransportStdio,
		Command:   "npx",
		Args:      []string{"-y", "@modelcontextprotocol/server-fs"},
		Env:       map[string]string{"FOO": "bar"},
	}
	if err := CreateMCPServer(db.DB, m); err != nil {
		t.Fatalf("create: %v", err)
	}
	if m.ID == 0 {
		t.Fatal("id not set after create")
	}

	// 列表
	list, err := ListMCPServers(db.DB, uid)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: err=%v len=%d", err, len(list))
	}

	// 按 id 查（owner-scoped）+ JSON 字段往返
	got, err := GetMCPServerByID(db.DB, uid, m.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Command != "npx" || len(got.Args) != 2 || got.Args[0] != "-y" {
		t.Fatalf("args 往返不一致: %+v", got)
	}
	if got.Env["FOO"] != "bar" {
		t.Fatalf("env 往返不一致: %+v", got.Env)
	}

	// 跨用户查 → not found
	if _, err := GetMCPServerByID(db.DB, 999, m.ID); err != ErrMCPServerNotFound {
		t.Fatalf("跨用户查应 ErrMCPServerNotFound, got %v", err)
	}

	// 更新
	got.Enabled = false
	got.Description = "updated"
	if err := UpdateMCPServer(db.DB, got); err != nil {
		t.Fatalf("update: %v", err)
	}
	got2, _ := GetMCPServerByID(db.DB, uid, m.ID)
	if got2.Enabled || got2.Description != "updated" {
		t.Fatalf("update 未落库: %+v", got2)
	}

	// 同名查重
	if _, err := GetMCPServerByName(db.DB, uid, "fs"); err != ErrMCPServerNotFound {
		t.Fatalf("GetMCPServerByName 已存在应返回该行, got %v", err)
	}
	if _, err := GetMCPServerByName(db.DB, uid, "nope"); err != ErrMCPServerNotFound {
		t.Fatalf("GetMCPServerByName 缺失应 ErrMCPServerNotFound, got %v", err)
	}

	// 删除
	if err := DeleteMCPServer(db.DB, m.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := GetMCPServerByID(db.DB, uid, m.ID); err != ErrMCPServerNotFound {
		t.Fatalf("删除后应 not found, got %v", err)
	}
}
