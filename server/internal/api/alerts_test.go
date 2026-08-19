package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/notify"
	"github.com/gin-gonic/gin"
)

// capturingNotifier 用 context.Context 签名实现 notify.Notifier，记录所有经统一通知
// 出口发出的通知，便于断言告警被正确投递（复用 M4-07 链路验证）。
type capturingNotifier struct {
	mu    sync.Mutex
	notes []*model.Notification
}

func (c *capturingNotifier) Notify(_ context.Context, n *model.Notification) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.notes = append(c.notes, n)
	return nil
}

// ensure capturingNotifier satisfies notify.Notifier.
var _ notify.Notifier = (*capturingNotifier)(nil)

func TestAlertsWebhookHandler_Firing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cap := &capturingNotifier{}
	h := NewAlertsWebhookHandler(cap).WithTargetUsers([]uint{1, 2})

	payload := alertmanagerPayload{
		Version: "4",
		Status:  "firing",
		Alerts: []alertItem{
			{Status: "firing", Labels: map[string]string{"alertname": "CodeAgentHighLLMErrorRate", "severity": "warning"}, Annotations: map[string]string{"description": "LLM 错误率 25%"}},
			{Status: "resolved", Labels: map[string]string{"alertname": "CodeAgentHighToolFailureRate"}}, // 不应投递
		},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/alerts", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	h.Handle(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200, body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Received int `json:"received"`
		Firing   int `json:"firing"`
		Notified int `json:"notified"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode resp: %v", err)
	}
	if resp.Received != 2 || resp.Firing != 1 {
		t.Errorf("received=%d firing=%d want 2/1", resp.Received, resp.Firing)
	}
	// 1 条 firing × 2 个目标用户 = 2 条通知。
	if resp.Notified != 2 {
		t.Errorf("notified=%d want 2", resp.Notified)
	}
	cap.mu.Lock()
	defer cap.mu.Unlock()
	if len(cap.notes) != 2 {
		t.Fatalf("captured notifications=%d want 2", len(cap.notes))
	}
	for _, n := range cap.notes {
		if n.Type != model.NotificationTypeAlert {
			t.Errorf("notification type=%q want %q", n.Type, model.NotificationTypeAlert)
		}
		if n.UserID != 1 && n.UserID != 2 {
			t.Errorf("notification user=%d want 1 or 2", n.UserID)
		}
		if n.Title != "平台告警：CodeAgentHighLLMErrorRate" {
			t.Errorf("notification title=%q", n.Title)
		}
		if n.RefID != "CodeAgentHighLLMErrorRate" {
			t.Errorf("notification ref_id=%q", n.RefID)
		}
	}
}

func TestAlertsWebhookHandler_TokenAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cap := &capturingNotifier{}
	h := NewAlertsWebhookHandler(cap).WithToken("secret123").WithTargetUsers([]uint{1})

	sample := alertmanagerPayload{Alerts: []alertItem{{Status: "firing", Labels: map[string]string{"alertname": "X"}}}}
	body, _ := json.Marshal(sample)

	// 无令牌 → 401
	req := httptest.NewRequest(http.MethodPost, "/api/alerts", bytes.NewReader(body))
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	h.Handle(c)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("no-token status=%d want 401", w.Code)
	}

	// 错误令牌 → 401
	req2 := httptest.NewRequest(http.MethodPost, "/api/alerts", bytes.NewReader(body))
	req2.Header.Set("Authorization", "Bearer wrong")
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request = req2
	h.Handle(c2)
	if w2.Code != http.StatusUnauthorized {
		t.Errorf("wrong-token status=%d want 401", w2.Code)
	}

	// 正确令牌（Bearer）→ 200
	req3 := httptest.NewRequest(http.MethodPost, "/api/alerts", bytes.NewReader(body))
	req3.Header.Set("Authorization", "Bearer secret123")
	w3 := httptest.NewRecorder()
	c3, _ := gin.CreateTestContext(w3)
	c3.Request = req3
	h.Handle(c3)
	if w3.Code != http.StatusOK {
		t.Errorf("correct-token status=%d want 200", w3.Code)
	}

	// 正确令牌（X-Alert-Token）→ 200
	req4 := httptest.NewRequest(http.MethodPost, "/api/alerts", bytes.NewReader(body))
	req4.Header.Set("X-Alert-Token", "secret123")
	w4 := httptest.NewRecorder()
	c4, _ := gin.CreateTestContext(w4)
	c4.Request = req4
	h.Handle(c4)
	if w4.Code != http.StatusOK {
		t.Errorf("x-alert-token status=%d want 200", w4.Code)
	}
}

func TestAlertsWebhookHandler_NoTargetNoNotify(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cap := &capturingNotifier{}
	// 无目标用户：不应投递，但仍返回 200（仅记录日志）。
	h := NewAlertsWebhookHandler(cap).WithTargetUsers(nil)
	sample := alertmanagerPayload{Alerts: []alertItem{{Status: "firing", Labels: map[string]string{"alertname": "Y"}}}}
	body, _ := json.Marshal(sample)
	req := httptest.NewRequest(http.MethodPost, "/api/alerts", bytes.NewReader(body))
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	h.Handle(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", w.Code)
	}
	cap.mu.Lock()
	defer cap.mu.Unlock()
	if len(cap.notes) != 0 {
		t.Errorf("expected 0 notifications when no targets, got %d", len(cap.notes))
	}
}
