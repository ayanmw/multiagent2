package main

import (
	"crypto/sha256"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/anmingwei/go-multi-agent-v2/internal/auth"
	"github.com/anmingwei/go-multi-agent-v2/internal/config"
	"github.com/anmingwei/go-multi-agent-v2/internal/model"
	"github.com/anmingwei/go-multi-agent-v2/internal/provider"
	"github.com/anmingwei/go-multi-agent-v2/internal/repo"
	"github.com/gin-gonic/gin"
)

// newRBACRouter 构造带鉴权 + RBAC 的路由，返回 engine 与底层 DB（便于直接造测试用户）。
func newRBACRouter(t *testing.T) (*gin.Engine, *repo.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dbPath := filepath.Join(t.TempDir(), "rbac.db")
	enc := sha256.Sum256([]byte("rbac-enc-key"))
	cfg := &config.Config{
		DBPath:        dbPath,
		Port:          "0",
		JWTSecret:     "rbac-secret",
		EncryptionKey: enc[:],
		WorkspaceRoot: t.TempDir(), // 隔离 workspace 目录创建，避免污染 cwd
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
	disc := provider.NewDiscoverer(cfg.EncryptionKey, time.Minute)
	return buildRouter(db, cfg, disc), db
}

// createViewerToken 直接落库一个 viewer 角色用户，并签发其 JWT（避免依赖注册默认 developer）。
func createViewerToken(t *testing.T, db *repo.DB, jwtSecret string) string {
	t.Helper()
	viewerRole, err := repo.GetRoleByName(db.DB, model.RoleViewer)
	if err != nil {
		t.Fatalf("GetRoleByName(viewer): %v", err)
	}
	hash, err := auth.HashPassword("secret123")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	user := &model.User{
		Username:     "viewer1",
		Email:        "viewer1@example.com",
		PasswordHash: hash,
		DisplayName:  "Viewer",
		RoleID:       viewerRole.ID,
		Status:       model.UserStatusActive,
	}
	if err := repo.CreateUser(db.DB, user); err != nil {
		t.Fatalf("CreateUser(viewer): %v", err)
	}
	tok, err := auth.GenerateToken(jwtSecret, user.ID, model.RoleViewer)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	return tok
}

// TestRBAC_SensitiveRoutes 验证 M0.5-02：敏感写路由接入 RequirePermission。
// viewer 调所有写路由应 403；developer 不被误伤（至少非 403）。
func TestRBAC_SensitiveRoutes(t *testing.T) {
	r, db := newRBACRouter(t)

	// developer 通过正常注册得到（默认 developer 角色，含 write 权限）。
	dev := &e2eClient{t: t, r: r}
	code, reg := dev.do("POST", "/api/auth/register", map[string]any{
		"username": "dev1", "email": "dev1@example.com", "password": "secret123",
	})
	if code != 201 {
		t.Fatalf("developer 注册失败: %d %v", code, reg)
	}
	dev.tok = reg["token"].(string)

	viewerTok := createViewerToken(t, db, "rbac-secret")

	// 受 RBAC 保护的敏感写路由（资源:动作）。
	sensitive := []struct {
		method, path string
	}{
		{"POST", "/api/providers"},
		{"PUT", "/api/providers/999"},
		{"DELETE", "/api/providers/999"},
		{"POST", "/api/providers/999/models/sync"},
		{"PUT", "/api/providers/999/models/999"},
		{"POST", "/api/auth/apikeys"},
		{"GET", "/api/auth/apikeys"},
		{"DELETE", "/api/auth/apikeys/999"},
		{"DELETE", "/api/sessions/whatever"},
		{"POST", "/api/workspaces"},
	}

	// viewer 应全部被拒（403）。
	v := &e2eClient{t: t, r: r, tok: viewerTok}
	for _, tc := range sensitive {
		code, _ := v.do(tc.method, tc.path, map[string]any{"name": "x"})
		if code != http.StatusForbidden {
			t.Errorf("viewer %s %s: 期望 403, 实际 %d", tc.method, tc.path, code)
		}
	}

	// viewer 读路由应放行（200）。
	if code, _ := v.do("GET", "/api/providers", nil); code != http.StatusOK {
		t.Errorf("viewer GET /api/providers: 期望 200, 实际 %d", code)
	}

	// developer 不应被 RBAC 误伤（非 403；可能因资源不存在返回 404 或合法 201）。
	d := &e2eClient{t: t, r: r, tok: dev.tok}
	for _, tc := range sensitive {
		code, _ := d.do(tc.method, tc.path, map[string]any{"name": "x"})
		if code == http.StatusForbidden {
			t.Errorf("developer %s %s: 不应 403, 实际 %d", tc.method, tc.path, code)
		}
	}

	// 正向校验：developer 合法创建 Provider 应 201，证明 RBAC 不阻断正常写操作。
	code, prov := d.do("POST", "/api/providers", map[string]any{
		"name": "p1", "protocol": "openai", "base_url": "http://localhost:1/v1", "api_key": "k",
	})
	if code != 201 {
		t.Errorf("developer 创建 Provider 应 201, 实际 %d, body=%v", code, prov)
	}
}
