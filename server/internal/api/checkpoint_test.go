package api

import (
	"bytes"
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
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// newCheckpointAPITestDB 构造内存 SQLite（纯 Go，无需 CGO）并迁移 checkpoints / audit_logs 表。
func newCheckpointAPITestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.Checkpoint{}, &model.AuditLog{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// seedCheckpoint 落一条待审批检查点，返回其主键。
func seedCheckpoint(t *testing.T, db *gorm.DB, uid uint, command, workdir string) *model.Checkpoint {
	t.Helper()
	cp := &model.Checkpoint{
		SessionID: "sess-" + strconv.FormatUint(uint64(uid), 10),
		UserID:    uid,
		Command:   command,
		Workdir:   workdir,
		Reason:    "命中 ask 规则",
		Context:   "coder",
		Status:    model.CheckpointPending,
	}
	if err := repo.CreateCheckpoint(db, cp); err != nil {
		t.Fatalf("seed checkpoint: %v", err)
	}
	return cp
}

type checkpointListResp struct {
	Checkpoints []model.Checkpoint `json:"checkpoints"`
	Total       int64              `json:"total"`
	Limit       int                `json:"limit"`
	Offset      int                `json:"offset"`
	Scope       string             `json:"scope"`
}

func callListCheckpoints(t *testing.T, db *gorm.DB, uid uint, role, query string) (int, checkpointListResp) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set(middleware.CtxUserID, uid)
	c.Set(middleware.CtxUserRole, role)
	c.Request, _ = http.NewRequest(http.MethodGet, "/api/checkpoints"+query, nil)

	ListCheckpointsHandler(db)(c)

	var out checkpointListResp
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("解析响应失败: %v (body=%s)", err, w.Body.String())
		}
	}
	return w.Code, out
}

func callResolveCheckpoint(t *testing.T, db *gorm.DB, uid uint, role string, id uint, action, comment string) (int, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set(middleware.CtxUserID, uid)
	c.Set(middleware.CtxUserRole, role)
	body, _ := json.Marshal(map[string]string{"action": action, "comment": comment})
	idStr := strconv.FormatUint(uint64(id), 10)
	c.Request, _ = http.NewRequest(http.MethodPost, "/api/checkpoints/"+idStr+"/resolve", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "id", Value: idStr}}

	ResolveCheckpointHandler(db)(c)

	out := map[string]any{}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

// TestListCheckpointsHandler_RoleScope 验证 owner 隔离：developer/admin 看全员，viewer 只看自己。
func TestListCheckpointsHandler_RoleScope(t *testing.T) {
	db := newCheckpointAPITestDB(t)
	seedCheckpoint(t, db, 42, "rm -rf ./build", t.TempDir())
	seedCheckpoint(t, db, 42, "git push --force", t.TempDir())
	seedCheckpoint(t, db, 99, "chmod 777 /etc", t.TempDir())

	code, res := callListCheckpoints(t, db, 42, model.RoleDeveloper, "")
	if code != http.StatusOK || res.Total != 3 || res.Scope != "all" {
		t.Fatalf("developer 应看到全员 3 条 scope=all，实际 code=%d total=%d scope=%s", code, res.Total, res.Scope)
	}

	code, res = callListCheckpoints(t, db, 42, model.RoleViewer, "")
	if code != http.StatusOK || res.Total != 2 || res.Scope != "self" {
		t.Fatalf("viewer 应只看本人 2 条 scope=self，实际 code=%d total=%d scope=%s", code, res.Total, res.Scope)
	}
	for _, cp := range res.Checkpoints {
		if cp.UserID != 42 {
			t.Fatalf("viewer 不应看到他人记录: %+v", cp)
		}
	}
}

// TestListCheckpointsHandler_StatusFilter 验证状态过滤与非法状态拒绝。
func TestListCheckpointsHandler_StatusFilter(t *testing.T) {
	db := newCheckpointAPITestDB(t)
	seedCheckpoint(t, db, 7, "rm -rf ./a", t.TempDir())
	done := seedCheckpoint(t, db, 7, "rm -rf ./b", t.TempDir())
	if err := repo.ResolveCheckpoint(db, done.ID, model.CheckpointRejected, "太危险", 1, ""); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if code, res := callListCheckpoints(t, db, 7, model.RoleAdmin, "?status=pending"); code != http.StatusOK || res.Total != 1 {
		t.Fatalf("pending 应 1 条，实际 code=%d total=%d", code, res.Total)
	}
	if code, res := callListCheckpoints(t, db, 7, model.RoleAdmin, "?status=rejected"); code != http.StatusOK || res.Total != 1 {
		t.Fatalf("rejected 应 1 条，实际 code=%d total=%d", code, res.Total)
	}
	if code, _ := callListCheckpoints(t, db, 7, model.RoleAdmin, "?status=bogus"); code != http.StatusBadRequest {
		t.Fatalf("非法 status 应 400，实际 %d", code)
	}
}

// TestResolveCheckpoint_ApproveExecutes 验收 M3-05 主链路：approve 后命令被真正执行、
// 结果回填检查点，并写入一条「放行」审计。
func TestResolveCheckpoint_ApproveExecutes(t *testing.T) {
	db := newCheckpointAPITestDB(t)
	cp := seedCheckpoint(t, db, 42, "echo approved-ok", t.TempDir())

	code, out := callResolveCheckpoint(t, db, 1, model.RoleAdmin, cp.ID, "approve", "已人工复核")
	if code != http.StatusOK {
		t.Fatalf("approve 应 200，实际 %d (%v)", code, out)
	}
	if out["status"] != model.CheckpointApproved {
		t.Fatalf("状态应为 approved，实际 %v", out["status"])
	}
	if s, _ := out["result"].(string); !strings.Contains(s, "approved-ok") {
		t.Fatalf("approve 应真正执行命令并回传输出，实际 %q", s)
	}

	got, err := repo.GetCheckpoint(db, cp.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Status != model.CheckpointApproved || got.ResolvedBy != 1 || got.Comment != "已人工复核" {
		t.Fatalf("检查点未正确落终态: %+v", got)
	}
	if !strings.Contains(got.Result, "approved-ok") {
		t.Fatalf("执行结果未回填: %q", got.Result)
	}

	logs, _, lerr := repo.ListAuditLogs(db, repo.AuditLogFilter{UserID: 42})
	if lerr != nil {
		t.Fatalf("list audit: %v", lerr)
	}
	if len(logs) != 1 || !logs[0].Allowed {
		t.Fatalf("approve 应留一条放行审计，实际 %+v", logs)
	}
	if !strings.Contains(logs[0].Note, cp.DisplayID()) {
		t.Fatalf("审计 note 应带检查点编号，实际 %q", logs[0].Note)
	}
}

// TestResolveCheckpoint_RejectAborts 验证 reject 后命令不执行、状态落 rejected 并留拒绝审计。
func TestResolveCheckpoint_RejectAborts(t *testing.T) {
	db := newCheckpointAPITestDB(t)
	cp := seedCheckpoint(t, db, 42, "rm -rf ./build", t.TempDir())

	code, out := callResolveCheckpoint(t, db, 1, model.RoleAdmin, cp.ID, "reject", "不允许删目录")
	if code != http.StatusOK || out["status"] != model.CheckpointRejected {
		t.Fatalf("reject 应 200 且状态 rejected，实际 code=%d out=%v", code, out)
	}
	got, _ := repo.GetCheckpoint(db, cp.ID)
	if got.Status != model.CheckpointRejected || got.Result != "" {
		t.Fatalf("reject 不应产生执行结果: %+v", got)
	}
	logs, _, _ := repo.ListAuditLogs(db, repo.AuditLogFilter{UserID: 42})
	if len(logs) != 1 || logs[0].Allowed {
		t.Fatalf("reject 应留一条未放行审计，实际 %+v", logs)
	}
}

// TestResolveCheckpoint_Guards 验证参数与权限护栏：非法 action / 重复处置 / 越权处置他人。
func TestResolveCheckpoint_Guards(t *testing.T) {
	db := newCheckpointAPITestDB(t)
	cp := seedCheckpoint(t, db, 42, "echo guard", t.TempDir())

	if code, _ := callResolveCheckpoint(t, db, 1, model.RoleAdmin, cp.ID, "maybe", ""); code != http.StatusBadRequest {
		t.Fatalf("非法 action 应 400，实际 %d", code)
	}
	if code, _ := callResolveCheckpoint(t, db, 1, model.RoleAdmin, 99999, "approve", ""); code != http.StatusNotFound {
		t.Fatalf("不存在的检查点应 404，实际 %d", code)
	}
	// viewer 处置他人的检查点应被 owner 隔离拦下。
	if code, _ := callResolveCheckpoint(t, db, 7, model.RoleViewer, cp.ID, "approve", ""); code != http.StatusForbidden {
		t.Fatalf("越权处置应 403，实际 %d", code)
	}
	// 正常处置后再次处置应 409。
	if code, _ := callResolveCheckpoint(t, db, 1, model.RoleAdmin, cp.ID, "reject", ""); code != http.StatusOK {
		t.Fatalf("首次 reject 应 200，实际 %d", code)
	}
	if code, _ := callResolveCheckpoint(t, db, 1, model.RoleAdmin, cp.ID, "approve", ""); code != http.StatusConflict {
		t.Fatalf("重复处置应 409，实际 %d", code)
	}
}
