package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/ayanmw/multiagent2/server/internal/middleware"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// callMCPTemplates 以指定身份经 RBAC 中间件请求连接器市场接口。
// action: "read"（列表）/ "write"（导入）。body 非空时携带 JSON 请求体。
func callMCPTemplates(t *testing.T, db *gorm.DB, method, action string, uid uint, role, id, body string) (int, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set(middleware.CtxUserID, uid)
	c.Set(middleware.CtxUserRole, role)
	if id != "" {
		c.Params = gin.Params{{Key: "id", Value: id}}
	}
	req, _ := http.NewRequest(method, "/api/mcp/templates", strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	c.Request = req

	middleware.RequirePermission(db, "mcp", action)(c)
	if c.IsAborted() {
		return w.Code, w.Body.String()
	}
	switch {
	case method == http.MethodGet:
		ListMCPTemplatesHandler()(c)
	case method == http.MethodPost:
		ImportMCPTemplateHandler(db, mcpTestEncKey())(c)
	}
	return w.Code, w.Body.String()
}

// TestMCPTemplates_List 验收：模板列表按类目返回、含所需密钥提示、不泄漏模板 env/headers 值。
func TestMCPTemplates_List(t *testing.T) {
	db := newMCPAPITestDB(t)
	code, resp := callMCPTemplates(t, db, http.MethodGet, "read", 1, model.RoleDeveloper, "", "")
	if code != http.StatusOK {
		t.Fatalf("列表应 200, got %d (%s)", code, resp)
	}
	var out struct {
		Templates []struct {
			ID           string   `json:"id"`
			Name         string   `json:"name"`
			Category     string   `json:"category"`
			SecretFields []string `json:"secret_fields"`
			DefaultName  string   `json:"default_name"`
		} `json:"templates"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal([]byte(resp), &out); err != nil {
		t.Fatalf("解析列表响应: %v", err)
	}
	if out.Total < 6 || len(out.Templates) != out.Total {
		t.Fatalf("模板总数不符: total=%d len=%d", out.Total, len(out.Templates))
	}
	// 关键模板必须存在。
	want := map[string]string{"github": "GitHub", "gitlab": "GitLab", "slack": "Slack", "atlassian": "Jira / Confluence"}
	got := map[string]string{}
	for _, tmpl := range out.Templates {
		got[tmpl.ID] = tmpl.Name
	}
	for id, name := range want {
		if got[id] != name {
			t.Fatalf("模板 %s 应存在且名为 %s, got %q", id, name, got[id])
		}
	}
	// GitHub 模板必须提示 GITHUB_TOKEN 密钥。
	for _, tmpl := range out.Templates {
		if tmpl.ID == "github" {
			if len(tmpl.SecretFields) != 1 || tmpl.SecretFields[0] != "GITHUB_TOKEN" {
				t.Fatalf("github 模板 secret_fields 应含 GITHUB_TOKEN, got %v", tmpl.SecretFields)
			}
			if tmpl.DefaultName == "" {
				t.Fatal("github 模板 default_name 不应为空")
			}
		}
	}
	// 响应体不得出现占位符之外的任何真实密钥字样（模板值不回显 env/headers 键值）。
	if strings.Contains(resp, "Authorization") {
		t.Fatal("模板视图不应回显 headers 键（含 Authorization）")
	}
}

// TestMCPTemplates_ImportGithub 验收：一键导入 streamable 模板 → 201 + DB 落行 + 密文存储。
func TestMCPTemplates_ImportGithub(t *testing.T) {
	db := newMCPAPITestDB(t)
	uid := uint(21)
	body := `{"env":{"GITHUB_TOKEN":"ghp_import_secret"}}`
	code, resp := callMCPTemplates(t, db, http.MethodPost, "write", uid, model.RoleDeveloper, "github", body)
	if code != http.StatusCreated {
		t.Fatalf("导入应 201, got %d (%s)", code, resp)
	}
	assertNoSecret(t, "import", resp)

	var created struct {
		ID          uint     `json:"id"`
		Name        string   `json:"name"`
		Transport   string   `json:"transport"`
		HasHeaders  bool     `json:"has_headers"`
		HeaderKeys  []string `json:"header_keys"`
		Description string   `json:"description"`
	}
	if err := json.Unmarshal([]byte(resp), &created); err != nil {
		t.Fatalf("解析导入响应: %v", err)
	}
	if created.Name != "github" || created.Transport != "streamable" || !created.HasHeaders {
		t.Fatalf("导入字段不符: %+v", created)
	}
	if len(created.HeaderKeys) != 1 || created.HeaderKeys[0] != "Authorization" {
		t.Fatalf("header_keys 应含 Authorization, got %v", created.HeaderKeys)
	}
	// DB 行存在且 env/headers 为密文（AES-256-GCM，不回显明文）。
	m, err := repo.GetMCPServerByID(db, uid, created.ID, mcpTestEncKey())
	if err != nil {
		t.Fatalf("读回 DB 行失败: %v", err)
	}
	if m.Headers["Authorization"] != "Bearer ghp_import_secret" {
		t.Fatalf("解密后 Authorization 应替换占位符, got %q", m.Headers["Authorization"])
	}
	if m.HeadersEnc == "" {
		t.Fatal("headers 应以密文落库")
	}
	if m.EnvEnc != "" {
		t.Fatal("该模板不应有 env 密文")
	}
}

// TestMCPTemplates_ImportCustomName 验收：导入请求可覆盖名称/描述/启用。
func TestMCPTemplates_ImportCustomName(t *testing.T) {
	db := newMCPAPITestDB(t)
	uid := uint(22)
	body := `{"name":"我的 GitHub","description":"自建实例","enabled":false}`
	code, resp := callMCPTemplates(t, db, http.MethodPost, "write", uid, model.RoleDeveloper, "github", body)
	if code != http.StatusCreated {
		t.Fatalf("导入应 201, got %d (%s)", code, resp)
	}
	var created struct {
		Name        string `json:"name"`
		Enabled     bool   `json:"enabled"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal([]byte(resp), &created); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if created.Name != "我的 GitHub" || created.Enabled || created.Description != "自建实例" {
		t.Fatalf("覆盖字段不符: %+v", created)
	}
}

// TestMCPTemplates_ImportNameConflict 验收：同名冲突 409（复用现有配置名）。
func TestMCPTemplates_ImportNameConflict(t *testing.T) {
	db := newMCPAPITestDB(t)
	uid := uint(23)
	// 先手动建同名配置。
	body := `{"name":"github","transport":"streamable","url":"https://example.com/mcp"}`
	code, resp := callMCP(t, db, http.MethodPost, "write", uid, model.RoleDeveloper, "", body)
	if code != http.StatusCreated {
		t.Fatalf("预置同名配置应 201, got %d (%s)", code, resp)
	}
	// 再导入同名模板 → 409。
	code, resp = callMCPTemplates(t, db, http.MethodPost, "write", uid, model.RoleDeveloper, "github", `{}`)
	if code != http.StatusConflict {
		t.Fatalf("同名导入应 409, got %d (%s)", code, resp)
	}
}

// TestMCPTemplates_ImportUnknown 验收：未知模板 404；非法 body 400。
func TestMCPTemplates_ImportUnknown(t *testing.T) {
	db := newMCPAPITestDB(t)
	code, resp := callMCPTemplates(t, db, http.MethodPost, "write", 1, model.RoleDeveloper, "not-exist", `{}`)
	if code != http.StatusNotFound {
		t.Fatalf("未知模板应 404, got %d (%s)", code, resp)
	}
	code, resp = callMCPTemplates(t, db, http.MethodPost, "write", 1, model.RoleDeveloper, "github", `{bad json`)
	if code != http.StatusBadRequest {
		t.Fatalf("非法 body 应 400, got %d (%s)", code, resp)
	}
}

// TestMCPTemplates_RBAC 验收：viewer 列表 200、导入 403。
func TestMCPTemplates_RBAC(t *testing.T) {
	db := newMCPAPITestDB(t)
	code, resp := callMCPTemplates(t, db, http.MethodGet, "read", 1, model.RoleViewer, "", "")
	if code != http.StatusOK {
		t.Fatalf("viewer 列表应 200, got %d (%s)", code, resp)
	}
	code, resp = callMCPTemplates(t, db, http.MethodPost, "write", 1, model.RoleViewer, "github", `{}`)
	if code != http.StatusForbidden {
		t.Fatalf("viewer 导入应 403, got %d (%s)", code, resp)
	}
}

// TestMCPTemplates_ImportOwnerIsolation 验收：导入配置归属当前用户，他人不可见。
func TestMCPTemplates_ImportOwnerIsolation(t *testing.T) {
	db := newMCPAPITestDB(t)
	uidA, uidB := uint(31), uint(32)
	code, resp := callMCPTemplates(t, db, http.MethodPost, "write", uidA, model.RoleDeveloper, "fetch", `{}`)
	if code != http.StatusCreated {
		t.Fatalf("A 导入应 201, got %d (%s)", code, resp)
	}
	var created struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal([]byte(resp), &created); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	// B 查 A 的配置 → 404（owner 隔离）。
	idStr := strconv.FormatUint(uint64(created.ID), 10)
	code, resp = callMCP(t, db, http.MethodGet, "read", uidB, model.RoleDeveloper, idStr, "")
	if code != http.StatusNotFound {
		t.Fatalf("跨用户读取应 404, got %d (%s)", code, resp)
	}
	// B 可再导入同名模板（名称唯一性按 (user_id,name) 隔离）。
	code, _ = callMCPTemplates(t, db, http.MethodPost, "write", uidB, model.RoleDeveloper, "fetch", `{}`)
	if code != http.StatusCreated {
		t.Fatalf("B 导入同名模板应 201（按用户隔离）, got %d", code)
	}
}
