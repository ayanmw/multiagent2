package api

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/executor"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"
)

// TestGateway_SessionLockSerializes 验证 Gateway 的「每会话串行锁」：
// 同一 sessionKey 的并发临界区互斥执行，绝不重叠（M4-04 核心承诺）。
func TestGateway_SessionLockSerializes(t *testing.T) {
	g := NewGateway(GatewayConfig{})
	const key = "sess-lock-test"

	var mu sync.Mutex
	maxConcurrent := 0
	current := 0

	var wg sync.WaitGroup
	const n = 12
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g.lockSession(key)
			mu.Lock()
			current++
			if current > maxConcurrent {
				maxConcurrent = current
			}
			mu.Unlock()

			// 持锁期间模拟一次 LLM 调用耗时，验证不会并发进入。
			time.Sleep(2 * time.Millisecond)

			mu.Lock()
			current--
			mu.Unlock()
			g.unlockSession(key)
		}()
	}
	wg.Wait()

	if maxConcurrent != 1 {
		t.Fatalf("同一会话应严格串行（maxConcurrent=1），实际 got %d", maxConcurrent)
	}
}

// TestGateway_DifferentSessionsParallel 验证不同会话的串行锁互不阻塞（可并行）。
func TestGateway_DifferentSessionsParallel(t *testing.T) {
	g := NewGateway(GatewayConfig{})

	var mu sync.Mutex
	maxConcurrent := 0
	current := 0

	var wg sync.WaitGroup
	const n = 8
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			g.lockSession("sess-parallel-" + string(rune('a'+idx)))
			mu.Lock()
			current++
			if current > maxConcurrent {
				maxConcurrent = current
			}
			mu.Unlock()
			time.Sleep(time.Millisecond)
			mu.Lock()
			current--
			mu.Unlock()
			g.unlockSession("sess-parallel-" + string(rune('a'+idx)))
		}(i)
	}
	wg.Wait()

	if maxConcurrent != n {
		t.Fatalf("不同会话应完全并行（maxConcurrent=%d），实际 got %d", n, maxConcurrent)
	}
}

// TestGateway_AllocateSessionKey 验证稳定 session_id 分配：空则生成、非空则沿用。
func TestGateway_AllocateSessionKey(t *testing.T) {
	g := NewGateway(GatewayConfig{})
	if got := g.allocateSessionKey(""); got == "" {
		t.Fatal("空 session 应生成非空 key")
	}
	if got := g.allocateSessionKey("sess-existing"); got != "sess-existing" {
		t.Fatalf("非空 session 应沿用，实际 got %q", got)
	}
}

// TestChannelKinds 验证 Channel 抽象的内置来源标识（Web/CLI/Webhook/Cron/IM）。
func TestChannelKinds(t *testing.T) {
	cases := map[Channel]string{
		ChannelWeb:     "web",
		ChannelCLI:     "cli",
		ChannelWebhook: "webhook",
		ChannelCron:    "cron",
		ChannelIM:      "im",
	}
	for ch, want := range cases {
		if got := ch.Kind(); got != want {
			t.Fatalf("Channel.Kind()=%q, want %q", got, want)
		}
	}
}

// TestGateway_ResolveExecutorMode_AutonomousForcesUnattended 验证 M4-06 的核心安全约束：
// 即使 RUN_MODE=attended，自主化 Channel（cron/webhook/recover）也必须强制无人值守，
// 因为无人实时值守时若 ask 危险命令走交互确认会直接卡死整个 24h 自主 Loop。
func TestGateway_ResolveExecutorMode_AutonomousForcesUnattended(t *testing.T) {
	// 配置为有人值守（调试会话语义），但自主化入口必须强制无人值守。
	g := NewGateway(GatewayConfig{ExecutorMode: executor.ModeInteractive})

	forced := []Channel{ChannelCron, ChannelWebhook, ChannelRecover}
	for _, ch := range forced {
		if got := g.resolveExecutorMode(ch); got != executor.ModeUnattended {
			t.Fatalf("自主化 Channel %q 必须强制无人值守，实际 %v", ch.Kind(), got)
		}
	}
	// 有人值守 Channel（web）跟随配置，不被强制覆盖。
	if got := g.resolveExecutorMode(ChannelWeb); got != executor.ModeInteractive {
		t.Fatalf("ChannelWeb 应跟随 RUN_MODE=attended，实际 %v", got)
	}
	if got := g.resolveExecutorMode(ChannelCLI); got != executor.ModeInteractive {
		t.Fatalf("ChannelCLI 应跟随 RUN_MODE=attended，实际 %v", got)
	}
	// nil Channel 回落配置。
	if got := g.resolveExecutorMode(nil); got != executor.ModeInteractive {
		t.Fatalf("nil Channel 应跟随配置 attended，实际 %v", got)
	}
}

// TestGateway_ResolveExecutorMode_DefaultUnattended 验证默认（RUN_MODE=unattended）
// 下所有 Channel 均为无人值守安全默认。
func TestGateway_ResolveExecutorMode_DefaultUnattended(t *testing.T) {
	g := NewGateway(GatewayConfig{ExecutorMode: executor.ModeUnattended})
	all := []Channel{ChannelWeb, ChannelCLI, ChannelCron, ChannelWebhook, ChannelRecover, ChannelIM}
	for _, ch := range all {
		if got := g.resolveExecutorMode(ch); got != executor.ModeUnattended {
			t.Fatalf("默认模式下 Channel %q 应为无人值守，实际 %v", ch.Kind(), got)
		}
	}
}

// TestBudgetExhaustedError 验证 M4-06 预算拦截错误可经 errors.As 识别（供 ChatHandler /
// StreamChatHandler 转换为 429 / SSE RUN_ERROR），且 Unwrap 暴露哨兵 ErrBudgetExhausted。
func TestBudgetExhaustedError(t *testing.T) {
	ev := repo.BudgetEvaluation{
		Blocked: true,
		Scope:   model.BudgetScopeUser,
		Used:    100,
		Max:     50,
	}
	err := &BudgetExhaustedError{Eval: ev}

	// 可通过 errors.As 取到具体类型，提取 scope/used/max 供响应体使用。
	var be *BudgetExhaustedError
	if !errors.As(err, &be) {
		t.Fatal("errors.As 应识别 *BudgetExhaustedError")
	}
	if be.Eval.Scope != ev.Scope || be.Eval.Used != ev.Used || be.Eval.Max != ev.Max {
		t.Fatal("BudgetExhaustedError 应携带原始 BudgetEvaluation 明细")
	}

	// Unwrap 暴露哨兵，errors.Is 可判定。
	if !errors.Is(err, ErrBudgetExhausted) {
		t.Fatal("errors.Is 应识别哨兵 ErrBudgetExhausted")
	}
	if err.Error() != "预算耗尽，待恢复" {
		t.Fatalf("错误信息不符，实际 %q", err.Error())
	}
}
