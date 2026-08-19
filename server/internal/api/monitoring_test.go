package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/metrics"
	"github.com/ayanmw/multiagent2/server/internal/middleware"
	"github.com/gin-gonic/gin"
)

// callMonitoringOverview 以指定身份请求运行监控概览接口（M3-09）。
// authed=false 时不注入身份，用于断言未认证 401。
func callMonitoringOverview(t *testing.T, authed bool, uid uint, role string) (int, metrics.Overview) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	if authed {
		c.Set(middleware.CtxUserID, uid)
		c.Set(middleware.CtxUserRole, role)
	}
	c.Request, _ = http.NewRequest(http.MethodGet, "/api/monitoring/overview", nil)

	MonitoringOverviewHandler(nil)(c)

	var out metrics.Overview
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode monitoring overview: %v (body=%s)", err, w.Body.String())
		}
	}
	return w.Code, out
}

// TestMonitoringOverview_RequiresAuth 未认证请求应 401，不泄漏任何指标。
func TestMonitoringOverview_RequiresAuth(t *testing.T) {
	code, _ := callMonitoringOverview(t, false, 0, "")
	if code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401", code)
	}
}

// TestMonitoringOverview_ReturnsSnapshot 启用 metrics 后，接口应返回递增后的进程内快照。
func TestMonitoringOverview_ReturnsSnapshot(t *testing.T) {
	if err := metrics.Init(metrics.Config{Enabled: true}); err != nil {
		t.Fatalf("metrics.Init: %v", err)
	}
	t.Cleanup(func() { _ = metrics.Init(metrics.Config{Enabled: false}) })

	before := metrics.Summary()
	ctx := t.Context()
	metrics.RecordLLMCall(ctx, "gateway", "hy3", 30*time.Millisecond, nil)
	metrics.RecordToolCall(ctx, "allowed", nil)
	metrics.RecordTokenUsage(ctx, 12, 8, 20)

	code, got := callMonitoringOverview(t, true, 7, "developer")
	if code != http.StatusOK {
		t.Fatalf("status=%d want 200", code)
	}
	if !got.Enabled {
		t.Errorf("expected enabled=true, got %+v", got)
	}
	if got.LLMCalls != before.LLMCalls+1 {
		t.Errorf("llm_calls=%d want %d", got.LLMCalls, before.LLMCalls+1)
	}
	if got.ToolCalls != before.ToolCalls+1 {
		t.Errorf("tool_calls=%d want %d", got.ToolCalls, before.ToolCalls+1)
	}
	if got.TokenTotal != before.TokenTotal+20 {
		t.Errorf("token_total=%d want %d", got.TokenTotal, before.TokenTotal+20)
	}
}

// TestMonitoringOverview_DisabledStillServes 关闭指标时接口仍可访问，
// 但 enabled=false 且计数不再变化（供前端提示「指标未启用」）。
func TestMonitoringOverview_DisabledStillServes(t *testing.T) {
	if err := metrics.Init(metrics.Config{Enabled: false}); err != nil {
		t.Fatalf("metrics.Init(false): %v", err)
	}
	before := metrics.Summary()
	metrics.RecordLLMCall(t.Context(), "gateway", "hy3", time.Second, nil)

	code, got := callMonitoringOverview(t, true, 7, "viewer")
	if code != http.StatusOK {
		t.Fatalf("status=%d want 200", code)
	}
	if got.Enabled {
		t.Errorf("expected enabled=false when metrics off: %+v", got)
	}
	if got.LLMCalls != before.LLMCalls {
		t.Errorf("disabled Record* must not change counters: got=%d want=%d", got.LLMCalls, before.LLMCalls)
	}
}
