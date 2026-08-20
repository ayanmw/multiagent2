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

// TestEvalChecker_Register_SelfGeneratedCases 验收 M8-05「评估集自举」：Register 从
// 技能正文反向生成多条用例（保底+标题+命令）；重复调用按 Input 幂等，不重复建。
func TestEvalChecker_Register_SelfGeneratedCases(t *testing.T) {
	db := newRegTestDB(t)
	chk := NewEvalChecker(db, dummyResolveSkill, 5*time.Second, 1.0)
	cand := &model.SkillCandidate{
		UserID:      1,
		Name:        "git-flow",
		Description: "Git 分支模型与提交规范",
		Body:        cmdRichBody,
	}

	dsID, _, err := chk.Register(context.Background(), 1, cand)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	cases, err := repo.ListEvalCases(db, dsID)
	if err != nil {
		t.Fatalf("list cases: %v", err)
	}
	if len(cases) < 4 {
		t.Fatalf("技能正文应自举生成 ≥4 条用例，实际 %d", len(cases))
	}

	// 幂等：再次 Register 不新增任何用例。
	if _, _, err := chk.Register(context.Background(), 1, cand); err != nil {
		t.Fatalf("Register(2): %v", err)
	}
	cases2, _ := repo.ListEvalCases(db, dsID)
	if len(cases2) != len(cases) {
		t.Fatalf("重复 Register 不应新增用例：首次 %d，再次 %d", len(cases), len(cases2))
	}

	// 用例确实覆盖标题与命令断言（而非只有保底一条）。
	var hasHeading, hasCmd bool
	for _, c := range cases {
		if c.Expected == "分支策略" || c.Expected == "合并流程" {
			hasHeading = true
		}
		if c.Expected == "git worktree" || c.Expected == "git merge" {
			hasCmd = true
		}
	}
	if !hasHeading || !hasCmd {
		t.Fatalf("自举用例应同时覆盖标题与命令断言（heading=%v cmd=%v）", hasHeading, hasCmd)
	}
}

// TestBaselineOf 验收 M8-05「回归分数可比」的基线选择：取最近一次 done 且非本次运行的
// 记录；running/failed 与本次运行不计入；无历史时 found=false。
func TestBaselineOf(t *testing.T) {
	now := time.Now()
	runs := []model.EvalRun{
		{Model: gorm.Model{ID: 1, CreatedAt: now.Add(-3 * time.Hour)}, Status: model.EvalRunStatusDone, ScoreAvg: 0.8, PassRate: 0.75},
		{Model: gorm.Model{ID: 2, CreatedAt: now.Add(-2 * time.Hour)}, Status: model.EvalRunStatusRunning},
		{Model: gorm.Model{ID: 3, CreatedAt: now.Add(-1 * time.Hour)}, Status: model.EvalRunStatusDone, ScoreAvg: 0.9, PassRate: 0.85},
		{Model: gorm.Model{ID: 4, CreatedAt: now.Add(-30 * time.Minute)}, Status: model.EvalRunStatusFailed},
	}
	// 列表按 created_at desc：顺序为 4(failed)、3(当前)、2(running)、1(done)。
	// 从前往后跳过 currentID=3、running、failed，命中 ID=1（done）。
	bs, bp, found := baselineOf(runs, 3)
	if !found {
		t.Fatal("应找到基线")
	}
	if bs != 0.8 || bp != 0.75 {
		t.Fatalf("基线应为 ID=1 的 0.8/0.75，实际 %.2f/%.2f", bs, bp)
	}
	// 全部非 done（running/failed）：找不到基线。
	allNonDone := []model.EvalRun{
		{Model: gorm.Model{ID: 5, CreatedAt: now.Add(-10 * time.Minute)}, Status: model.EvalRunStatusRunning},
		{Model: gorm.Model{ID: 6, CreatedAt: now.Add(-5 * time.Minute)}, Status: model.EvalRunStatusFailed},
	}
	if _, _, found := baselineOf(allNonDone, 6); found {
		t.Fatal("无 done 历史（全部跳过）时应 found=false")
	}
	// 无任何历史（空列表）。
	if _, _, found := baselineOf(nil, 1); found {
		t.Fatal("空历史应 found=false")
	}
}
