package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/middleware"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// newUsageAPITestDB 构造内存 SQLite（纯 Go，无需 CGO）并迁移 usage_records 表。
func newUsageAPITestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.UsageRecord{}); err != nil {
		t.Fatalf("migrate usage_records: %v", err)
	}
	return db
}

// seedUsageRecord 精确落一条用量记录（可控 created_at，用于时间范围断言）。
func seedUsageRecord(t *testing.T, db *gorm.DB, uid, providerID, modelID uint, sessionKey string, pt, ct, tt int, createdAt time.Time) {
	t.Helper()
	rec := &model.UsageRecord{
		UserID:     uid,
		SessionID:  uint(1),
		SessionKey: sessionKey,
		ProviderID: providerID,
		ModelID:    modelID,
		ModelName:  "test-model",
		PromptTokens:     pt,
		CompletionTokens: ct,
		TotalTokens:      tt,
	}
	rec.CreatedAt = createdAt
	if err := repo.CreateUsageRecord(db, rec); err != nil {
		t.Fatalf("seed usage record: %v", err)
	}
}

// usageResp 是 GET /api/usage 的响应契约（M3-03）。
type usageResp struct {
	UsageRecords []model.UsageRecord `json:"usage_records"`
	Total        int64               `json:"total"`
	Totals       struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
		TotalTokens      int64 `json:"total_tokens"`
		Records          int64 `json:"records"`
	} `json:"totals"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
	Scope  string `json:"scope"`
}

// callUsage 以指定身份（uid + role）请求用量列表接口，返回状态码与解析后的响应。
func callUsage(t *testing.T, db *gorm.DB, uid uint, role, query string) (int, usageResp) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set(middleware.CtxUserID, uid)
	c.Set(middleware.CtxUserRole, role)
	c.Request, _ = http.NewRequest(http.MethodGet, "/api/usage"+query, nil)

	ListUsageHandler(db)(c)

	var out usageResp
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("解析响应失败: %v (body=%s)", err, w.Body.String())
		}
	}
	return w.Code, out
}

// TestListUsageHandler_RoleScope 验收 M3-03 核心：developer 看全员、viewer 只看自己。
func TestListUsageHandler_RoleScope(t *testing.T) {
	db := newUsageAPITestDB(t)
	now := time.Now()
	seedUsageRecord(t, db, 42, 1, 1, "sk-a", 10, 5, 15, now.Add(-2*time.Hour))
	seedUsageRecord(t, db, 42, 1, 1, "sk-b", 20, 8, 28, now.Add(-1*time.Hour))
	seedUsageRecord(t, db, 99, 1, 1, "sk-c", 4, 2, 6, now)

	// developer：看全员 3 条，scope=all，totals 累计 15+28+6=49。
	code, res := callUsage(t, db, 42, model.RoleDeveloper, "")
	if code != http.StatusOK {
		t.Fatalf("developer 期望 200，实际 %d", code)
	}
	if res.Total != 3 || res.Scope != usageScopeAll {
		t.Fatalf("developer 应看到全员 3 条且 scope=all，实际 total=%d scope=%s", res.Total, res.Scope)
	}
	if res.Totals.TotalTokens != 49 {
		t.Fatalf("developer 累计 total 应为 49，实际 %d", res.Totals.TotalTokens)
	}

	// admin 同样看全员。
	if code, res = callUsage(t, db, 1, model.RoleAdmin, ""); code != http.StatusOK || res.Total != 3 {
		t.Fatalf("admin 应看到全员 3 条，实际 code=%d total=%d", code, res.Total)
	}

	// viewer：只看自己（uid=42 的 2 条），scope=self，累计 15+28=43。
	code, res = callUsage(t, db, 42, model.RoleViewer, "")
	if code != http.StatusOK {
		t.Fatalf("viewer 期望 200，实际 %d", code)
	}
	if res.Total != 2 || res.Scope != usageScopeSelf {
		t.Fatalf("viewer 应仅看到自己 2 条且 scope=self，实际 total=%d scope=%s", res.Total, res.Scope)
	}
	if res.Totals.TotalTokens != 43 {
		t.Fatalf("viewer 累计 total 应为 43，实际 %d", res.Totals.TotalTokens)
	}
	for _, r := range res.UsageRecords {
		if r.UserID != 42 {
			t.Fatalf("viewer 不应看到他人记录: %+v", r)
		}
	}

	// viewer 传 user_id 想看他人 → 被忽略，仍只看自己（越权兜底）。
	code, res = callUsage(t, db, 42, model.RoleViewer, "?user_id=99")
	if code != http.StatusOK || res.Total != 2 {
		t.Fatalf("viewer 传 user_id 应被忽略仍见自己 2 条，实际 code=%d total=%d", code, res.Total)
	}
}

// TestListUsageHandler_Filters 验收筛选：user_id / provider_id / model_id / session_key / 时间 / 分页。
func TestListUsageHandler_Filters(t *testing.T) {
	db := newUsageAPITestDB(t)
	now := time.Now()
	seedUsageRecord(t, db, 42, 1, 1, "sk-a", 10, 5, 15, now.Add(-72*time.Hour))
	seedUsageRecord(t, db, 42, 2, 1, "sk-b", 20, 8, 28, now.Add(-1*time.Hour))
	seedUsageRecord(t, db, 99, 1, 2, "sk-c", 4, 2, 6, now.Add(-30*time.Minute))

	// developer 按 user_id 收敛到 99。
	if code, res := callUsage(t, db, 1, model.RoleDeveloper, "?user_id=99"); code != http.StatusOK || res.Total != 1 || res.UsageRecords[0].UserID != 99 {
		t.Fatalf("user_id 过滤异常")
	}
	// 按 provider_id=2 收敛（仅 uid=42 那条 28）。
	if code, res := callUsage(t, db, 1, model.RoleDeveloper, "?provider_id=2"); code != http.StatusOK || res.Total != 1 || res.Totals.TotalTokens != 28 {
		t.Fatalf("provider_id 过滤异常")
	}
	// 按 model_id=2 收敛（仅 uid=99 那条 6）。
	if code, res := callUsage(t, db, 1, model.RoleDeveloper, "?model_id=2"); code != http.StatusOK || res.Total != 1 || res.Totals.TotalTokens != 6 {
		t.Fatalf("model_id 过滤异常")
	}
	// 按 session_key=sk-a 收敛（第一条 15）。
	if code, res := callUsage(t, db, 1, model.RoleDeveloper, "?session_key=sk-a"); code != http.StatusOK || res.Total != 1 || res.Totals.TotalTokens != 15 {
		t.Fatalf("session_key 过滤异常")
	}
	// 时间范围（毫秒时间戳）：最近 24 小时应排除 72 小时前那条（剩 2 条，28+6=34）。
	startMS := strconv.FormatInt(now.Add(-24*time.Hour).UnixMilli(), 10)
	if code, res := callUsage(t, db, 1, model.RoleDeveloper, "?start="+startMS); code != http.StatusOK || res.Total != 2 || res.Totals.TotalTokens != 34 {
		t.Fatalf("start 时间过滤异常: total=%d totals=%+v", res.Total, res.Totals)
	}
	// 分页元信息回显。
	if code, res := callUsage(t, db, 1, model.RoleDeveloper, "?limit=2&offset=1"); code != http.StatusOK {
		t.Fatalf("分页请求失败: %d", code)
	} else if res.Limit != 2 || res.Offset != 1 || len(res.UsageRecords) != 2 || res.Total != 3 {
		t.Fatalf("分页元信息异常: limit=%d offset=%d len=%d total=%d", res.Limit, res.Offset, len(res.UsageRecords), res.Total)
	}
}

// TestListUsageHandler_BadRequest 验证非法筛选参数返回 400。
func TestListUsageHandler_BadRequest(t *testing.T) {
	db := newUsageAPITestDB(t)
	cases := []struct{ name, query string }{
		{"非法 user_id", "?user_id=abc"},
		{"非法 provider_id", "?provider_id=xyz"},
		{"非法 model_id", "?model_id=-1"},
		{"非法 start", "?start=not-a-time"},
		{"end 早于 start", "?start=2026-01-02&end=2026-01-01"},
	}
	for _, tc := range cases {
		if code, _ := callUsage(t, db, 1, model.RoleDeveloper, tc.query); code != http.StatusBadRequest {
			t.Fatalf("%s 应返回 400，实际 %d", tc.name, code)
		}
	}
}

// TestListUsageHandler_Unauthenticated 未注入身份时返回 401。
func TestListUsageHandler_Unauthenticated(t *testing.T) {
	db := newUsageAPITestDB(t)
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest(http.MethodGet, "/api/usage", nil)
	ListUsageHandler(db)(c)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("未认证应 401，实际 %d", w.Code)
	}
}

// TestParseUsageTime 复用审计时间解析（同一函数），覆盖关键输入。
func TestParseUsageTime(t *testing.T) {
	if _, ok := parseAuditTime(""); !ok {
		t.Fatal("空串应合法")
	}
	if _, ok := parseAuditTime("2026-01-02"); !ok {
		t.Fatal("日期格式应可解析")
	}
	if ts, ok := parseAuditTime("1767312000000"); !ok || ts.IsZero() {
		t.Fatal("毫秒时间戳应可解析")
	}
	if _, ok := parseAuditTime("garbage"); ok {
		t.Fatal("非法输入应返回 ok=false")
	}
	_ = url.QueryEscape("2026-01-02 15:04:05")
}
