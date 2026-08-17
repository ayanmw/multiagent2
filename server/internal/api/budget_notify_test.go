package api

import (
	"context"
	"sync"
	"testing"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"
)

// mockNotifier 是 notify.Notifier 的测试桩，记录发出的通知条数（M6-05 预算通知单测用）。
type mockNotifier struct {
	mu   sync.Mutex
	recs []*model.Notification
}

func (m *mockNotifier) Notify(_ context.Context, n *model.Notification) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recs = append(m.recs, n)
	return nil
}

func (m *mockNotifier) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.recs)
}

// TestGateway_MaybeNotifyBudget_Blocked 验证预算耗尽时发出一条 budget 类型站内信。
func TestGateway_MaybeNotifyBudget_Blocked(t *testing.T) {
	mn := &mockNotifier{}
	g := NewGateway(GatewayConfig{Notifier: mn})
	g.maybeNotifyBudget(1, repo.BudgetEvaluation{Blocked: true, Scope: model.BudgetScopeUser, Used: 100, Max: 100})
	if mn.count() != 1 {
		t.Fatalf("期望 1 条预算通知，实际 %d", mn.count())
	}
	if mn.recs[0].Type != model.NotificationTypeBudget {
		t.Fatalf("通知类型应为 budget，实际 %q", mn.recs[0].Type)
	}
}

// TestGateway_MaybeNotifyBudget_NilNotifier 验证未配置 notifier 时不发通知（静默跳过）。
func TestGateway_MaybeNotifyBudget_NilNotifier(t *testing.T) {
	mn := &mockNotifier{}
	g := NewGateway(GatewayConfig{Notifier: nil})
	g.maybeNotifyBudget(1, repo.BudgetEvaluation{Blocked: true})
	if mn.count() != 0 {
		t.Fatalf("nil notifier 不应发通知，实际 %d", mn.count())
	}
}

// TestGateway_MaybeNotifyBudget_NotBlocked 验证未触发拦截（Blocked=false）时不发通知。
func TestGateway_MaybeNotifyBudget_NotBlocked(t *testing.T) {
	mn := &mockNotifier{}
	g := NewGateway(GatewayConfig{Notifier: mn})
	g.maybeNotifyBudget(1, repo.BudgetEvaluation{Blocked: false})
	if mn.count() != 0 {
		t.Fatalf("未拦截不应发通知，实际 %d", mn.count())
	}
}

// TestGateway_MaybeNotifyBudget_Cooldown 验证同用户冷却期内不重复发通知（防无人值守 Loop 风暴）。
func TestGateway_MaybeNotifyBudget_Cooldown(t *testing.T) {
	mn := &mockNotifier{}
	g := NewGateway(GatewayConfig{Notifier: mn})
	ev := repo.BudgetEvaluation{Blocked: true, Scope: model.BudgetScopeUser, Used: 100, Max: 100}
	g.maybeNotifyBudget(1, ev) // 第 1 次：发出
	g.maybeNotifyBudget(1, ev) // 冷却期内（同一时刻）：应被抑制
	g.maybeNotifyBudget(1, ev) // 仍冷却期内：应被抑制
	if mn.count() != 1 {
		t.Fatalf("冷却期内同用户只应发 1 条，实际 %d", mn.count())
	}
	// 不同用户不受影响，各自收到一条。
	g.maybeNotifyBudget(2, ev)
	g.maybeNotifyBudget(3, ev)
	if mn.count() != 3 {
		t.Fatalf("不同用户应各自收到一条，合计 3 条，实际 %d", mn.count())
	}
}
