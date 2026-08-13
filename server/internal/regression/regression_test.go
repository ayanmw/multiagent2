package regression

import (
	"context"
	"testing"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/engine"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// newRegTestDB 建内存 SQLite（纯 Go，无 CGO）并迁移评估四表。
func newRegTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if s, err := db.DB(); err == nil {
		s.SetMaxOpenConns(1)
	}
	if err := db.AutoMigrate(&model.EvalDataset{}, &model.EvalCase{}, &model.EvalRun{}, &model.EvalResult{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// dummyResolveSkill 是 ResolverWithSkill 的测试桩（Register 不调用；Check 调用但 mock runner 不真正用）。
func dummyResolveSkill(_ context.Context, _ uint, _ string, _ string) (engine.ModelConfig, error) {
	return engine.ModelConfig{ModelID: "m"}, nil
}

// TestEvalChecker_Register_CreatesDatasetAndCase 验收 M5-08「新发布技能自动进 eval 集」：
// Register 自动建「skill:<name>」评估集 + 一条用例；重复调用幂等（不重复建）。
func TestEvalChecker_Register_CreatesDatasetAndCase(t *testing.T) {
	db := newRegTestDB(t)
	chk := NewEvalChecker(db, dummyResolveSkill, 5*time.Second, 1.0)
	cand := &model.SkillCandidate{UserID: 1, Name: "deploy_docker", Description: "部署到 docker", Body: "x"}

	dsID, caseID, err := chk.Register(context.Background(), 1, cand)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if dsID == 0 || caseID == 0 {
		t.Fatalf("expected nonzero ids, got ds=%d case=%d", dsID, caseID)
	}
	ds, err := repo.GetEvalDataset(db, 1, dsID)
	if err != nil {
		t.Fatalf("get dataset: %v", err)
	}
	if ds.Name != "skill:deploy_docker" {
		t.Fatalf("dataset name 应为 skill:deploy_docker，实际 %q", ds.Name)
	}
	cases, err := repo.ListEvalCases(db, dsID)
	if err != nil {
		t.Fatalf("list cases: %v", err)
	}
	if len(cases) != 1 {
		t.Fatalf("首次 Register 应建 1 条用例，实际 %d", len(cases))
	}

	// 幂等：再次 Register 不重复建评估集/用例。
	dsID2, caseID2, err := chk.Register(context.Background(), 1, cand)
	if err != nil {
		t.Fatalf("Register(2): %v", err)
	}
	if dsID2 != dsID || caseID2 != caseID {
		t.Fatalf("非幂等：首次 ds/case=%d/%d，再次=%d/%d", dsID, caseID, dsID2, caseID2)
	}
	cases2, _ := repo.ListEvalCases(db, dsID)
	if len(cases2) != 1 {
		t.Fatalf("重复 Register 不应新增用例，实际 %d", len(cases2))
	}
}
