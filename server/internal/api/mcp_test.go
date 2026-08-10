package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/ayanmw/multiagent2/server/internal/middleware"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

const mcpTestSecret = "sk-mcp-secret-token"

func mcpTestEncKey() []byte {
	sum := sha256.Sum256([]byte("api-mcp-test-key"))
	return sum[:]
}

// newMCPAPITestDB 构造内存 SQLite 并迁移 mcp_servers + roles（RBAC 中间件需查角色权限）。
func newMCPAPITestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.MCPServer{}, &model.Role{}, &model.RolePermission{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, r := range model.SeedRoles() {
		if err := db.Create(&r).Error; err != nil {
			t.Fatalf("seed role %q: %v", r.Name, err)
		}
	}
	return db
}

// callMCP 以指定身份经 RBAC 中间件请求 MCP 管理接口，返回状态码与原始响应体。
func callMCP(t *testing.T, db *gorm.DB, method, action string, uid uint, role, id, body string) (int, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set(middleware.CtxUserID, uid)
	c.Set(middleware.CtxUserRole, role)
	if id != "" {
		c.Params = gin.Params{{Key: "id", Value: id}}
	}
	var req *http.Request
	if body != "" {
		req, _ = http.NewRequest(method, "/api/mcp", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req, _ = http.NewRequest(method, "/api/mcp", nil)
	}
	c.Request = req

	middleware.RequirePermission(db, "mcp", action)(c)
	if c.IsAborted() {
		return w.Code, w.Body.String()
	}
	key := mcpTestEncKey()
	switch {
	case method == http.MethodPost:
		CreateMCPServerHandler(db, key)(c)
	case method == http.MethodGet && id == "":
		ListMCPServersHandler(db, key)(c)
	case method == http.MethodGet:
		GetMCPServerHandler(db, key)(c)
	case method == http.MethodPut:
		UpdateMCPServerHandler(db, key)(c)
	}
	return w.Code, w.Body.String()
}

// TestMCPAPI_SecretsNeverEchoed 验收 M3-07：创建/列表/详情三条读路径都不回显
// env/headers 明文，只给掩码（has_env / env_keys）；库内存的是密文。
func TestMCPAPI_SecretsNeverEchoed(t *testing.T) {
	db := newMCPAPITestDB(t)
	uid := uint(11)

	body := `{"name":"remote","transport":"sse","url":"https://example.com/sse",
		"env":{"API_TOKEN":"` + mcpTestSecret + `"},
		"headers":{"Authorization":"Bearer ` + mcpTestSecret + `"}}`
	code, resp := callMCP(t, db, http.MethodPost, "write", uid, model.RoleDeveloper, "", body)
	if code != http.StatusCreated {
		t.Fatalf("create 应 201, got %d (%s)", code, resp)
	}
	assertNoSecret(t, "create", resp)

	var created struct {
		ID         uint     `json:"id"`
		HasEnv     bool     `json:"has_env"`
		EnvKeys    []string `json:"env_keys"`
		HasHeaders bool     `json:"has_headers"`
		HeaderKeys []string `json:"header_keys"`
	}
	if err := json.Unmarshal([]byte(resp), &created); err != nil {
		t.Fatalf("解析 create 响应: %v", err)
	}
	if !created.HasEnv || !created.HasHeaders {
		t.Fatalf("掩码字段缺失: %+v", created)
	}
	if len(created.EnvKeys) != 1 || created.EnvKeys[0] != "API_TOKEN" {
		t.Fatalf("env_keys 不符: %v", created.EnvKeys)
	}
	if len(created.HeaderKeys) != 1 || created.HeaderKeys[0] != "Authorization" {
		t.Fatalf("header_keys 不符: %v", created.HeaderKeys)
	}

	idStr := strconv.FormatUint(uint64(created.ID), 10)
	code, resp = callMCP(t, db, http.MethodGet, "read", uid, model.RoleDeveloper, "", "")
	if code != http.StatusOK {
		t.Fatalf("list 应 200, got %d (%s)", code, resp)
	}
	assertNoSecret(t, "list", resp)

	code, resp = callMCP(t, db, http.MethodGet, "read", uid, model.RoleDeveloper, idStr, "")
	if code != http.StatusOK {
		t.Fatalf("get 应 200, got %d (%s)", code, resp)
	}
	assertNoSecret(t, "get", resp)

	// 未传 env 的部分更新不得丢失原密文（"留空不修改" 语义）。
	code, resp = callMCP(t, db, http.MethodPut, "write", uid, model.RoleDeveloper, idStr,
		`{"description":"updated"}`)
	if code != http.StatusOK {
		t.Fatalf("update 应 200, got %d (%s)", code, resp)
	}
	assertNoSecret(t, "update", resp)
	if !strings.Contains(resp, `"has_env":true`) {
		t.Fatalf("部分更新后 env 丢失: %s", resp)
	}

	// 库里存的必须是密文。
	var envEnc string
	if err := db.Raw("SELECT env_enc FROM mcp_servers WHERE id = ?", created.ID).
		Scan(&envEnc).Error; err != nil {
		t.Fatalf("raw select: %v", err)
	}
	if envEnc == "" || strings.Contains(envEnc, mcpTestSecret) {
		t.Fatalf("库内非密文: %q", envEnc)
	}
}

// TestMCPAPI_CrossUserCannotRead 验收「越权读取拿不到明文」：
// 他人 id 直接 404，viewer 无写权限 403。
func TestMCPAPI_CrossUserCannotRead(t *testing.T) {
	db := newMCPAPITestDB(t)
	owner, intruder := uint(21), uint(22)

	body := `{"name":"fs","transport":"stdio","command":"npx","env":{"API_TOKEN":"` + mcpTestSecret + `"}}`
	code, resp := callMCP(t, db, http.MethodPost, "write", owner, model.RoleDeveloper, "", body)
	if code != http.StatusCreated {
		t.Fatalf("create 应 201, got %d (%s)", code, resp)
	}
	var created struct {
		ID uint `json:"id"`
	}
	_ = json.Unmarshal([]byte(resp), &created)
	idStr := strconv.FormatUint(uint64(created.ID), 10)

	// 越权详情 → 404，且响应体不含任何明文。
	code, resp = callMCP(t, db, http.MethodGet, "read", intruder, model.RoleDeveloper, idStr, "")
	if code != http.StatusNotFound {
		t.Fatalf("越权 get 应 404, got %d (%s)", code, resp)
	}
	assertNoSecret(t, "cross-user get", resp)

	// 越权列表 → 空集。
	code, resp = callMCP(t, db, http.MethodGet, "read", intruder, model.RoleDeveloper, "", "")
	if code != http.StatusOK || !strings.Contains(resp, `"total":0`) {
		t.Fatalf("越权 list 应为空, got %d (%s)", code, resp)
	}

	// viewer 无 mcp:write → 403。
	code, _ = callMCP(t, db, http.MethodPut, "write", owner, model.RoleViewer, idStr, `{"description":"x"}`)
	if code != http.StatusForbidden {
		t.Fatalf("viewer 写应 403, got %d", code)
	}
}

// assertNoSecret 断言响应体中不出现密钥明文与旧的 env/headers 字段。
func assertNoSecret(t *testing.T, stage, resp string) {
	t.Helper()
	if strings.Contains(resp, mcpTestSecret) {
		t.Fatalf("%s 响应泄漏明文: %s", stage, resp)
	}
	for _, banned := range []string{`"env":`, `"headers":`} {
		if strings.Contains(resp, banned) {
			t.Fatalf("%s 响应仍含明文字段 %s: %s", stage, banned, resp)
		}
	}
}
