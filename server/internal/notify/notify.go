// Package notify 实现 M4-07「通知/结果回发（outbound）」统一出口。
//
// 自主化 Loop（cron/webhook/recover）完成 / 失败 / 需人工检查点时，经本包的通知器
// 把结果「回发」给用户：①站内信（notifications 表，前端通知中心消费）；
// ②出站 webhook 回调（占位，可配置目标 URL，本地以 mock 验证，不依赖真实外网）。
//
// 设计要点：
//   - Notifier 接口把「副作用」抽象出来，production 实现写库 + 打回调，
//     scheduler/webhook/recovery 三处调用方与具体副作用解耦，单测可注入 mock；
//   - 站内信落库失败时仅告警不阻断主流程（自主化 Loop 的结果不能因为通知失败而失败）；
//   - 出站 webhook 回调默认关闭（WEBHOOK_NOTIFY_URL 为空），开启时失败也只记录日志，
//     不阻断 Loop —— 通知是「尽力而为」的旁路能力。
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"gorm.io/gorm"
)

// Notifier 是通知出口的抽象（production=DB + 出站 webhook；测试=mock）。
type Notifier interface {
	// Notify 发送一条站内信（并触发出站 webhook 回调，若已配置）。
	Notify(ctx context.Context, n *model.Notification) error
}

// Service 是 production 通知器：写库 + 出站 webhook 回调（可关闭）。
type Service struct {
	DB          *gorm.DB
	CallbackURL string        // 出站 webhook 回调地址（占位，为空则跳过）
	HTTPClient  *http.Client  // 出站回调 HTTP 客户端（可注入，便于测试）
	Logger      *log.Logger   // 可空，空则回退 log.Default()
}

// compile-time 接口满足检查。
var _ Notifier = (*Service)(nil)

func (s *Service) logger() *log.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return log.Default()
}

// Notify 写入站内信；若配置了出站回调地址，额外 POST 一条 JSON 事件（best-effort）。
// 任意通知副作用失败都只记录日志，绝不向上抛出（避免阻断自主化 Loop 主流程）。
func (s *Service) Notify(ctx context.Context, n *model.Notification) error {
	if s.DB != nil {
		if err := repo.CreateNotification(s.DB, n); err != nil {
			s.logger().Printf("[NOTIFY] 写入站内信失败(user=%d, type=%s): %v", n.UserID, n.Type, err)
		}
	}
	if s.CallbackURL != "" {
		s.postCallback(ctx, n)
	}
	return nil
}

// postCallback 向配置的出站 webhook 回调地址发送通知事件（best-effort）。
func (s *Service) postCallback(ctx context.Context, n *model.Notification) {
	payload, err := json.Marshal(map[string]any{
		"type":     n.Type,
		"title":    n.Title,
		"message":  n.Message,
		"user_id":  n.UserID,
		"ref_kind": n.RefKind,
		"ref_id":   n.RefID,
	})
	if err != nil {
		s.logger().Printf("[NOTIFY] 序列化出站回调失败: %v", err)
		return
	}
	client := s.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.CallbackURL, bytes.NewReader(payload))
	if err != nil {
		s.logger().Printf("[NOTIFY] 构造出站回调请求失败: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if resp, err := client.Do(req); err != nil {
		s.logger().Printf("[NOTIFY] 出站回调失败(url=%s): %v", s.CallbackURL, err)
		return
	} else {
		_ = resp.Body.Close()
		if resp.StatusCode >= 300 {
			s.logger().Printf("[NOTIFY] 出站回调返回非 2xx(url=%s, code=%d)", s.CallbackURL, resp.StatusCode)
		}
	}
}

// NewService 构造 production 通知器。callbackURL 为空表示不开出站 webhook 回调。
func NewService(db *gorm.DB, callbackURL string, logger *log.Logger) *Service {
	return &Service{DB: db, CallbackURL: callbackURL, Logger: logger}
}

// NewSuccess / NewFailure / NewCheckpoint 是三处主流程对应的通知构造 helper，
// 统一标题/类型/来源，调用方只填 user/automation/正文即可，避免散落字符串。

// NewSuccess 构造「自动化完成」通知。
func NewSuccess(userID uint, automationID uint, automationName, sessionKey, reply string) *model.Notification {
	return &model.Notification{
		UserID:  userID,
		Type:    model.NotificationTypeSuccess,
		Title:   fmt.Sprintf("自动化「%s」已执行完成", automationName),
		Message: shortReply(reply),
		RefKind: model.NotificationRefAutomation,
		RefID:   fmt.Sprintf("%d", automationID),
	}
}

// NewFailure 构造「自动化失败」通知。
func NewFailure(userID uint, automationID uint, automationName, reason string) *model.Notification {
	return &model.Notification{
		UserID:  userID,
		Type:    model.NotificationTypeFailure,
		Title:   fmt.Sprintf("自动化「%s」执行失败", automationName),
		Message: reason,
		RefKind: model.NotificationRefAutomation,
		RefID:   fmt.Sprintf("%d", automationID),
	}
}

// NewCheckpoint 构造「需人工检查点」通知（ref_id 携带检查点展示编号）。
func NewCheckpoint(userID uint, automationID uint, automationName, checkpointDisplayID, command string) *model.Notification {
	return &model.Notification{
		UserID:  userID,
		Type:    model.NotificationTypeCheckpoint,
		Title:   fmt.Sprintf("自动化「%s」有待审批命令", automationName),
		Message: fmt.Sprintf("检查点 %s：%s", checkpointDisplayID, command),
		RefKind: model.NotificationRefCheckpoint,
		RefID:   checkpointDisplayID,
	}
}

// shortReply 截断过长的结果正文，避免一条通知塞入整段 Loop 输出。
func shortReply(reply string) string {
	const max = 200
	if len(reply) <= max {
		return reply
	}
	return reply[:max] + "…"
}
