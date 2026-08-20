package repo

import (
	"testing"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// newTenantTestDB 构造内存 SQLite（纯 Go，无需 CGO）并迁移租户/用户/用量/预算四表。
// 与 newBudgetTestDB 的区别：含 users 与 tenants 表（租户聚合与成员管理需要）。
func newTenantTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.Tenant{}, &model.User{}, &model.Role{}, &model.UsageRecord{}, &model.BudgetPolicy{}); err != nil {
		t.Fatalf("migrate tenant tables: %v", err)
	}
	return db
}

// seedTenantUser 创建带租户归属的用户（PasswordHash 随便填，仅测聚合不测登录）。
func seedTenantUser(t *testing.T, db *gorm.DB, id uint, tid *uint) {
	t.Helper()
	u := &model.User{Username: "u" + string(rune('a'+id%26)) + itoa(int(id)), Email: itoa(int(id)) + "@t.test", PasswordHash: "x", TenantID: tid}
	u.ID = id
	if err := db.Create(u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// seedTenantUsage 落一条带 workspace_key 的用量记录（今日，命中 daily 窗口）。
func seedTenantUsage(t *testing.T, db *gorm.DB, uid uint, wsKey string, total int) {
	t.Helper()
	rec := &model.UsageRecord{
		UserID:       uid,
		SessionID:    1,
		SessionKey:   "sk-" + wsKey,
		WorkspaceKey: wsKey,
		ProviderID:   1,
		ModelID:      1,
		ModelName:    "test-model",
		TotalTokens:  total,
	}
	if err := CreateUsageRecord(db, rec); err != nil {
		t.Fatalf("seed usage: %v", err)
	}
}

// TestTenant_CRUDAndMember 验收 M8-09 租户 CRUD 与成员管理。
func TestTenant_CRUDAndMember(t *testing.T) {
	db := newTenantTestDB(t)

	// 创建。
	t1 := &model.Tenant{Name: "tenant-a", Description: "租户 A", Status: model.TenantStatusActive, CreatedBy: 1}
	if err := CreateTenant(db, t1); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	if t1.ID == 0 {
		t.Fatal("create 后应有自增 id")
	}
	// 同名冲突。
	if err := CreateTenant(db, &model.Tenant{Name: "tenant-a"}); err == nil {
		t.Fatal("同名租户应冲突")
	}

	// 列表 / 详情。
	list, err := ListTenants(db)
	if err != nil || len(list) != 1 {
		t.Fatalf("list tenants: err=%v list=%d", err, len(list))
	}
	got, err := GetTenant(db, t1.ID)
	if err != nil || got.Name != "tenant-a" {
		t.Fatalf("get tenant: err=%v got=%+v", err, got)
	}

	// 成员加入（先建两个用户）。
	seedTenantUser(t, db, 10, nil)
	seedTenantUser(t, db, 11, nil)
	if err := MoveUserToTenant(db, 10, t1.ID); err != nil {
		t.Fatalf("move user 10: %v", err)
	}
	if err := MoveUserToTenant(db, 11, t1.ID); err != nil {
		t.Fatalf("move user 11: %v", err)
	}
	cnt, err := CountTenantUsers(db, t1.ID)
	if err != nil || cnt != 2 {
		t.Fatalf("member count: err=%v cnt=%d", err, cnt)
	}

	// 移出成员。
	if err := MoveUserToTenant(db, 11, 0); err != nil {
		t.Fatalf("remove user 11: %v", err)
	}
	cnt, _ = CountTenantUsers(db, t1.ID)
	if cnt != 1 {
		t.Fatalf("移出后成员数应=1, got %d", cnt)
	}

	// 删除：仍有成员 → 拒绝。
	if err := DeleteTenant(db, t1.ID); err != ErrTenantNotEmpty {
		t.Fatalf("有成员应拒绝删除, got %v", err)
	}
	// 移出全部后删除成功。
	if err := MoveUserToTenant(db, 10, 0); err != nil {
		t.Fatalf("remove user 10: %v", err)
	}
	if err := DeleteTenant(db, t1.ID); err != nil {
		t.Fatalf("无成员应可删除, got %v", err)
	}
	if _, err := GetTenant(db, t1.ID); err != ErrTenantNotFound {
		t.Fatalf("删除后应查不到, got %v", err)
	}
}

// TestEvaluateBudgets_WorkspaceScope 验收 M8-09 workspace 作用域预算：
// 绑定该 workspace 的会话累计 token 超限即拦截；其他 workspace 不受影响。
func TestEvaluateBudgets_WorkspaceScope(t *testing.T) {
	db := newTenantTestDB(t)
	// 仅给 workspace ws-a 设上限 50。
	if err := UpsertBudgetPolicy(db, &model.BudgetPolicy{
		Scope: model.BudgetScopeWorkspace, ScopeKey: "ws-a", MaxTokens: 50, Window: model.BudgetWindowDaily,
	}); err != nil {
		t.Fatalf("upsert workspace policy: %v", err)
	}
	seedTenantUsage(t, db, 42, "ws-a", 60)

	ev, err := EvaluateBudgets(db, 42, "sk-ws-a", "ws-a", "")
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if !ev.Blocked || ev.Scope != model.BudgetScopeWorkspace {
		t.Fatalf("应触发 workspace 级拦截: %+v", ev)
	}
	if ev.Used != 60 || ev.Max != 50 {
		t.Fatalf("拦截明细异常: %+v", ev)
	}
	// 其他 workspace（无策略）不受影响。
	if ev2, _ := EvaluateBudgets(db, 42, "sk-ws-b", "ws-b", ""); ev2.Blocked {
		t.Fatalf("其它 workspace 不应被拦: %+v", ev2)
	}
	// 不传 workspaceKey（默认目录会话）不参与 workspace 聚合。
	if ev3, _ := EvaluateBudgets(db, 42, "sk-x", "", ""); ev3.Blocked {
		t.Fatalf("默认目录会话不应被拦: %+v", ev3)
	}
}

// TestEvaluateBudgets_TenantIsolation 验收 M8-09 核心：「租户 A 超配额不影响 B」。
// 租户 A 用户共享租户级上限；超限只拦 A 内用户；租户 B 用户与独立用户完全不受影响。
func TestEvaluateBudgets_TenantIsolation(t *testing.T) {
	db := newTenantTestDB(t)
	// 两个租户 + 各两名用户 + 一名独立用户。
	tA := &model.Tenant{Name: "tenant-a", Status: model.TenantStatusActive}
	tB := &model.Tenant{Name: "tenant-b", Status: model.TenantStatusActive}
	if err := CreateTenant(db, tA); err != nil {
		t.Fatalf("create A: %v", err)
	}
	if err := CreateTenant(db, tB); err != nil {
		t.Fatalf("create B: %v", err)
	}
	seedTenantUser(t, db, 1, &tA.ID)
	seedTenantUser(t, db, 2, &tA.ID)
	seedTenantUser(t, db, 3, &tB.ID)
	seedTenantUser(t, db, 4, &tB.ID)
	seedTenantUser(t, db, 5, nil) // 独立用户

	// 租户 A 预算上限 50；租户 B 上限 500；不给用户级/会话级策略（聚焦租户聚合）。
	if err := UpsertBudgetPolicy(db, &model.BudgetPolicy{
		Scope: model.BudgetScopeTenant, ScopeKey: itoa(int(tA.ID)), MaxTokens: 50, Window: model.BudgetWindowDaily,
	}); err != nil {
		t.Fatalf("upsert tenant A policy: %v", err)
	}
	if err := UpsertBudgetPolicy(db, &model.BudgetPolicy{
		Scope: model.BudgetScopeTenant, ScopeKey: itoa(int(tB.ID)), MaxTokens: 500, Window: model.BudgetWindowDaily,
	}); err != nil {
		t.Fatalf("upsert tenant B policy: %v", err)
	}

	// 租户 A 两名用户共消耗 120（分别 70+50），远超 A 的 50 上限；
	// 租户 B 两名用户共消耗 200（100+100），未超 500。
	seedTenantUsage(t, db, 1, "ws", 70)
	seedTenantUsage(t, db, 2, "ws", 50)
	seedTenantUsage(t, db, 3, "ws", 100)
	seedTenantUsage(t, db, 4, "ws", 100)

	// A 内用户 1/2 都被拦（共享租户预算）。
	for _, uid := range []uint{1, 2} {
		ev, err := EvaluateBudgets(db, uid, "sk-"+itoa(int(uid)), "", "")
		if err != nil {
			t.Fatalf("evaluate user %d: %v", uid, err)
		}
		if !ev.Blocked || ev.Scope != model.BudgetScopeTenant {
			t.Fatalf("租户 A 用户 %d 应被租户级拦截: %+v", uid, ev)
		}
		if ev.Used != 120 || ev.Max != 50 {
			t.Fatalf("租户 A 聚合应为 120/50, got %d/%d", ev.Used, ev.Max)
		}
	}
	// B 内用户 3/4 不受影响（B 未超限）。
	for _, uid := range []uint{3, 4} {
		if ev, _ := EvaluateBudgets(db, uid, "sk-"+itoa(int(uid)), "", ""); ev.Blocked {
			t.Fatalf("租户 B 用户 %d 不应被拦: %+v", uid, ev)
		}
	}
	// 独立用户不受影响（无租户归属，跳过 tenant 聚合）。
	if ev, _ := EvaluateBudgets(db, 5, "sk-5", "", ""); ev.Blocked {
		t.Fatalf("独立用户不应被拦: %+v", ev)
	}

	// 租户 B 也超限后（提额到 50 模拟 B 耗尽），B 被拦但 A 已超限依旧——
	// 且各自聚合互不包含对方用户（隔离成立）。
	if err := UpsertBudgetPolicy(db, &model.BudgetPolicy{
		Scope: model.BudgetScopeTenant, ScopeKey: itoa(int(tB.ID)), MaxTokens: 50, Window: model.BudgetWindowDaily,
	}); err != nil {
		t.Fatalf("lower tenant B policy: %v", err)
	}
	evB, _ := EvaluateBudgets(db, 3, "sk-3", "", "")
	if !evB.Blocked || evB.Used != 200 {
		t.Fatalf("B 降额后应拦 B 用户且聚合=200: %+v", evB)
	}
	// A 的聚合仍是 120（不含 B 的 200）——隔离的核心证明。
	evA, _ := EvaluateBudgets(db, 1, "sk-1", "", "")
	if evA.Used != 120 {
		t.Fatalf("A 聚合不应包含 B 用户用量, got %d", evA.Used)
	}
}
