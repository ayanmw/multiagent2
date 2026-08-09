package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/executor"
	"github.com/ayanmw/multiagent2/server/internal/middleware"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// newAuditAPITestDB 构造内存 SQLite（纯 Go，无需 CGO）并迁移 audit_logs 表。
func newAuditAPITestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.AuditLog{}); err != nil {
		t.Fatalf("migrate audit_logs: %v", err)
	}
	return db
}

// seedAuditLog 精确落一条审计记录（可控 created_at，用于时间范围断言）。
func seedAuditLog(t *testing.T, db *gorm.DB, uid uint, command, decision string, createdAt time.Time) {
	t.Helper()
	rec := &model.AuditLog{
		UserID:   uid,
		Command:  command,
		Workdir:  "/tmp/ws",
		Decision: decision,
		Allowed:  decision == executor.DecisionAllow.String(),
	}
	rec.CreatedAt = createdAt
	if err := repo.CreateAuditLog(db, rec); err != nil {
		t.Fatalf("seed audit log %s: %v", command, err)
	}
}

// auditResp 是 GET /api/audit 的响应契约（M3-02 增加 limit/offset/scope 元信息）。
type auditResp struct {
	AuditLogs []model.AuditLog `json:"audit_logs"`
	Total     int64            `json:"total"`
	Limit     int              `json:"limit"`
	Offset    int              `json:"offset"`
	Scope     string           `json:"scope"`
}

// callAudit 以指定身份（uid + role）请求审计列表接口，返回状态码与解析后的响应。
func callAudit(t *testing.T, db *gorm.DB, uid uint, role, query string) (int, auditResp) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set(middleware.CtxUserID, uid)
	c.Set(middleware.CtxUserRole, role)
	c.Request, _ = http.NewRequest(http.MethodGet, "/api/audit"+query, nil)

	ListAuditLogsHandler(db)(c)

	var out auditResp
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("解析响应失败: %v (body=%s)", err, w.Body.String())
		}
	}
	return w.Code, out
}

// TestListAuditLogsHandler_RoleScope 验收 M3-02 核心：developer 看全员、viewer 只看自己。
func TestListAuditLogsHandler_RoleScope(t *testing.T) {
	db := newAuditAPITestDB(t)
	now := time.Now()
	seedAuditLog(t, db, 42, "ls -la", executor.DecisionAllow.String(), now.Add(-2*time.Hour))
	seedAuditLog(t, db, 42, "rm -rf /", executor.DecisionDeny.String(), now.Add(-1*time.Hour))
	seedAuditLog(t, db, 99, "git status", executor.DecisionAllow.String(), now)

	// developer：看全员 3 条，scope=all。
	code, res := callAudit(t, db, 42, model.RoleDeveloper, "")
	if code != http.StatusOK {
		t.Fatalf("developer 期望 200，实际 %d", code)
	}
	if res.Total != 3 || res.Scope != auditScopeAll {
		t.Fatalf("developer 应看到全员 3 条且 scope=all，实际 total=%d scope=%s", res.Total, res.Scope)
	}

	// admin 同样看全员。
	if code, res = callAudit(t, db, 1, model.RoleAdmin, ""); code != http.StatusOK || res.Total != 3 {
		t.Fatalf("admin 应看到全员 3 条，实际 code=%d total=%d", code, res.Total)
	}

	// viewer：只看自己（uid=42 的 2 条），scope=self。
	code, res = callAudit(t, db, 42, model.RoleViewer, "")
	if code != http.StatusOK {
		t.Fatalf("viewer 期望 200，实际 %d", code)
	}
	if res.Total != 2 || res.Scope != auditScopeSelf {
		t.Fatalf("viewer 应仅看到自己 2 条且 scope=self，实际 total=%d scope=%s", res.Total, res.Scope)
	}
	for _, l := range res.AuditLogs {
		if l.UserID != 42 {
			t.Fatalf("viewer 不应看到他人记录: %+v", l)
		}
	}

	// viewer 传 user_id 想看他人 → 被忽略，仍只看自己（越权兜底）。
	code, res = callAudit(t, db, 42, model.RoleViewer, "?user_id=99")
	if code != http.StatusOK || res.Total != 2 {
		t.Fatalf("viewer 传 user_id 应被忽略仍见自己 2 条，实际 code=%d total=%d", code, res.Total)
	}
}

// TestListAuditLogsHandler_Filters 验收筛选：user_id / decision / command / 时间范围 / 分页。
func TestListAuditLogsHandler_Filters(t *testing.T) {
	db := newAuditAPITestDB(t)
	now := time.Now()
	seedAuditLog(t, db, 42, "ls -la", executor.DecisionAllow.String(), now.Add(-72*time.Hour))
	seedAuditLog(t, db, 42, "rm -rf /", executor.DecisionDeny.String(), now.Add(-1*time.Hour))
	seedAuditLog(t, db, 99, "git status", executor.DecisionAllow.String(), now.Add(-30*time.Minute))

	// developer 按 user_id 收敛到 99。
	code, res := callAudit(t, db, 1, model.RoleDeveloper, "?user_id=99")
	if code != http.StatusOK || res.Total != 1 || res.AuditLogs[0].UserID != 99 {
		t.Fatalf("user_id 过滤异常: code=%d total=%d", code, res.Total)
	}

	// decision 过滤。
	if code, res = callAudit(t, db, 1, model.RoleDeveloper, "?decision=deny"); code != http.StatusOK || res.Total != 1 {
		t.Fatalf("decision=deny 应命中 1 条，实际 code=%d total=%d", code, res.Total)
	}

	// command 模糊匹配。
	if code, res = callAudit(t, db, 1, model.RoleDeveloper, "?command=git"); code != http.StatusOK || res.Total != 1 {
		t.Fatalf("command=git 应命中 1 条，实际 code=%d total=%d", code, res.Total)
	}

	// 时间范围（毫秒时间戳）：最近 24 小时应排除 72 小时前那条。
	startMS := strconv.FormatInt(now.Add(-24*time.Hour).UnixMilli(), 10)
	if code, res = callAudit(t, db, 1, model.RoleDeveloper, "?start="+startMS); code != http.StatusOK || res.Total != 2 {
		t.Fatalf("start 时间过滤应命中 2 条，实际 code=%d total=%d", code, res.Total)
	}

	// 时间范围（日期时间字符串 end）：截止到 24 小时前，只剩 72 小时前那条。
	endStr := now.Add(-24 * time.Hour).Format("2006-01-02 15:04:05")
	if code, res = callAudit(t, db, 1, model.RoleDeveloper, "?end="+url.QueryEscape(endStr)); code != http.StatusOK || res.Total != 1 {
		t.Fatalf("end 时间过滤应命中 1 条，实际 code=%d total=%d", code, res.Total)
	}

	// 分页元信息回显。
	if code, res = callAudit(t, db, 1, model.RoleDeveloper, "?limit=2&offset=1"); code != http.StatusOK {
		t.Fatalf("分页请求失败: %d", code)
	}
	if res.Limit != 2 || res.Offset != 1 || len(res.AuditLogs) != 2 || res.Total != 3 {
		t.Fatalf("分页元信息异常: limit=%d offset=%d len=%d total=%d", res.Limit, res.Offset, len(res.AuditLogs), res.Total)
	}
}

// TestListAuditLogsHandler_BadRequest 验证非法筛选参数返回 400（而非静默忽略）。
func TestListAuditLogsHandler_BadRequest(t *testing.T) {
	db := newAuditAPITestDB(t)
	cases := []struct{ name, query string }{
		{"非法 decision", "?decision=whatever"},
		{"非法 user_id", "?user_id=abc"},
		{"非法 start", "?start=not-a-time"},
		{"end 早于 start", "?start=2026-01-02&end=2026-01-01"},
	}
	for _, tc := range cases {
		if code, _ := callAudit(t, db, 1, model.RoleDeveloper, tc.query); code != http.StatusBadRequest {
			t.Fatalf("%s 应返回 400，实际 %d", tc.name, code)
		}
	}
}

// TestListAuditLogsHandler_Unauthenticated 未注入身份时返回 401。
func TestListAuditLogsHandler_Unauthenticated(t *testing.T) {
	db := newAuditAPITestDB(t)
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest(http.MethodGet, "/api/audit", nil)
	ListAuditLogsHandler(db)(c)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("未认证应 401，实际 %d", w.Code)
	}
}

// TestParseAuditTime 覆盖时间参数解析的各种输入形态。
func TestParseAuditTime(t *testing.T) {
	if ts, ok := parseAuditTime(""); !ok || !ts.IsZero() {
		t.Fatal("空串应解析为零值且合法")
	}
	if _, ok := parseAuditTime("2026-01-02"); !ok {
		t.Fatal("日期格式应可解析")
	}
	if _, ok := parseAuditTime("2026-01-02 10:20:30"); !ok {
		t.Fatal("日期时间格式应可解析")
	}
	if _, ok := parseAuditTime("2026-01-02T10:20:30Z"); !ok {
		t.Fatal("RFC3339 应可解析")
	}
	if ts, ok := parseAuditTime("1767312000000"); !ok || ts.IsZero() {
		t.Fatal("毫秒时间戳应可解析")
	}
	if _, ok := parseAuditTime("garbage"); ok {
		t.Fatal("非法输入应返回 ok=false")
	}
}
