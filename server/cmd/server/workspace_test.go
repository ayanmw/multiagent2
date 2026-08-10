package main

import (
	"crypto/sha256"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ayanmw/multiagent2/server/internal/config"
	"github.com/ayanmw/multiagent2/server/internal/provider"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/gin-gonic/gin"
)

// newWorkspaceRouter 构造带鉴权 + workspace 路由的引擎，返回 engine、底层 DB 与工作区根目录。
func newWorkspaceRouter(t *testing.T) (*gin.Engine, *repo.DB, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	wsRoot := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "ws.db")
	enc := sha256.Sum256([]byte("ws-enc-key"))
	cfg := &config.Config{
		DBPath:        dbPath,
		Port:          "0",
		JWTSecret:     "ws-secret",
		EncryptionKey: enc[:],
		WorkspaceRoot: wsRoot,
	}
	db, err := repo.NewDB(cfg)
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, e := db.DB.DB(); e == nil {
			_ = sqlDB.Close()
		}
	})
	disc := provider.NewDiscoverer(cfg.EncryptionKey, 0)
	return buildRouter(db, cfg, disc, nil, false, nil, nil, nil, buildGateway(db, cfg, nil, false, nil, nil, nil)), db, wsRoot
}

// TestWorkspace_API_CRUD 验证 workspace 全生命周期（建/列/查/改/删）与目录落盘。
func TestWorkspace_API_CRUD(t *testing.T) {
	r, db, wsRoot := newWorkspaceRouter(t)

	dev := &e2eClient{t: t, r: r}
	code, reg := dev.do("POST", "/api/auth/register", map[string]any{
		"username": "wdev1", "email": "wd@example.com", "password": "secret123",
	})
	if code != 201 {
		t.Fatalf("注册失败: %d %v", code, reg)
	}
	dev.tok = reg["token"].(string)

	// 创建
	code, body := dev.do("POST", "/api/workspaces", map[string]any{
		"name": "proj", "git_remote": "https://example.com/x/y.git", "description": "d",
	})
	if code != 201 {
		t.Fatalf("创建 workspace 失败: %d %v", code, body)
	}
	key := body["key"].(string)
	if !strings.HasPrefix(key, "ws-") {
		t.Fatalf("workspace key 格式异常: %v", key)
	}
	lp := body["local_path"].(string)
	if !strings.HasPrefix(lp, wsRoot) {
		t.Fatalf("local_path %s 不在 WorkspaceRoot %s 下", lp, wsRoot)
	}
	if _, statErr := os.Stat(lp); statErr != nil {
		t.Fatalf("workspace 目录未创建: %v", statErr)
	}

	// 列表
	code, list := dev.do("GET", "/api/workspaces", nil)
	if code != 200 {
		t.Fatalf("列表失败: %d %v", code, list)
	}
	if int(list["total"].(float64)) != 1 {
		t.Fatalf("列表数量应为 1, got %v", list["total"])
	}

	// 详情
	code, got := dev.do("GET", "/api/workspaces/"+key, nil)
	if code != 200 {
		t.Fatalf("详情失败: %d %v", code, got)
	}
	if got["name"] != "proj" || got["git_remote"] != "https://example.com/x/y.git" {
		t.Fatalf("详情内容异常: %v", got)
	}

	// 更新
	code, upd := dev.do("PUT", "/api/workspaces/"+key, map[string]any{"description": "newd"})
	if code != 200 {
		t.Fatalf("更新失败: %d %v", code, upd)
	}
	if upd["description"] != "newd" {
		t.Fatalf("更新未生效: %v", upd)
	}

	// 跨用户不可访问
	viewerTok := createViewerToken(t, db, "ws-secret")
	v := &e2eClient{t: t, r: r, tok: viewerTok}
	if code, _ := v.do("GET", "/api/workspaces/"+key, nil); code != http.StatusNotFound {
		t.Fatalf("跨用户应 404, got %d", code)
	}

	// 删除
	code, _ = dev.do("DELETE", "/api/workspaces/"+key, nil)
	if code != http.StatusNoContent {
		t.Fatalf("删除失败: %d", code)
	}
	if code, _ := dev.do("GET", "/api/workspaces/"+key, nil); code != http.StatusNotFound {
		t.Fatalf("删除后详情应 404, got %d", code)
	}
}

// TestWorkspace_RBAC 验证写操作受 workspaces:write 保护：viewer 被拒、developer 放行。
func TestWorkspace_RBAC(t *testing.T) {
	r, db, _ := newWorkspaceRouter(t)

	dev := &e2eClient{t: t, r: r}
	code, reg := dev.do("POST", "/api/auth/register", map[string]any{
		"username": "devw", "email": "devw@example.com", "password": "secret123",
	})
	if code != 201 {
		t.Fatalf("注册失败: %d %v", code, reg)
	}
	dev.tok = reg["token"].(string)

	viewerTok := createViewerToken(t, db, "ws-secret")
	v := &e2eClient{t: t, r: r, tok: viewerTok}

	// viewer 创建应被 403。
	if code, _ := v.do("POST", "/api/workspaces", map[string]any{"name": "x"}); code != http.StatusForbidden {
		t.Fatalf("viewer 创建 workspace 应 403, got %d", code)
	}
	// developer 创建应 201。
	if code, _ := dev.do("POST", "/api/workspaces", map[string]any{"name": "x"}); code != 201 {
		t.Fatalf("developer 创建 workspace 应 201, got %d", code)
	}
}
