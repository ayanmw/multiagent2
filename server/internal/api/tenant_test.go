package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ayanmw/multiagent2/server/internal/middleware"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/gin-gonic/gin"
)

// callTenant 以指定身份（uid + role）经 RBAC 中间件（tenants:<action>）请求租户接口。
// 返回状态码与解析后的响应体（map）。
// route 约定（routeParams 按路径段名识别参数）：
//   - "/"                       → 列表 / 创建
//   - "/id/<id>"                → 详情 / 更新 / 删除
//   - "/id/<id>/members"        → 加入成员
//   - "/id/<id>/members/uid/<u>" → 移出成员
func callTenant(t *testing.T, db *repo.DB, method, action string, uid uint, role, route, body string) (int, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set(middleware.CtxUserID, uid)
	c.Set(middleware.CtxUserRole, role)
	// 解析路由参数（:id / :uid）。
	c.Params = routeParams(route)
	var req *http.Request
	if body != "" {
		req, _ = http.NewRequest(method, route, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req, _ = http.NewRequest(method, route, nil)
	}
	c.Request = req

	middleware.RequirePermission(db.DB, "tenants", action)(c)
	if c.IsAborted() {
		return w.Code, nil
	}
	switch {
	case method == http.MethodGet && c.Param("id") == "":
		ListTenantsHandler(db.DB)(c)
	case method == http.MethodPost && c.Param("id") == "":
		CreateTenantHandler(db.DB)(c)
	case method == http.MethodGet:
		GetTenantHandler(db.DB)(c)
	case method == http.MethodPut:
		UpdateTenantHandler(db.DB)(c)
	case method == http.MethodDelete && c.Param("uid") == "":
		DeleteTenantHandler(db.DB)(c)
	case method == http.MethodPost: // POST /:id/members
		AddTenantMemberHandler(db.DB)(c)
	case method == http.MethodDelete: // DELETE /:id/members/:uid
		RemoveTenantMemberHandler(db.DB)(c)
	}

	var out map[string]any
	if w.Code >= 200 && w.Code < 300 && len(w.Body.Bytes()) > 0 {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("解析响应失败: %v (body=%s)", err, w.Body.String())
		}
	}
	return w.Code, out
}

// routeParams 从 "/tenants/:id/members/:uid" 风格路由中提取 gin 参数。
func routeParams(route string) gin.Params {
	params := gin.Params{}
	// 简单解析：形如 /tenants/5/members/7 → id=5, uid=7；/tenants/5 → id=5。
	parts := splitPath(route)
	for i, p := range parts {
		switch p {
		case "id":
			if i+1 < len(parts) {
				params = append(params, gin.Param{Key: "id", Value: parts[i+1]})
			}
		case "uid":
			if i+1 < len(parts) {
				params = append(params, gin.Param{Key: "uid", Value: parts[i+1]})
			}
		}
	}
	return params
}

func splitPath(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '/' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
		} else {
			cur += string(r)
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// seedAPIUser 直接落一个用户（用于成员管理测试）。
func seedAPIUser(t *testing.T, db *repo.DB, uid uint, username string) {
	t.Helper()
	u := &model.User{Username: username, Email: username + "@t.test", PasswordHash: "x"}
	u.ID = uid
	if err := db.DB.Create(u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
}

// TestTenantAPI_RoleScopes 验收 RBAC：developer 只读（写 403），admin 读写全通。
func TestTenantAPI_RoleScopes(t *testing.T) {
	db := newAPITestDB(t)

	// developer 可读列表（tenants:read 种子已加）。
	if code, _ := callTenant(t, db, http.MethodGet, "read", 1, model.RoleDeveloper, "/", ""); code != http.StatusOK {
		t.Fatalf("developer 读列表应 200, got %d", code)
	}
	// developer 写 403。
	if code, _ := callTenant(t, db, http.MethodPost, "write", 1, model.RoleDeveloper, "/", `{"name":"t-a"}`); code != http.StatusForbidden {
		t.Fatalf("developer 写应 403, got %d", code)
	}
	// admin 写 201。
	code, out := callTenant(t, db, http.MethodPost, "write", 1, model.RoleAdmin, "/", `{"name":"t-a","description":"租户 A"}`)
	if code != http.StatusCreated {
		t.Fatalf("admin 创建应 201, got %d", code)
	}
	if out["name"] != "t-a" || out["member_count"] != float64(0) {
		t.Fatalf("创建返回异常: %v", out)
	}
}

// TestTenantAPI_CRUD 验收租户 CRUD 全流程（创建/同名冲突/详情/更新/删除非空拒绝）。
func TestTenantAPI_CRUD(t *testing.T) {
	db := newAPITestDB(t)

	code, out := callTenant(t, db, http.MethodPost, "write", 1, model.RoleAdmin, "/", `{"name":"t-a"}`)
	if code != http.StatusCreated {
		t.Fatalf("创建应 201, got %d", code)
	}
	id := fmt.Sprintf("%v", out["id"])

	// 同名冲突 409。
	if code, _ := callTenant(t, db, http.MethodPost, "write", 1, model.RoleAdmin, "/", `{"name":"t-a"}`); code != http.StatusConflict {
		t.Fatalf("同名应 409, got %d", code)
	}
	// 详情。
	if code, d := callTenant(t, db, http.MethodGet, "read", 1, model.RoleAdmin, "/id/"+id, ""); code != http.StatusOK || d["name"] != "t-a" {
		t.Fatalf("详情应 200 且 name=t-a, got %d %v", code, d)
	}

	// 成员管理：建用户 → 加入 → 有成员删除 409 → 移出 → 删除 200。
	seedAPIUser(t, db, 50, "tenantmember")
	code, _ = callTenant(t, db, http.MethodPost, "write", 1, model.RoleAdmin, "/id/"+id+"/members", `{"user_id":50}`)
	if code != http.StatusOK {
		t.Fatalf("加入成员应 200, got %d", code)
	}
	// 加入不存在的用户 404。
	if code, _ := callTenant(t, db, http.MethodPost, "write", 1, model.RoleAdmin, "/id/"+id+"/members", `{"user_id":9999}`); code != http.StatusNotFound {
		t.Fatalf("加入不存在用户应 404, got %d", code)
	}
	// 有成员删除 409。
	if code, _ := callTenant(t, db, http.MethodDelete, "write", 1, model.RoleAdmin, "/id/"+id, ""); code != http.StatusConflict {
		t.Fatalf("有成员删除应 409, got %d", code)
	}
	// 移出成员 → 删除 200。
	if code, _ := callTenant(t, db, http.MethodDelete, "write", 1, model.RoleAdmin, "/id/"+id+"/members/uid/50", ""); code != http.StatusOK {
		t.Fatalf("移出成员应 200, got %d", code)
	}

	// 更新（改名 + 停用）。
	code, out = callTenant(t, db, http.MethodPut, "write", 1, model.RoleAdmin, "/id/"+id, `{"name":"t-a2","status":"disabled"}`)
	if code != http.StatusOK || out["name"] != "t-a2" || out["status"] != "disabled" {
		t.Fatalf("更新应 200, got %d %v", code, out)
	}
	// 停用租户不能加入新成员。
	seedAPIUser(t, db, 51, "tenantmember2")
	if code, _ := callTenant(t, db, http.MethodPost, "write", 1, model.RoleAdmin, "/id/"+id+"/members", `{"user_id":51}`); code != http.StatusConflict {
		t.Fatalf("停用租户加成员应 409, got %d", code)
	}
	// 无成员删除 200。
	if code, _ := callTenant(t, db, http.MethodDelete, "write", 1, model.RoleAdmin, "/id/"+id, ""); code != http.StatusOK {
		t.Fatalf("无成员删除应 200, got %d", code)
	}
}

// TestTenantAPI_UserAssignmentViaAdmin 验收 admin 用户管理可带 tenant_id 归属（创建 + 更新）。
func TestTenantAPI_UserAssignmentViaAdmin(t *testing.T) {
	db := newAPITestDB(t)
	// 建租户。
	code, out := callTenant(t, db, http.MethodPost, "write", 1, model.RoleAdmin, "/", `{"name":"t-owner"}`)
	if code != http.StatusCreated {
		t.Fatalf("创建租户应 201, got %d", code)
	}
	tid := fmt.Sprintf("%v", out["id"])

	// admin 建用户并归属租户。
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set(middleware.CtxUserID, 1)
	c.Set(middleware.CtxUserRole, model.RoleAdmin)
	req, _ := http.NewRequest(http.MethodPost, "/api/admin/users",
		bytes.NewBufferString(fmt.Sprintf(`{"username":"tu1","email":"tu1@t.test","password":"secret1","tenant_id":%s}`, tid)))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	CreateUserHandler(db.DB)(c)
	if w.Code != http.StatusCreated {
		t.Fatalf("创建带租户用户应 201, got %d body=%s", w.Code, w.Body.String())
	}
	var outUser map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &outUser); err != nil {
		t.Fatalf("解析用户响应失败: %v", err)
	}
	u := outUser["user"].(map[string]any)
	if u["tenant_id"] == nil || fmt.Sprintf("%v", u["tenant_id"]) != tid {
		t.Fatalf("用户应归属租户 %s, got %v", tid, u["tenant_id"])
	}

	// 通过租户成员数确认归属。
	code, d := callTenant(t, db, http.MethodGet, "read", 1, model.RoleAdmin, "/id/"+tid, "")
	if code != http.StatusOK || d["member_count"] != float64(1) {
		t.Fatalf("租户成员数应=1, got %d %v", code, d)
	}
	_ = repo.ErrTenantNotFound // 保持 import（复用哨兵）
}
