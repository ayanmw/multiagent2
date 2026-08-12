package promptiter

import (
	"context"
	"testing"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/eval"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newTestDB 用纯 Go sqlite 建一个仅含 promptiter 测试所需表的库（免 gcc）。
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.EvalDataset{}, &model.EvalCase{}, &model.AgentInstruction{}, &model.PromptIterRun{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	return db
}

// mockReflector 是 Reflector 的测试桩：返回预设的改进指令与理由，并记录调用次数。
type mockReflector struct {
	improved  string
	reasoning string
	calls     int
}

func (m *mockReflector) Reflect(_ context.Context, _ uint, _ string, _ []WeakCase) (string, string, error) {
	m.calls++
	return m.improved, m.reasoning, nil
}

// mockRunner 是 eval.CaseRunner 的测试桩：按 (override, input) 查表返回预设输出，
// 未命中则用 defaultOut。用于在不调真实 LLM 的前提下模拟「应用改进提示词后输出变好」。
type mockRunner struct {
	override   string
	table      map[string]string // key: override + "|" + input
	defaultOut string
}

func (m *mockRunner) RunCase(_ context.Context, _ uint, _ string, input string) (string, int64, error) {
	if out, ok := m.table[m.override+"|"+input]; ok {
		return out, 0, nil
	}
	return m.defaultOut, 0, nil
}

// newMockService 构造注入 mock 运行器的服务（resolve/judge 对 mock 运行器无影响，传 nil）。
func newMockService(db *gorm.DB, reflector Reflector, table map[string]string, defaultOut string) *Service {
	svc := NewService(db, nil, nil, reflector)
	svc.runnerFactory = func(_ eval.ModelResolver, override string, _ time.Duration) eval.CaseRunner {
		return &mockRunner{override: override, table: table, defaultOut: defaultOut}
	}
	return svc
}

// seedDataset 写入一个评估集与两条用例（精确匹配评分器），返回数据集 id。
func seedDataset(t *testing.T, db *gorm.DB, userID uint) uint {
	t.Helper()
	ds := &model.EvalDataset{
		UserID:         userID,
		Name:           "ds",
		DefaultGrader:  model.GraderExact,
		DefaultModel:   "m",
	}
	if err := repo.CreateEvalDataset(db, ds); err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	for _, in := range []string{"q1", "q2"} {
		if err := repo.CreateEvalCase(db, &model.EvalCase{
			DatasetID: ds.ID,
			Input:     in,
			Expected:  "expected",
			Grader:    model.GraderExact,
		}); err != nil {
			t.Fatalf("create case: %v", err)
		}
	}
	return ds.ID
}

// TestOptimize_Accepted：基线全错（弱项）→ 反射出改进 → 重评全对 → 接受。
func TestOptimize_Accepted(t *testing.T) {
	db := newTestDB(t)
	uid := uint(1)
	dsID := seedDataset(t, db, uid)
	reflector := &mockReflector{improved: "improved", reasoning: "r"}
	// override="improved" 时两条用例都输出 expected；基线 override="" 输出 wrong。
	table := map[string]string{
		"improved|q1": "expected",
		"improved|q2": "expected",
	}
	svc := newMockService(db, reflector, table, "wrong")

	run, err := svc.Optimize(context.Background(), Request{UserID: uid, DatasetID: dsID, Threshold: 0.5})
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}
	if run.Status != model.PromptIterStatusAccepted {
		t.Fatalf("期望 accepted，实际 %s", run.Status)
	}
	if run.BaselineScore != 0 {
		t.Fatalf("基线分应为 0，实际 %v", run.BaselineScore)
	}
	if run.CandidateScore != 1 {
		t.Fatalf("候选分应为 1，实际 %v", run.CandidateScore)
	}
	if reflector.calls != 1 {
		t.Fatalf("反射器应被调用 1 次，实际 %d", reflector.calls)
	}
	if run.BeforeContent != "" {
		t.Fatalf("改前指令应为空（无既有 override），实际 %q", run.BeforeContent)
	}
	if run.AfterContent != "improved" {
		t.Fatalf("改后指令应为 improved，实际 %q", run.AfterContent)
	}
	// 落库：AgentInstruction 应为 "improved"。
	ins, gerr := repo.GetInstruction(db, uid, model.DefaultInstructionName)
	if gerr != nil {
		t.Fatalf("get instruction: %v", gerr)
	}
	if ins.Content != "improved" {
		t.Fatalf("指令内容应为 improved，实际 %q", ins.Content)
	}
}

// TestOptimize_RolledBack：基线部分对 → 反射出「无效」改进 → 重评更差 → 回滚。
func TestOptimize_RolledBack(t *testing.T) {
	db := newTestDB(t)
	uid := uint(1)
	dsID := seedDataset(t, db, uid)
	reflector := &mockReflector{improved: "improved", reasoning: "r"}
	// 基线（override=""）：q1 对、q2 错 → 基线分 0.5；改进后（override="improved"）全错 → 0。
	table := map[string]string{
		"|q1":          "expected",
		"|q2":          "wrong",
		"improved|q1":  "wrong",
		"improved|q2":  "wrong",
	}
	svc := newMockService(db, reflector, table, "wrong")

	run, err := svc.Optimize(context.Background(), Request{UserID: uid, DatasetID: dsID, Threshold: 0.5})
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}
	if run.Status != model.PromptIterStatusRolledBack {
		t.Fatalf("期望 rolled_back，实际 %s", run.Status)
	}
	if run.BaselineScore != 0.5 {
		t.Fatalf("基线分应为 0.5，实际 %v", run.BaselineScore)
	}
	if run.CandidateScore != 0 {
		t.Fatalf("候选分应为 0，实际 %v", run.CandidateScore)
	}
	// 回滚后：AgentInstruction 应恢复为改前内容（空 → 回落引擎内置默认）。
	ins, gerr := repo.GetInstruction(db, uid, model.DefaultInstructionName)
	if gerr != nil {
		t.Fatalf("get instruction after rollback: %v", gerr)
	}
	if ins.Content != "" {
		t.Fatalf("回滚后指令内容应为空（改前为空），实际 %q", ins.Content)
	}
}

// TestOptimize_NoImprovement：基线全对 → 无弱项 → 不调反射器，状态 no_improvement。
func TestOptimize_NoImprovement(t *testing.T) {
	db := newTestDB(t)
	uid := uint(1)
	dsID := seedDataset(t, db, uid)
	reflector := &mockReflector{improved: "improved", reasoning: "r"}
	// 基线全对（default "expected"），无改进条目。
	svc := newMockService(db, reflector, map[string]string{}, "expected")

	run, err := svc.Optimize(context.Background(), Request{UserID: uid, DatasetID: dsID, Threshold: 0.5})
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}
	if run.Status != model.PromptIterStatusNoImprovement {
		t.Fatalf("期望 no_improvement，实际 %s", run.Status)
	}
	if reflector.calls != 0 {
		t.Fatalf("无弱项时反射器不应被调用，实际 %d", reflector.calls)
	}
	if run.WeakCount != 0 {
		t.Fatalf("弱项数应为 0，实际 %d", run.WeakCount)
	}
}

// TestRollback：先接受一次改进，再调用 Rollback 应把指令恢复为改前内容。
func TestRollback(t *testing.T) {
	db := newTestDB(t)
	uid := uint(1)
	dsID := seedDataset(t, db, uid)
	reflector := &mockReflector{improved: "improved", reasoning: "r"}
	table := map[string]string{
		"improved|q1": "expected",
		"improved|q2": "expected",
	}
	svc := newMockService(db, reflector, table, "wrong")

	run, err := svc.Optimize(context.Background(), Request{UserID: uid, DatasetID: dsID, Threshold: 0.5})
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}
	if run.Status != model.PromptIterStatusAccepted {
		t.Fatalf("期望 accepted，实际 %s", run.Status)
	}
	// 回滚到改前（空）。
	rb, err := svc.Rollback(context.Background(), uid, run.ID)
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if rb.Status != model.PromptIterStatusRolledBack {
		t.Fatalf("回滚后状态应为 rolled_back，实际 %s", rb.Status)
	}
	ins, gerr := repo.GetInstruction(db, uid, model.DefaultInstructionName)
	if gerr != nil {
		t.Fatalf("get instruction after rollback: %v", gerr)
	}
	if ins.Content != "" {
		t.Fatalf("回滚后指令内容应为空（改前为空），实际 %q", ins.Content)
	}
}
