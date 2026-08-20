package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/executor"
	"github.com/ayanmw/multiagent2/server/internal/im"
	"github.com/ayanmw/multiagent2/server/internal/metrics"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// IM Channel（M8-07）：飞书/钉钉/企微 webhook 入口 + IM 用户绑定管理。
//
// 入口语义：
//   POST /api/im/:platform/webhook   —— 不挂鉴权中间件，靠 IM 平台签名（secret 配置
//                                       后启用）保护；解析消息 → 匹配绑定 → 以绑定
//                                       用户身份经 Gateway.Run 跑 Loop → 结果回发 IM。
//   GET/POST/DELETE /api/im/bindings —— 鉴权 + RBAC（im:read / im:write），用户管理
//                                       自己的 IM 绑定（本人绑定本人）。
//
// 与 M4-04 Channel 抽象的关系：Gateway.Request.Channel = ChannelIM（已有预留常量），
// 复用统一会话串行锁 + 同一 Runner；session_key 稳定为 "im:<platform>:<chat_id>"，
// 使 IM 聊天天然形成多轮记忆（M0.5-01 历史回灌）。IM 为无人值守入口，
// resolveExecutorMode 将其归入 Unattended（ask 危险命令进检查点队列）。
// ---------------------------------------------------------------------------

// IMRunFunc 是「以某平台用户身份跑一次 Loop」的运行器签名（生产实现由 main.go
// 包装 Gateway.Run + TeamOverride；测试注入 mock）。返回回发文本。
type IMRunFunc func(ctx context.Context, uid uint, sessionKey, text string) (string, error)

// IMWebhookHandler 处理三种 IM 平台的入站 webhook。
type IMWebhookHandler struct {
	db      *gorm.DB
	limiter *WebhookRateLimiter
	runFunc IMRunFunc
	senders map[im.Platform]im.Sender // 各平台出站回发；未配置 URL 的平台为 nil（跳过回发）
	secrets map[im.Platform]string    // 各平台入站验签密钥；空=关闭验签
	running sync.Map                  // "platform:senderID" -> true，防同一 IM 用户并发重入
}

// IMWebhookOptions 是 IM webhook 的构造选项。
type IMWebhookOptions struct {
	DB      *gorm.DB
	Limiter *WebhookRateLimiter
	RunFunc IMRunFunc
	Senders map[im.Platform]im.Sender
	Secrets map[im.Platform]string
}

// NewIMWebhookHandler 构造 IM webhook 处理链。
// Senders/Secrets 为 nil 时按空 map 处理（未配置平台→验签跳过、回发跳过）。
func NewIMWebhookHandler(o IMWebhookOptions) *IMWebhookHandler {
	if o.Senders == nil {
		o.Senders = map[im.Platform]im.Sender{}
	}
	if o.Secrets == nil {
		o.Secrets = map[im.Platform]string{}
	}
	return &IMWebhookHandler{db: o.DB, limiter: o.Limiter, runFunc: o.RunFunc, senders: o.Senders, secrets: o.Secrets}
}

// Handle 是 Gin handler：平台校验 → 读 body → 验签 → 解析 → 限流 → 查绑定 →
// 202 立即返回 → goroutine 异步跑 Loop 并回发。
func (h *IMWebhookHandler) Handle(c *gin.Context) {
	p, ok := im.ParsePlatform(c.Param("platform"))
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported platform (must be feishu, dingtalk or wecom)"})
		return
	}

	var body []byte
	if c.Request.Body != nil {
		if b, rerr := io.ReadAll(c.Request.Body); rerr == nil {
			body = b
		}
	}
	if len(body) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "empty request body"})
		return
	}

	// 入站验签（secret 配置后启用；各平台签名参数位置不同）。
	if !h.verify(p, c, body) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid im signature"})
		return
	}

	// 各平台解析为统一 InboundMessage。
	msg, perr := parseIMEvent(p, body)
	if perr != nil {
		fmt.Printf("[IM] %s parse failed: %v\n", p, perr)
		c.JSON(http.StatusBadRequest, gin.H{"error": "parse event failed"})
		return
	}

	// 限流（按 platform:senderID 维度，防刷）。
	if h.limiter != nil && !h.limiter.Allow(string(p)+":"+msg.SenderID) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "im rate limit exceeded"})
		return
	}

	// 查绑定：IM 用户 → 平台用户。
	b, berr := repo.GetIMBindingByPlatformUser(h.db, string(p), msg.SenderID)
	if berr != nil {
		if errors.Is(berr, repo.ErrIMBindingNotFound) {
			// 未绑定：202 + 异步回发「绑定指引」提示（不阻塞、不报错给 IM 平台）。
			c.JSON(http.StatusAccepted, gin.H{"status": "accepted", "bound": false})
			go h.reply(c.Request.Context(), msg, fmt.Sprintf(
				"[goMultiAgent] 您的 %s 账号（%s）尚未绑定平台用户。\n请在平台「IM 绑定」页面（或 API）创建绑定：platform=%s, im_user_id=%s",
				p, msg.SenderID, p, msg.SenderID))
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query binding failed"})
		return
	}

	// 防重入：同一 IM 用户已有 Loop 在跑则 202 排队提示（避免消息风暴打爆预算）。
	reentryKey := string(p) + ":" + msg.SenderID
	if _, loaded := h.running.LoadOrStore(reentryKey, true); loaded {
		c.JSON(http.StatusAccepted, gin.H{"status": "accepted", "queued": true})
		go h.reply(c.Request.Context(), msg, "[goMultiAgent] 您有一条 Loop 正在执行，请稍候再发。")
		return
	}

	sessionKey := imSessionKey(p, msg.ChatID)
	c.JSON(http.StatusAccepted, gin.H{"status": "accepted", "session_key": sessionKey, "user_id": b.UserID})

	// 异步跑 Loop（请求上下文 c 不可复用，用独立 context.Background）。
	clientIP := c.ClientIP()
	go h.runLoop(context.Background(), b, msg, sessionKey, clientIP)
}

// verify 按平台分发验签。secret 为空（未配置）直接放行。
func (h *IMWebhookHandler) verify(p im.Platform, c *gin.Context, body []byte) bool {
	secret := h.secrets[p]
	if secret == "" {
		return true
	}
	switch p {
	case im.Feishu:
		return im.VerifyFeishu(secret, c.GetHeader("X-Lark-Request-Timestamp"), c.GetHeader("X-Lark-Signature"), body)
	case im.DingTalk:
		timestamp := firstNonEmpty(c.GetHeader("timestamp"), c.Query("timestamp"))
		sign := firstNonEmpty(c.GetHeader("sign"), c.Query("sign"))
		return im.VerifyDingTalk(secret, timestamp, sign, body)
	case im.WeCom:
		return im.VerifyWeCom(secret, c.Query("timestamp"), c.Query("nonce"), c.Query("msg_signature"), body)
	default:
		return false
	}
}

// parseIMEvent 按平台分发解析。
func parseIMEvent(p im.Platform, body []byte) (im.InboundMessage, error) {
	switch p {
	case im.Feishu:
		return im.ParseFeishuEvent(body)
	case im.DingTalk:
		return im.ParseDingTalkEvent(body)
	case im.WeCom:
		return im.ParseWeComEvent(body)
	default:
		return im.InboundMessage{}, fmt.Errorf("unsupported platform %q", p)
	}
}

// imSessionKey 生成 IM 聊天的稳定会话 key（跨消息保留多轮记忆）。
func imSessionKey(p im.Platform, chatID string) string {
	return "im:" + string(p) + ":" + chatID
}

// reply 尽力回发一条消息（无 sender/失败不阻断，仅日志）。
func (h *IMWebhookHandler) reply(ctx context.Context, msg im.InboundMessage, text string) {
	s := h.senders[msg.Platform]
	if s == nil {
		fmt.Printf("[IM] %s 无出站 webhook，跳过回发: %s\n", msg.Platform, text)
		return
	}
	if err := s.Send(ctx, msg, text); err != nil {
		fmt.Printf("[IM] %s 回发失败: %v\n", msg.Platform, err)
	}
}

// runLoop 异步执行：跑 Gateway Loop → 回发结果/错误 → 写审计。
func (h *IMWebhookHandler) runLoop(ctx context.Context, b *model.IMBinding, msg im.InboundMessage, sessionKey, clientIP string) {
	defer h.running.Delete(string(msg.Platform) + ":" + msg.SenderID)
	now := time.Now()

	// M7-04：记录一次自主 Loop 运行（IM 触发，供「Loop 失败率」告警）。
	metrics.RecordLoopRun(ctx)

	auditor := repo.NewDBAuditor(h.db, b.UserID)
	reply, err := h.runFunc(ctx, b.UserID, sessionKey, msg.Text)
	if err != nil {
		metrics.RecordLoopFailure(ctx)
		auditor.Record(executor.AuditEntry{
			Timestamp: now,
			Command:   "im:" + string(msg.Platform),
			Workdir:   "",
			Decision:  executor.DecisionDeny,
			Reason:    fmt.Sprintf("IM Loop 运行失败: %v (user=%s, session=%s, client=%s)", err, msg.SenderID, sessionKey, clientIP),
			Allowed:   false,
			Note:      "im",
		})
		h.reply(ctx, msg, "[goMultiAgent] Loop 执行失败: "+err.Error())
		return
	}
	auditor.Record(executor.AuditEntry{
		Timestamp: now,
		Command:   "im:" + string(msg.Platform),
		Workdir:   "",
		Decision:  executor.DecisionAllow,
		Reason:    fmt.Sprintf("IM 触发 Loop 成功 (user=%s, session=%s, reply_chars=%d)", msg.SenderID, sessionKey, len(reply)),
		Allowed:   true,
		Note:      "im",
	})
	// 结果回发 IM（完成通知回发：IM 即通知渠道）。
	h.reply(ctx, msg, reply)
}

// firstNonEmpty 取第一个非空字符串。
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// IM 绑定管理 API（鉴权 + RBAC im:read / im:write，owner 隔离）
// ---------------------------------------------------------------------------

// ListIMBindingsHandler 列出当前用户自己的 IM 绑定。
func ListIMBindingsHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		rows, err := repo.ListIMBindingsByUser(db, uid)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list im bindings"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"bindings": rows})
	}
}

// createIMBindingRequest 是创建绑定的请求体。
type createIMBindingRequest struct {
	Platform string `json:"platform" binding:"required"`
	IMUserID string `json:"im_user_id" binding:"required"`
	ChatID   string `json:"chat_id" binding:"required"`
	Username string `json:"username"`
}

// CreateIMBindingHandler 创建当前用户自己的 IM 绑定（仅本人绑定本人，防越权代绑）。
// 复合唯一 (platform, im_user_id) 冲突 → 409。
func CreateIMBindingHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		var req createIMBindingRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
			return
		}
		b := &model.IMBinding{
			UserID:   uid,
			Platform: req.Platform,
			IMUserID: strings.TrimSpace(req.IMUserID),
			ChatID:   strings.TrimSpace(req.ChatID),
			Username: req.Username,
		}
		if err := repo.CreateIMBinding(db, b); err != nil {
			if err.Error() == "im user already bound" {
				c.JSON(http.StatusConflict, gin.H{"error": "im user already bound"})
				return
			}
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusCreated, gin.H{"binding": b})
	}
}

// DeleteIMBindingHandler 删除绑定（owner 校验：仅本人可删自己的绑定）。
func DeleteIMBindingHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := currentUserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		id, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid binding id"})
			return
		}
		b, berr := repo.GetIMBindingByID(db, uint(id))
		if berr != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "im binding not found"})
			return
		}
		if b.UserID != uid {
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		if derr := repo.DeleteIMBinding(db, b.ID); derr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete im binding"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"deleted": true})
	}
}
