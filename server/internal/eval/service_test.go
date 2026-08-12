package eval

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

// newTestDB 建一个单连接的内存 SQLite（避免 glebarez 共享缓存跨连接串扰），并建出评估四表。
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// 单连接保证 :memory: 数据库在测试内唯一。
	if s, err := db.DB(); err == nil {
		s.SetMaxOpenConns(1)
	}
	if err := db.AutoMigrate(&model.EvalDataset{}, &model.EvalCase{}, &model.EvalRun{}, &model.EvalResult{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// mockRunner 是 CaseRunner 的 mock：固定返回 output（或 err），记录调用次数。
type mockRunner struct {
	output string
	err    error
	calls  int
}

func (m *mockRunner) RunCase(_ context.Context, _ uint, _ string, _ string) (string, int64, error) {
	m.calls++
	return m.output, 10, m.err
}

// mockJudge 是 Judge 的 mock：固定返回 score（或 err）。
type mockJudge struct {
	score float64
	err   error
}

func (m *mockJudge) Judge(_ context.Context, _ uint, _, _, _ string) (float64, error) {
	return m.score, m.err
}

// dummyResolve 是 ModelResolver 的测试桩（mock runner/judge 不真正用模型配置）。
func dummyResolve(_ context.Context, _ uint, _ string) (engine.ModelConfig, error) {
	return engine.ModelConfig{ModelID: "m"}, nil
}

// waitRun 轮询运行记录直到非 running 或超时（StartRun 异步执行）。
func waitRun(t *testing.T, db *gorm.DB, uid, runID uint) *model.EvalRun {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r, err := repo.GetEvalRun(db, uid, runID)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		if r.Status != model.EvalRunStatusRunning {
			return r
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("run %d 在超时内未结束", runID)
	return nil
}

func seedDataset(t *testing.T, db *gorm.DB, uid uint, grader model.GraderType, expected string) (uint, uint) {
	t.Helper()
	ds := &model.EvalDataset{
		UserID:        uid,
		Name:          "ds-" + string(grader),
		DefaultGrader: grader,
		DefaultModel:  "m1",
	}
	if err := ds.Validate(); err != nil {
		t.Fatalf("validate dataset: %v", err)
	}
	if err := repo.CreateEvalDataset(db, ds); err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	cs := &model.EvalCase{
		DatasetID: ds.ID,
		Input:     "1+1=?",
		Expected:  expected,
	}
	if err := cs.Validate(); err != nil {
		t.Fatalf("validate case: %v", err)
	}
	if err := repo.CreateEvalCase(db, cs); err != nil {
		t.Fatalf("create case: %v", err)
	}
	return ds.ID, cs.ID
}

func TestService_RunExactPass(t *testing.T) {
	db := newTestDB(t)
	dsID, _ := seedDataset(t, db, 1, model.GraderExact, "2")
	svc := NewService(db, dummyResolve, &mockRunner{output: "2"}, nil)

	run, err := svc.StartRun(context.Background(), 1, dsID, "", "", 3)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	got := waitRun(t, db, 1, run.ID)
	if got.Status != model.EvalRunStatusDone {
		t.Fatalf("status=%s err=%q", got.Status, got.Error)
	}
	if got.TotalCases != 1 || got.TotalAttempts != 3 {
		t.Fatalf("totals: cases=%d attempts=%d", got.TotalCases, got.TotalAttempts)
	}
	if got.ScoreAvg != 1.0 || got.PassRate != 1.0 {
		t.Fatalf("score_avg=%.2f pass_rate=%.2f", got.ScoreAvg, got.PassRate)
	}
	results, err := repo.ListEvalResults(db, run.ID)
	if err != nil {
		t.Fatalf("list results: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Score != 1.0 || !r.Passed {
			t.Fatalf("result attempt %d score=%.1f passed=%v", r.Attempt, r.Score, r.Passed)
		}
	}
}

func TestService_RunExactFail(t *testing.T) {
	db := newTestDB(t)
	dsID, _ := seedDataset(t, db, 1, model.GraderExact, "2")
	svc := NewService(db, dummyResolve, &mockRunner{output: "3"}, nil)

	run, err := svc.StartRun(context.Background(), 1, dsID, "", "", 2)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	got := waitRun(t, db, 1, run.ID)
	if got.Status != model.EvalRunStatusDone {
		t.Fatalf("status=%s err=%q", got.Status, got.Error)
	}
	if got.ScoreAvg != 0.0 || got.PassRate != 0.0 {
		t.Fatalf("score_avg=%.2f pass_rate=%.2f", got.ScoreAvg, got.PassRate)
	}
}

func TestService_RunContainsPass(t *testing.T) {
	db := newTestDB(t)
	dsID, _ := seedDataset(t, db, 1, model.GraderContains, "hello")
	// 数据集默认评分器 contains，用例期望 "hello"，runner 返回包含 hello 的文本。
	svc := NewService(db, dummyResolve, &mockRunner{output: "the answer is hello"}, nil)
	run, err := svc.StartRun(context.Background(), 1, dsID, "", "", 1)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	got := waitRun(t, db, 1, run.ID)
	if got.Status != model.EvalRunStatusDone || got.ScoreAvg != 1.0 {
		t.Fatalf("status=%s score=%.2f", got.Status, got.ScoreAvg)
	}
}

func TestService_RunLLMGrader(t *testing.T) {
	db := newTestDB(t)
	dsID, _ := seedDataset(t, db, 1, model.GraderLLM, "2")
	svc := NewService(db, dummyResolve, &mockRunner{output: "some output"}, &mockJudge{score: 0.8})

	run, err := svc.StartRun(context.Background(), 1, dsID, "", "", 1)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	got := waitRun(t, db, 1, run.ID)
	if got.Status != model.EvalRunStatusDone {
		t.Fatalf("status=%s err=%q", got.Status, got.Error)
	}
	if got.ScoreAvg != 0.8 || got.PassRate != 1.0 {
		t.Fatalf("score_avg=%.2f pass_rate=%.2f", got.ScoreAvg, got.PassRate)
	}
}

func TestService_RunCaseErrorMarksFailed(t *testing.T) {
	db := newTestDB(t)
	dsID, _ := seedDataset(t, db, 1, model.GraderExact, "2")
	svc := NewService(db, dummyResolve, &mockRunner{err: context.DeadlineExceeded}, nil)

	run, err := svc.StartRun(context.Background(), 1, dsID, "", "", 1)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	got := waitRun(t, db, 1, run.ID)
	if got.Status != model.EvalRunStatusFailed {
		t.Fatalf("expected failed, got %s", got.Status)
	}
	if got.Error == "" {
		t.Fatalf("expected error message")
	}
	results, _ := repo.ListEvalResults(db, run.ID)
	if len(results) != 1 || results[0].Error == "" {
		t.Fatalf("expected one result with error, got %d", len(results))
	}
}

func TestService_StartRunEmptyDataset(t *testing.T) {
	db := newTestDB(t)
	ds := &model.EvalDataset{UserID: 1, Name: "empty", DefaultGrader: model.GraderExact}
	_ = ds.Validate()
	_ = ds.Validate()
	if err := repo.CreateEvalDataset(db, ds); err != nil {
		t.Fatalf("create: %v", err)
	}
	svc := NewService(db, dummyResolve, &mockRunner{output: "2"}, nil)
	if _, err := svc.StartRun(context.Background(), 1, ds.ID, "", "", 1); err == nil {
		t.Fatalf("expected error for empty dataset")
	}
}

func TestService_StartRunOwnerIsolation(t *testing.T) {
	db := newTestDB(t)
	dsID, _ := seedDataset(t, db, 7, model.GraderExact, "2")
	svc := NewService(db, dummyResolve, &mockRunner{output: "2"}, nil)
	// 不同用户（uid=9）对该数据集无归属，应返回 not found 错误。
	if _, err := svc.StartRun(context.Background(), 9, dsID, "", "", 1); err == nil {
		t.Fatalf("expected owner-isolation error")
	}
}
