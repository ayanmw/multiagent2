package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ayanmw/multiagent2/server/internal/middleware"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// newBudgetAPITestDB 构造内存 SQLite 并迁移 budgets + roles 两张表（RBAC 中间件需查角色权限）。
func newBudgetAPITestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.BudgetPolicy{}, &model.Role{}, &model.RolePermission{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	//  seeding 默认角色（含 budgets 权限），使 RequirePermission 可命中。
	for _, r := range model.SeedRoles() {
		if err := db.Create(&r).Error; err != nil {
			t.Fatalf("seed role %q: %v", r.Name, err)
		}
	}
	return db
}

// callBudget 以指定身份（uid + role）经 RBAC 中间件（budgets:<action>）请求预算接口。
// 返回状态码与解析后的响应体（map）。DELETE 需传入 id 以填充路由参数。
func callBudget(t *testing.T, db *gorm.DB, method, action string, uid uint, role, id, body string) (int, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set(middleware.CtxUserID, uid)
	c.Set(middleware.CtxUserRole, role)
	if method == http.MethodDelete {
		c.Params = gin.Params{{Key: "id", Value: id}}
	}
	var req *http.Request
	if body != "" {
		req, _ = http.NewRequest(method, "/api/budgets", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req, _ = http.NewRequest(method, "/api/budgets", nil)
	}
	c.Request = req

	// 先过 RBAC 中间件；被拒（aborted）则直接返回状态码。
	middleware.RequirePermission(db, "budgets", action)(c)
	if c.IsAborted() {
		return w.Code, nil
	}
	switch method {
	case http.MethodGet:
		ListBudgetsHandler(db)(c)
	case http.MethodPut:
		UpsertBudgetHandler(db)(c)
	case http.MethodDelete:
		DeleteBudgetHandler(db)(c)
	}

	var out map[string]any
	if w.Code == http.StatusOK && len(w.Body.Bytes()) > 0 {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("解析响应失败: %v (body=%s)", err, w.Body.String())
		}
	}
	return w.Code, out
}

// TestBudgetAPI_RoleScopes 验收 RBAC：developer 可读写，viewer 只读（写 403）。
func TestBudgetAPI_RoleScopes(t *testing.T) {
	db := newBudgetAPITestDB(t)
	// developer 可读列表（空）。
	if code, _ := callBudget(t, db, http.MethodGet, "read", 1, model.RoleDeveloper, "", ""); code != http.StatusOK {
		t.Fatalf("developer GET 期望 200，实际 %d", code)
	}
	// developer 可写入一条全局用户预算。
	body := `{"scope":"user","scope_key":"","max_tokens":500,"window":"daily"}`
	code, out := callBudget(t, db, http.MethodPut, "write", 1, model.RoleDeveloper, "", body)
	if code != http.StatusOK {
		t.Fatalf("developer PUT 期望 200，实际 %d (body=%v)", code, out)
	}
	if p, ok := out["scope"]; !ok || p != "user" {
		t.Fatalf("PUT 响应应含 scope=user，实际 %v", out)
	}

	// viewer 读取应成功。
	if code, _ := callBudget(t, db, http.MethodGet, "read", 42, model.RoleViewer, "", ""); code != http.StatusOK {
		t.Fatalf("viewer GET 期望 200，实际 %d", code)
	}
	// viewer 写入应被拒（403）。
	if code, _ := callBudget(t, db, http.MethodPut, "write", 42, model.RoleViewer, "", body); code != http.StatusForbidden {
		t.Fatalf("viewer PUT 应 403，实际 %d", code)
	}
}

// TestBudgetAPI_UpsertAndDelete 验收 upsert 创建 + 列表可见 + 删除。
func TestBudgetAPI_UpsertAndDelete(t *testing.T) {
	db := newBudgetAPITestDB(t)
	body := `{"scope":"session","scope_key":"sk-x","max_tokens":10,"window":"daily"}`
	if code, _ := callBudget(t, db, http.MethodPut, "write", 1, model.RoleDeveloper, "", body); code != http.StatusOK {
		t.Fatalf("upsert 期望 200，实际 %d", code)
	}
	// 列表应含 1 条。
	code, out := callBudget(t, db, http.MethodGet, "read", 1, model.RoleDeveloper, "", "")
	if code != http.StatusOK {
		t.Fatalf("list 期望 200，实际 %d", code)
	}
	list, ok := out["budget_policies"].([]any)
	if !ok || len(list) != 1 {
		t.Fatalf("list 应含 1 条，实际 %v", out["budget_policies"])
	}
	// 取 id 删除。
	row := list[0].(map[string]any)
	idStr, _ := row["ID"].(float64)
	if code, _ := callBudget(t, db, http.MethodDelete, "write", 1, model.RoleDeveloper, itoa(int(idStr)), ""); code != http.StatusOK {
		t.Fatalf("delete 期望 200，实际 %d", code)
	}
	// 删除后列表为空。
	_, out = callBudget(t, db, http.MethodGet, "read", 1, model.RoleDeveloper, "", "")
	list = out["budget_policies"].([]any)
	if len(list) != 0 {
		t.Fatalf("删除后应为空，实际 %d 条", len(list))
	}
}

// TestBudgetAPI_Validation 非法请求体（缺 scope / 负 max_tokens / 非法 scope）应 400。
func TestBudgetAPI_Validation(t *testing.T) {
	db := newBudgetAPITestDB(t)
	cases := []string{
		`{"max_tokens":10}`, // 缺 scope
		`{"scope":"user","scope_key":"","max_tokens":0}`,   // 非法 max_tokens
		`{"scope":"bogus","scope_key":"","max_tokens":10}`, // 非法 scope
	}
	for i, body := range cases {
		if code, _ := callBudget(t, db, http.MethodPut, "write", 1, model.RoleDeveloper, "", body); code != http.StatusBadRequest {
			t.Fatalf("case %d 应 400，实际 %d", i, code)
		}
	}
}

// TestBudgetAPI_Unauthenticated 未注入身份时（无 role）RequirePermission 回 401。
func TestBudgetAPI_Unauthenticated(t *testing.T) {
	db := newBudgetAPITestDB(t)
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest(http.MethodGet, "/api/budgets", nil)
	// 不注入 CtxUserRole → 中间件应 401。
	middleware.RequirePermission(db, "budgets", "read")(c)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("未认证应 401，实际 %d", w.Code)
	}
}

// itoa 小工具（避免仅为此处引入 strconv）。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
