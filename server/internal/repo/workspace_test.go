package repo

import (
	"path/filepath"
	"testing"

	"github.com/anmingwei/go-multi-agent-v2/internal/config"
	"github.com/anmingwei/go-multi-agent-v2/internal/model"
	"github.com/google/uuid"
)

func newWorkspaceTestDB(t *testing.T) *DB {
	t.Helper()
	cfg := &config.Config{DBPath: filepath.Join(t.TempDir(), "ws.db")}
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

func TestWorkspace_CRUD(t *testing.T) {
	db := newWorkspaceTestDB(t)
	uid := uint(1)
	key := "ws-" + uuid.NewString()[:8]

	w := &model.Workspace{
		UserID:    uid,
		Key:       key,
		Name:      "proj",
		LocalPath: filepath.Join(t.TempDir(), key),
		Status:    model.WorkspaceStatusActive,
	}
	if err := CreateWorkspace(db.DB, w); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := GetWorkspaceByKey(db.DB, uid, key)
	if err != nil {
		t.Fatalf("get by key: %v", err)
	}
	if got.Name != "proj" {
		t.Fatalf("name mismatch: %q", got.Name)
	}

	// 跨用户查询应返回未找到（防越权信息泄露）。
	if _, err := GetWorkspaceByKey(db.DB, 999, key); err == nil {
		t.Fatalf("cross-user lookup should fail")
	}

	// 更新。
	got.Description = "desc"
	if err := UpdateWorkspace(db.DB, got); err != nil {
		t.Fatalf("update: %v", err)
	}
	got2, _ := GetWorkspaceByKey(db.DB, uid, key)
	if got2.Description != "desc" {
		t.Fatalf("description not persisted: %q", got2.Description)
	}

	// 列表。
	list, err := ListWorkspaces(db.DB, uid)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: err=%v len=%d", err, len(list))
	}

	// 删除。
	if err := DeleteWorkspace(db.DB, got.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := GetWorkspaceByKey(db.DB, uid, key); err == nil {
		t.Fatalf("should be deleted")
	}
}

func TestGetWorkspaceByID_Ownership(t *testing.T) {
	db := newWorkspaceTestDB(t)
	w := &model.Workspace{
		UserID:    1,
		Key:       "ws-owner",
		Name:      "a",
		LocalPath: "/tmp/a",
		Status:    model.WorkspaceStatusActive,
	}
	if err := CreateWorkspace(db.DB, w); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := GetWorkspaceByID(db.DB, 1, w.ID); err != nil {
		t.Fatalf("owner should find: %v", err)
	}
	if _, err := GetWorkspaceByID(db.DB, 2, w.ID); err == nil {
		t.Fatalf("non-owner should not find")
	}
}
