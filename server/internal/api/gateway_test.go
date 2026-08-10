package api

import (
	"sync"
	"testing"
	"time"
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
