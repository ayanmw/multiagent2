package api

import (
	"log"
	"net/http"
	"strings"

	"github.com/ayanmw/multiagent2/server/internal/notify"
	"github.com/gin-gonic/gin"
)

// AlertsWebhookHandler 是 M7-04「告警规则（Prometheus Alertmanager）」的接收端点：
// 接收 Alertmanager 推送的标准 webhook 负载（POST /api/alerts），把每条 firing 告警
// 经统一通知出口（notify.Notifier，即 M4-07 的站内信 + 可选出站回调）写入通知中心，
// 使 Prometheus 告警直达用户站内信 / 外部渠道 —— 复用既有通知链路，无需重复实现落库。
//
// 安全：本端点不挂 JWT 鉴权中间件（由 Alertmanager 调用），改以可选共享密钥
// （ALERT_WEBHOOK_TOKEN）校验：配置非空时要求请求头 `Authorization: Bearer <token>`
// 或 `X-Alert-Token: <token>` 匹配；空则关闭校验（向后兼容，生产建议配置）。
//
// 接收人：告警是「平台级」事件，默认投递到 ALERT_NOTIFY_USER_IDS 配置的管理员用户列表
// （可多个，逗号分隔），由 main.go 注入 targetUserIDs；列表为空时仅记录日志、不投递。
type AlertsWebhookHandler struct {
	notifier       notify.Notifier
	token          string
	targetUserIDs  []uint
	logger         *log.Logger
}

// NewAlertsWebhookHandler 构造告警接收端点。notifier 为统一通知出口（可空：nil 时仅记录日志）。
func NewAlertsWebhookHandler(notifier notify.Notifier) *AlertsWebhookHandler {
	return &AlertsWebhookHandler{notifier: notifier, logger: log.Default()}
}

// WithToken 注入共享密钥（ALERT_WEBHOOK_TOKEN）；空字符串表示关闭校验。
func (h *AlertsWebhookHandler) WithToken(secret string) *AlertsWebhookHandler {
	h.token = secret
	return h
}

// WithTargetUsers 注入告警通知的目标用户 ID 列表（管理员）；空表示不投递（仅记录）。
func (h *AlertsWebhookHandler) WithTargetUsers(ids []uint) *AlertsWebhookHandler {
	h.targetUserIDs = ids
	return h
}

// alertmanagerPayload 是 Alertmanager webhook 推送负载的兼容子集（标准字段）。
type alertmanagerPayload struct {
	Version string `json:"version"`
	Status  string `json:"status"` // firing / resolved（整个 group 的状态）
	// CommonLabels/CommonAnnotations 是 group 内所有告警共享的标签/注解，可补到每条告警。
	CommonLabels      map[string]string `json:"commonLabels"`
	CommonAnnotations map[string]string `json:"commonAnnotations"`
	Alerts            []alertItem       `json:"alerts"`
}

// alertItem 是单条告警。
type alertItem struct {
	Status       string            `json:"status"` // firing / resolved
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     string            `json:"startsAt"`
	EndsAt       string            `json:"endsAt"`
	Fingerprint  string            `json:"fingerprint"`
}

// Handle 是 Gin handler：校验密钥 → 解析负载 → 把 firing 告警转为通知投递。
func (h *AlertsWebhookHandler) Handle(c *gin.Context) {
	if h.token != "" {
		if !h.validToken(c) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid alert token"})
			return
		}
	}

	var payload alertmanagerPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid alertmanager payload: " + err.Error()})
		return
	}

	firing := 0
	notified := 0
	for i := range payload.Alerts {
		item := payload.Alerts[i]
		if item.Status != "firing" {
			continue
		}
		firing++
		alertName := item.Labels["alertname"]
		if alertName == "" {
			alertName = "unknown"
		}
		detail := buildAlertDetail(item, payload.CommonAnnotations)
		for _, uid := range h.targetUserIDs {
			notified++
			if h.notifier != nil {
				_ = h.notifier.Notify(c.Request.Context(), notify.NewAlert(uid, alertName, detail))
			}
		}
		if h.notifier == nil || len(h.targetUserIDs) == 0 {
			h.logger.Printf("[ALERTS] 收到 firing 告警 alertname=%s（未投递：notifier=%v, targets=%d）: %s",
				alertName, h.notifier == nil, len(h.targetUserIDs), detail)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"status":   "ok",
		"received": len(payload.Alerts),
		"firing":   firing,
		"notified": notified,
	})
}

// validToken 校验请求是否携带正确的共享密钥（Authorization: Bearer 或 X-Alert-Token）。
func (h *AlertsWebhookHandler) validToken(c *gin.Context) bool {
	auth := c.GetHeader("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer ")) == h.token
	}
	return c.GetHeader("X-Alert-Token") == h.token
}

// buildAlertDetail 拼接告警正文：优先 annotations.description，其次 summary，
// 再退化为标签罗列；并附上 severity（若有），与通知中心展示对齐。
func buildAlertDetail(item alertItem, commonAnnotations map[string]string) string {
	detail := item.Annotations["description"]
	if detail == "" {
		detail = item.Annotations["summary"]
	}
	if detail == "" {
		detail = commonAnnotations["description"]
	}
	if detail == "" {
		detail = commonAnnotations["summary"]
	}
	if detail == "" {
		// 退化：罗列标签，保证通知有意义。
		var parts []string
		for k, v := range item.Labels {
			parts = append(parts, k+"="+v)
		}
		detail = "告警触发：" + strings.Join(parts, ", ")
	}
	if sev := item.Labels["severity"]; sev != "" {
		detail = "[" + sev + "] " + detail
	}
	return detail
}
