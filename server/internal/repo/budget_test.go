package repo

import (
	"os"
	"testing"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// newBudgetTestDB 构造内存 SQLite（纯 Go，无需 CGO）并迁移预算 + 用量两张表。
func newBudgetTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.BudgetPolicy{}, &model.UsageRecord{}); err != nil {
		t.Fatalf("migrate budget/usage: %v", err)
	}
	return db
}

// seedBudgetUsage 落一条用量记录（今日，命中 daily 窗口）。
func seedBudgetUsage(t *testing.T, db *gorm.DB, uid uint, sessionKey string, total int) {
	t.Helper()
	rec := &model.UsageRecord{
		UserID:      uid,
		SessionID:   uint(1),
		SessionKey:  sessionKey,
		ProviderID:  1,
		ModelID:     1,
		ModelName:   "test-model",
		TotalTokens: total,
	}
	rec.CreatedAt = time.Now()
	if err := CreateUsageRecord(db, rec); err != nil {
		t.Fatalf("seed usage: %v", err)
	}
}

// TestEvaluateBudgets_UserBlocked 验收 M3-04 核心：设极低全局用户阈值 → 累计超阈值即拦截。
func TestEvaluateBudgets_UserBlocked(t *testing.T) {
	db := newBudgetTestDB(t)
	// 全局默认用户预算：daily 窗口上限 50 token（极低阈值）。
	if err := UpsertBudgetPolicy(db, &model.BudgetPolicy{
		Scope: model.BudgetScopeUser, ScopeKey: "", MaxTokens: 50, Window: model.BudgetWindowDaily,
	}); err != nil {
		t.Fatalf("upsert policy: %v", err)
	}
	// 模拟「第一轮」对话已消耗 120 token（落库）。
	seedBudgetUsage(t, db, 42, "sk-a", 120)

	ev, err := EvaluateBudgets(db, 42, "sk-a", "")
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if !ev.Blocked {
		t.Fatal("应触发预算拦截，实际未拦截")
	}
	if ev.Scope != model.BudgetScopeUser || ev.Used != 120 || ev.Max != 50 {
		t.Fatalf("拦截结果异常: %+v", ev)
	}
}

// TestEvaluateBudgets_Under 阈值内不拦截。
func TestEvaluateBudgets_Under(t *testing.T) {
	db := newBudgetTestDB(t)
	if err := UpsertBudgetPolicy(db, &model.BudgetPolicy{
		Scope: model.BudgetScopeUser, ScopeKey: "", MaxTokens: 1000, Window: model.BudgetWindowDaily,
	}); err != nil {
		t.Fatalf("upsert policy: %v", err)
	}
	seedBudgetUsage(t, db, 42, "sk-a", 120)
	ev, err := EvaluateBudgets(db, 42, "sk-a", "")
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if ev.Blocked {
		t.Fatalf("阈值内不应拦截: %+v", ev)
	}
}

// TestEvaluateBudgets_RaiseRecovers 管理员提额后恢复（验收「提额后恢复」）。
func TestEvaluateBudgets_RaiseRecovers(t *testing.T) {
	db := newBudgetTestDB(t)
	if err := UpsertBudgetPolicy(db, &model.BudgetPolicy{
		Scope: model.BudgetScopeUser, ScopeKey: "", MaxTokens: 50, Window: model.BudgetWindowDaily,
	}); err != nil {
		t.Fatalf("upsert policy: %v", err)
	}
	seedBudgetUsage(t, db, 42, "sk-a", 120)

	if ev, _ := EvaluateBudgets(db, 42, "sk-a", ""); !ev.Blocked {
		t.Fatal("提额前应被拦")
	}
	// 管理员提额到 100000。
	if err := UpsertBudgetPolicy(db, &model.BudgetPolicy{
		Scope: model.BudgetScopeUser, ScopeKey: "", MaxTokens: 100000, Window: model.BudgetWindowDaily,
	}); err != nil {
		t.Fatalf("raise policy: %v", err)
	}
	if ev, _ := EvaluateBudgets(db, 42, "sk-a", ""); ev.Blocked {
		t.Fatalf("提额后应恢复，仍被拦: %+v", ev)
	}
}

// TestEvaluateBudgets_SessionScope 会话级策略拦截（独立于用户级）。
func TestEvaluateBudgets_SessionScope(t *testing.T) {
	db := newBudgetTestDB(t)
	// 不设用户级策略；仅给该会话设上限 10。
	if err := UpsertBudgetPolicy(db, &model.BudgetPolicy{
		Scope: model.BudgetScopeSession, ScopeKey: "sk-x", MaxTokens: 10, Window: model.BudgetWindowDaily,
	}); err != nil {
		t.Fatalf("upsert session policy: %v", err)
	}
	seedBudgetUsage(t, db, 42, "sk-x", 20)
	ev, err := EvaluateBudgets(db, 42, "sk-x", "")
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if !ev.Blocked || ev.Scope != model.BudgetScopeSession {
		t.Fatalf("应触发会话级拦截: %+v", ev)
	}
	// 不同会话不受影响。
	if ev, _ := EvaluateBudgets(db, 42, "sk-other", ""); ev.Blocked {
		t.Fatalf("其它会话不应被拦: %+v", ev)
	}
}

// TestEvaluateBudgets_Disabled 总开关关闭时直接放行。
func TestEvaluateBudgets_Disabled(t *testing.T) {
	db := newBudgetTestDB(t)
	if err := UpsertBudgetPolicy(db, &model.BudgetPolicy{
		Scope: model.BudgetScopeUser, ScopeKey: "", MaxTokens: 1, Window: model.BudgetWindowDaily,
	}); err != nil {
		t.Fatalf("upsert policy: %v", err)
	}
	seedBudgetUsage(t, db, 42, "sk-a", 999)
	os.Setenv("BUDGET_ENABLED", "false")
	defer os.Unsetenv("BUDGET_ENABLED")
	if ev, err := EvaluateBudgets(db, 42, "sk-a", ""); err != nil || ev.Blocked {
		t.Fatalf("关闭开关后应放行: err=%v ev=%+v", err, ev)
	}
}

// TestUpsertBudgetPolicy_CreateThenUpdate 同一 (scope, scope_key) 第二次 upsert 应更新而非新增。
func TestUpsertBudgetPolicy_CreateThenUpdate(t *testing.T) {
	db := newBudgetTestDB(t)
	p := &model.BudgetPolicy{Scope: model.BudgetScopeUser, ScopeKey: "7", MaxTokens: 100, Window: model.BudgetWindowDaily}
	if err := UpsertBudgetPolicy(db, p); err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.ID == 0 {
		t.Fatal("create 后应有自增 id")
	}
	// 更新同一个 key 的阈值。
	if err := UpsertBudgetPolicy(db, &model.BudgetPolicy{Scope: model.BudgetScopeUser, ScopeKey: "7", MaxTokens: 200, Window: model.BudgetWindowDaily}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := GetBudgetPolicy(db, model.BudgetScopeUser, "7")
	if err != nil || got == nil {
		t.Fatalf("get: err=%v got=%v", err, got)
	}
	if got.MaxTokens != 200 {
		t.Fatalf("应更新为 200，实际 %d", got.MaxTokens)
	}
	var cnt int64
	db.Model(&model.BudgetPolicy{}).Count(&cnt)
	if cnt != 1 {
		t.Fatalf("upsert 不应新增行，实际 %d", cnt)
	}
}

// TestGetEffectiveUserBudgetPolicy_SpecificOverridesGlobal 用户特定策略优先于全局默认。
func TestGetEffectiveUserBudgetPolicy_SpecificOverridesGlobal(t *testing.T) {
	db := newBudgetTestDB(t)
	if err := UpsertBudgetPolicy(db, &model.BudgetPolicy{Scope: model.BudgetScopeUser, ScopeKey: "", MaxTokens: 1000, Window: model.BudgetWindowDaily}); err != nil {
		t.Fatalf("global: %v", err)
	}
	if err := UpsertBudgetPolicy(db, &model.BudgetPolicy{Scope: model.BudgetScopeUser, ScopeKey: "7", MaxTokens: 50, Window: model.BudgetWindowDaily}); err != nil {
		t.Fatalf("specific: %v", err)
	}
	got, err := GetEffectiveUserBudgetPolicy(db, 7)
	if err != nil || got == nil {
		t.Fatalf("get: err=%v got=%v", err, got)
	}
	if got.MaxTokens != 50 {
		t.Fatalf("应使用用户特定 50，实际 %d", got.MaxTokens)
	}
	// 无特定策略的用户回落全局默认。
	got2, err := GetEffectiveUserBudgetPolicy(db, 99)
	if err != nil || got2 == nil || got2.MaxTokens != 1000 {
		t.Fatalf("应回落全局 1000: err=%v got=%v", err, got2)
	}
}
