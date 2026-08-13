package api

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/regression"
	"github.com/ayanmw/multiagent2/server/internal/repo"
)

// mockRegressionChecker 是回归检查器的测试桩（M5-08 门禁单测用）：
// pass 控制 Check 是否通过；registerID 提供稳定的评估集/用例 id。
type mockRegressionChecker struct {
	pass       bool
	registerID uint
}

func (m *mockRegressionChecker) Register(_ context.Context, _ uint, _ *model.SkillCandidate) (uint, uint, error) {
	return m.registerID, m.registerID + 1, nil
}

func (m *mockRegressionChecker) Check(_ context.Context, _ uint, _ uint) (regression.Report, error) {
	return regression.Report{Passed: m.pass, Detail: "mock report"}, nil
}

// TestResolveSkillCandidate_RegressionPass_GateAllowsPublish 验收 M5-08 门禁放行：
// 回归检查器置为「通过」时，审批发布照常落盘共享库且状态 approved。
func TestResolveSkillCandidate_RegressionPass_GateAllowsPublish(t *testing.T) {
	db := newSkillCandidateTestDB(t)
	sharedRoot := t.TempDir()
	uid := uint(42)
	id := seedPendingCandidate(t, db, uid, "deploy_docker", "---\nname: deploy_docker\n---\n# 部署\n用 docker 部署。")

	SetRegressionChecker(&mockRegressionChecker{pass: true, registerID: 1})
	defer SetRegressionChecker(nil)

	code, out := callResolve(t, db, sharedRoot, uid, uint64(id), "approve", "")
	if code != http.StatusOK {
		t.Fatalf("回归通过时应放行发布，期望 200，实际 %d", code)
	}
	if out.Status != string(model.SkillCandidateApproved) {
		t.Fatalf("回归通过后状态应 approved，实际 %s", out.Status)
	}
	// 共享库文件应已落盘。
	published := filepath.Join(sharedRoot, "deploy_docker", "SKILL.md")
	if _, err := os.Stat(published); err != nil {
		t.Fatalf("回归通过后共享库文件应落盘: %v", err)
	}
	// DB 状态应为 approved。
	got, gerr := repo.GetSkillCandidate(db, id, uid)
	if gerr != nil || got == nil || got.Status != model.SkillCandidateApproved {
		t.Fatalf("DB 状态应 approved，实际 got=%v err=%v", got, gerr)
	}
}

// TestResolveSkillCandidate_RegressionBlock_GateRollback 验收 M5-08 门禁拦截：
// 回归检查器置为「未通过」时，审批返回 409、共享库回滚不落盘、DB 状态保持 pending。
func TestResolveSkillCandidate_RegressionBlock_GateRollback(t *testing.T) {
	db := newSkillCandidateTestDB(t)
	sharedRoot := t.TempDir()
	uid := uint(42)
	id := seedPendingCandidate(t, db, uid, "deploy_docker", "---\nname: deploy_docker\n---\n# 部署\n用 docker 部署。")

	SetRegressionChecker(&mockRegressionChecker{pass: false, registerID: 1})
	defer SetRegressionChecker(nil)

	code, _ := callResolve(t, db, sharedRoot, uid, uint64(id), "approve", "")
	if code != http.StatusConflict {
		t.Fatalf("回归未通过时应 409 拦截，期望 409，实际 %d", code)
	}
	// 共享库应已回滚（试探发布后被 RemoveShared 清理）。
	entries, err := os.ReadDir(sharedRoot)
	if err != nil {
		t.Fatalf("读共享根失败: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("回归拦截后共享库不应落盘，实际 %d 个条目", len(entries))
	}
	// DB 状态应仍为 pending（未翻转）。
	got, gerr := repo.GetSkillCandidate(db, id, uid)
	if gerr != nil || got == nil {
		t.Fatalf("读候选失败: %v got=%v", gerr, got)
	}
	if got.Status != model.SkillCandidatePending {
		t.Fatalf("回归拦截后 DB 状态应仍为 pending，实际 %s", got.Status)
	}
}
