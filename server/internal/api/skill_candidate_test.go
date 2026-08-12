package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/ayanmw/multiagent2/server/internal/middleware"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// newSkillCandidateTestDB 构造内存 SQLite（纯 Go，无 CGO）并迁移 skill_candidates 表。
func newSkillCandidateTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.SkillCandidate{}); err != nil {
		t.Fatalf("migrate skill_candidates: %v", err)
	}
	return db
}

// seedPendingCandidate 精确落一条 pending 候选（可控 name/body）。
func seedPendingCandidate(t *testing.T, db *gorm.DB, uid uint, name, body string) uint {
	t.Helper()
	cand := &model.SkillCandidate{
		UserID:      uid,
		Name:        name,
		Description: "测试候选",
		Body:        body,
		Status:      model.SkillCandidatePending,
	}
	if err := db.Create(cand).Error; err != nil {
		t.Fatalf("seed candidate: %v", err)
	}
	return cand.ID
}

// callResolve 以指定身份请求审批接口，返回状态码与解析后的候选视图。
func callResolve(t *testing.T, db *gorm.DB, sharedRoot string, uid uint, id uint64, decision, reason string) (int, skillCandidateView) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set(middleware.CtxUserID, uid)
	payload, _ := json.Marshal(resolveSkillCandidateBody{Decision: decision, RejectReason: reason})
	c.Request, _ = http.NewRequest(http.MethodPost, "/api/skill-candidates/resolve", bytes.NewReader(payload))
	c.Params = []gin.Param{{Key: "id", Value: strconv.FormatUint(id, 10)}}

	ResolveSkillCandidateHandler(db, sharedRoot)(c)

	var out skillCandidateView
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("解析响应失败: %v (body=%s)", err, w.Body.String())
		}
	}
	return w.Code, out
}

// TestResolveSkillCandidate_PublishOnApprove 验收 M5-04 核心：approve → 发布到共享技能库 + 状态 approved。
func TestResolveSkillCandidate_PublishOnApprove(t *testing.T) {
	db := newSkillCandidateTestDB(t)
	sharedRoot := t.TempDir()
	uid := uint(42)
	id := seedPendingCandidate(t, db, uid, "deploy_docker", "---\nname: deploy_docker\n---\n# 部署\n用 docker 部署。")

	code, out := callResolve(t, db, sharedRoot, uid, uint64(id), "approve", "")
	if code != http.StatusOK {
		t.Fatalf("approve 期望 200，实际 %d (body=%s)", code, "n/a")
	}
	if out.Status != string(model.SkillCandidateApproved) {
		t.Fatalf("approve 后状态应 approved，实际 %s", out.Status)
	}

	// 共享库文件应已落盘：<sharedRoot>/deploy_docker/SKILL.md 且内容一致。
	published := filepath.Join(sharedRoot, "deploy_docker", "SKILL.md")
	data, err := os.ReadFile(published)
	if err != nil {
		t.Fatalf("approve 后共享库文件未落盘: %v", err)
	}
	if string(data) != "---\nname: deploy_docker\n---\n# 部署\n用 docker 部署。" {
		t.Fatalf("发布内容不一致: %q", string(data))
	}

	// DB 状态应为 approved。
	got, gerr := repo.GetSkillCandidate(db, id, uid)
	if gerr != nil || got == nil {
		t.Fatalf("读候选失败: %v got=%v", gerr, got)
	}
	if got.Status != model.SkillCandidateApproved {
		t.Fatalf("DB 状态应 approved，实际 %s", got.Status)
	}
}

// TestResolveSkillCandidate_RejectNoPublish 验收：reject → 不落盘 + 状态 rejected。
func TestResolveSkillCandidate_RejectNoPublish(t *testing.T) {
	db := newSkillCandidateTestDB(t)
	sharedRoot := t.TempDir()
	uid := uint(42)
	id := seedPendingCandidate(t, db, uid, "write_tests", "写单元测试的技能。")

	code, out := callResolve(t, db, sharedRoot, uid, uint64(id), "reject", "过于通用")
	if code != http.StatusOK {
		t.Fatalf("reject 期望 200，实际 %d", code)
	}
	if out.Status != string(model.SkillCandidateRejected) {
		t.Fatalf("reject 后状态应 rejected，实际 %s", out.Status)
	}
	if out.RejectReason != "过于通用" {
		t.Fatalf("reject_reason 应透传，实际 %q", out.RejectReason)
	}

	// 共享库不应落盘任何技能目录。
	entries, err := os.ReadDir(sharedRoot)
	if err != nil {
		t.Fatalf("读共享根失败: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("reject 不应落盘，实际有 %d 个条目", len(entries))
	}
}

// TestResolveSkillCandidate_NotFound 验收：越权/不存在的候选返回 404。
func TestResolveSkillCandidate_NotFound(t *testing.T) {
	db := newSkillCandidateTestDB(t)
	sharedRoot := t.TempDir()
	// uid=99 非归属用户，查不到 uid=42 的候选。
	seedPendingCandidate(t, db, 42, "x", "y")
	code, _ := callResolve(t, db, sharedRoot, 99, uint64(1), "approve", "")
	if code != http.StatusNotFound {
		t.Fatalf("越权/不存在候选应 404，实际 %d", code)
	}
}
