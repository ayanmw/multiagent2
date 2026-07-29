package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/anmingwei/go-multi-agent-v2/internal/config"
	"github.com/anmingwei/go-multi-agent-v2/internal/model"
	"github.com/anmingwei/go-multi-agent-v2/internal/repo"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// newAPITestDB 构造一个临时 DB（仅本测试包复用）。
func newAPITestDB(t *testing.T) *repo.DB {
	t.Helper()
	cfg := &config.Config{DBPath: filepath.Join(t.TempDir(), "api.db")}
	db, err := repo.NewDB(cfg)
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

// TestBuildCodeActTools_WorkspaceDir 验证 Executor 在指定的 workspace 目录内执行：
// 给定 wsLocalDir 后，shell_exec 写入的文件应落在该目录下（M1-07 验收核心）。
func TestBuildCodeActTools_WorkspaceDir(t *testing.T) {
	dir := t.TempDir()
	tools, err := buildCodeActTools("", 0, dir)
	if err != nil {
		t.Fatalf("buildCodeActTools: %v", err)
	}

	var shell tool.CallableTool
	for _, tl := range tools {
		if tl.Declaration().Name == "shell_exec" {
			c, ok := tl.(tool.CallableTool)
			if !ok {
				t.Fatalf("shell_exec 不可调用")
			}
			shell = c
			break
		}
	}
	if shell == nil {
		t.Fatalf("未找到 shell_exec 工具")
	}

	out, err := shell.Call(context.Background(), []byte(`{"command":"echo hi > marker.txt"}`))
	if err != nil {
		t.Fatalf("call shell_exec: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "marker.txt")); statErr != nil {
		t.Fatalf("marker 未落在 workspace 目录 %s: %v (out=%v)", dir, statErr, out)
	}
}

// TestBuildCodeActTools_DefaultFallback 验证 wsLocalDir 为空时回退到默认用户目录。
func TestBuildCodeActTools_DefaultFallback(t *testing.T) {
	root := t.TempDir()
	tools, err := buildCodeActTools(root, 42, "")
	if err != nil {
		t.Fatalf("buildCodeActTools: %v", err)
	}
	var shell tool.CallableTool
	for _, tl := range tools {
		if tl.Declaration().Name == "shell_exec" {
			shell = tl.(tool.CallableTool)
			break
		}
	}
	if shell == nil {
		t.Fatalf("未找到 shell_exec 工具")
	}
	out, err := shell.Call(context.Background(), []byte(`{"command":"echo ok > probe.txt"}`))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	// 默认目录为 <root>/42。
	expect := filepath.Join(root, "42", "probe.txt")
	if _, statErr := os.Stat(expect); statErr != nil {
		t.Fatalf("默认目录回退错误，期望文件 %s 不存在: %v (out=%v)", expect, statErr, out)
	}
}

// TestResolveWorkspaceLocalDir 验证工作目录解析与会话绑定逻辑。
func TestResolveWorkspaceLocalDir(t *testing.T) {
	db := newAPITestDB(t)
	uid := uint(1)
	wsKey := "ws-resolve"
	wsPath := filepath.Join(t.TempDir(), "wsdir")
	ws := &model.Workspace{
		UserID:    uid,
		Key:       wsKey,
		Name:      "n",
		LocalPath: wsPath,
		Status:    model.WorkspaceStatusActive,
	}
	if err := repo.CreateWorkspace(db.DB, ws); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	sess := &model.Session{UserID: uid, SessionKey: "sess-x", Title: "t"}
	if err := db.DB.Create(sess).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}

	// 指定 workspaceKey：应返回其目录并绑定会话。
	dir, err := resolveWorkspaceLocalDir(db.DB, uid, wsKey, sess)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if dir != wsPath {
		t.Fatalf("dir=%q want %q", dir, wsPath)
	}
	if sess.WorkspaceID == nil || *sess.WorkspaceID != ws.ID {
		t.Fatalf("会话未绑定 workspace")
	}

	// 跨用户解析应报错（404）。
	if _, err := resolveWorkspaceLocalDir(db.DB, 999, wsKey, sess); err == nil {
		t.Fatalf("跨用户解析应报错")
	}

	// 不指定 key 且未绑定：回退默认目录（空串）。
	sess2 := &model.Session{UserID: uid, SessionKey: "sess-y", Title: "t"}
	if err := db.DB.Create(sess2).Error; err != nil {
		t.Fatalf("create session2: %v", err)
	}
	dir2, err := resolveWorkspaceLocalDir(db.DB, uid, "", sess2)
	if err != nil || dir2 != "" {
		t.Fatalf("未绑定应回退空串, got %q err=%v", dir2, err)
	}

	// 不指定 key 但已绑定：复用已绑定目录。
	dir3, err := resolveWorkspaceLocalDir(db.DB, uid, "", sess)
	if err != nil || dir3 != wsPath {
		t.Fatalf("应复用已绑定目录, got %q err=%v", dir3, err)
	}
}
