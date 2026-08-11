package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/executor"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/notify"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// automationLoopRunner 是 webhook 触发 Loop 所需的运行器接口（与 scheduler.AutomationRunner
// 同签名）。抽成局部接口使 webhook 处理与具体 runner 实现解耦，便于单测注入 mock。
type automationLoopRunner interface {
	Run(ctx context.Context, a *model.Automation, sessionKey string) error
}

// WebhookRateLimiter 是「按 token 维度」的滑动窗口速率限制器（M4-03 webhook 防刷）。
// 单进程内存实现（与调度器同生命周期）：每个 webhook token 在窗口内最多允许 limit 次触发，
// 超出返回 false（handler 转 429）。now 可注入，便于单测。
type WebhookRateLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	now    func() time.Time
	hits   map[string][]time.Time // token -> 窗口内命中时间戳
}

// NewWebhookRateLimiter 构造速率限制器。limit<=0 或 window<=0 时由调用方（config）兜底，
// 此处仅接收已校验的合法值。
func NewWebhookRateLimiter(limit int, window time.Duration) *WebhookRateLimiter {
	return &WebhookRateLimiter{
		limit:  limit,
		window: window,
		now:    time.Now,
		hits:   make(map[string][]time.Time),
	}
}

// Allow 判断 token 本次是否放行：清理窗口外旧命中后，未达上限则记录并返回 true，否则 false。
func (l *WebhookRateLimiter) Allow(token string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	cutoff := now.Add(-l.window)
	valid := l.hits[token][:0]
	for _, t := range l.hits[token] {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	if len(valid) >= l.limit {
		l.hits[token] = valid
		return false
	}
	valid = append(valid, now)
	l.hits[token] = valid
	return true
}

// WebhookHandler 处理外部事件触发的自动化（M4-03）。
// 路由 POST /api/webhooks/:token 不挂鉴权中间件，完全靠 URL 中的 32B 令牌匹配 Automation；
// 命中后异步启动 Goal Loop（与 cron 调度器共用同一套 Loop 运行器），并写审计 + 更新 LastRun。
type WebhookHandler struct {
	db       *gorm.DB
	runner   automationLoopRunner
	limiter  *WebhookRateLimiter
	notifier notify.Notifier // 运行结果通知出口（M4-07，可空）
	running  sync.Map        // automation id -> true，防止同一自动化并发重入
}

// NewWebhookHandler 构造 webhook 处理链。runner 为生产用 AutomationLoopRunner（复用 cron 同款），
// limiter 为按 token 的速率限制器，notifier 为可选通知出口（nil 时不发通知）。
func NewWebhookHandler(db *gorm.DB, runner automationLoopRunner, limiter *WebhookRateLimiter) *WebhookHandler {
	return &WebhookHandler{db: db, runner: runner, limiter: limiter}
}

// WithNotifier 注入运行结果通知出口（M4-07），返回自身便于链式构造。
// 生产 main.go 调用；单测保持不注入（nil 安全）。
func (h *WebhookHandler) WithNotifier(n notify.Notifier) *WebhookHandler {
	h.notifier = n
	return h
}

// Handle 是 Gin handler：先速率限制 → 令牌匹配 automation → 建会话 → 202 立即返回 → 异步跑 Loop。
func (h *WebhookHandler) Handle(c *gin.Context) {
	token := c.Param("token")

	// 1) 速率限制（按 token 维度，防外部刷接口）。
	if !h.limiter.Allow(token) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "webhook rate limit exceeded"})
		return
	}

	// 2) 令牌匹配启用的 webhook 自动化。
	a, err := repo.GetAutomationByWebhookToken(h.db, token)
	if err != nil || a.TriggerType != model.AutomationTriggerWebhook {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or disabled webhook token"})
		return
	}

	// 3) 防重入：同一自动化已有 webhook 在跑则冲突返回（与调度器 running 锁思路一致）。
	if _, loaded := h.running.LoadOrStore(a.ID, true); loaded {
		c.JSON(http.StatusConflict, gin.H{"error": "automation already running"})
		return
	}

	// 4) 预先建会话（可观测，验证要求「触发 Loop」应创建会话）。
	sessionKey := repo.NewSessionKey()
	if _, serr := repo.GetOrCreateSession(h.db, a.UserID, sessionKey); serr != nil {
		h.running.Delete(a.ID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create session"})
		return
	}

	// 捕获请求上下文信息（异步 goroutine 内 c 不可再用）。
	clientIP := c.ClientIP()
	eventSize := 0
	if c.Request.Body != nil {
		if body, rerr := io.ReadAll(c.Request.Body); rerr == nil {
			eventSize = len(body)
		}
	}

	// 5) 立即返回 202 Accepted（外部系统不应长等 LLM 跑完）。
	c.JSON(http.StatusAccepted, gin.H{
		"status":        "accepted",
		"automation_id": a.ID,
		"session_key":   sessionKey,
	})

	// 6) 异步跑 Loop。
	go h.runLoop(a, sessionKey, clientIP, eventSize)
}

// runLoop 在独立 goroutine 中执行一次 webhook 触发的自动化：跑 Goal Loop → 更新 LastRun → 写审计。
func (h *WebhookHandler) runLoop(a *model.Automation, sessionKey, clientIP string, eventSize int) {
	defer h.running.Delete(a.ID)
	now := time.Now()

	// M4-05：记录本次自动化运行（status=running），供进程重启后「跨天恢复」扫描续跑。
	// 建表失败（如测试库未迁移）时仅告警，不阻断本次运行（恢复能力降级）。
	run := &model.AutomationRun{
		AutomationID: a.ID,
		UserID:       a.UserID,
		SessionKey:   sessionKey,
		Channel:      string(ChannelWebhook),
		Status:       model.RunStatusRunning,
	}
	if cerr := repo.CreateAutomationRun(h.db, run); cerr != nil {
		fmt.Printf("[WEBHOOK] automation %d 创建运行记录失败(恢复不可追踪): %v\n", a.ID, cerr)
	}

	err := h.runner.Run(context.Background(), a, sessionKey)

	a.LastRun = &now
	if uerr := repo.UpdateAutomation(h.db, a); uerr != nil {
		// 更新时间戳失败不致命，仅记录日志级（沙箱外可观测性由审计补充）。
		fmt.Printf("[WEBHOOK] automation %d 更新 last_run 失败: %v\n", a.ID, uerr)
	}

	auditor := repo.NewDBAuditor(h.db, a.UserID)
	if err != nil {
		// M4-05：运行失败 → 标记 failed。
		if run != nil && run.ID != 0 {
			if merr := repo.MarkAutomationRunStatus(h.db, run.ID, model.RunStatusFailed, err.Error(), run.Attempts); merr != nil {
				fmt.Printf("[WEBHOOK] automation %d 标记运行 failed 失败: %v\n", a.ID, merr)
			}
		}
		auditor.Record(executor.AuditEntry{
			Timestamp: now,
			Command:   "webhook:" + a.Name,
			Workdir:   "",
			Decision:  executor.DecisionDeny,
			Reason:    fmt.Sprintf("webhook 触发运行失败: %v (client=%s, event_size=%d)", err, clientIP, eventSize),
			Allowed:   false,
			Note:      "webhook",
		})
		return
	}
	// M4-05：Loop 收敛结束 → 标记运行 done。
	if run != nil && run.ID != 0 {
		if merr := repo.MarkAutomationRunStatus(h.db, run.ID, model.RunStatusDone, "", run.Attempts); merr != nil {
			fmt.Printf("[WEBHOOK] automation %d 标记运行 done 失败: %v\n", a.ID, merr)
		}
	}
	auditor.Record(executor.AuditEntry{
		Timestamp: now,
		Command:   "webhook:" + a.Name,
		Workdir:   "",
		Decision:  executor.DecisionAllow,
		Reason:    fmt.Sprintf("webhook 触发 Loop 成功 (client=%s, session=%s, event_size=%d)", clientIP, sessionKey, eventSize),
		Allowed:   true,
		Note:      "webhook",
	})
	// M4-07：成功完成 → 站内信通知（best-effort，不阻断主流程）。
	if h.notifier != nil {
		_ = h.notifier.Notify(context.Background(), notify.NewSuccess(a.UserID, a.ID, a.Name, sessionKey, "自动化已执行完成"))
	}
}
