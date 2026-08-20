// Package im 是 IM Channel（M8-07）的协议层：把飞书/钉钉/企微三种 IM 平台的
// 入站 webhook 事件统一解析为 InboundMessage，按平台验签，并按平台格式构造出站回发。
//
// 设计约定：
//   - 本包**无框架依赖、无 os/exec**，全部为可单测的纯函数 + 一个轻量 HTTP Sender；
//   - 入站验签采用「配置 secret 非空即启用，空则跳过」模式（与 M6-05 webhook 签名同款），
//     生产必须配置 secret，本地调试/集成测试可不配；
//   - 出站回发走各平台「自定义机器人 webhook」格式（HTTP POST JSON），
//     Sender 以接口暴露，便于测试注入 mock 或 httptest.Server 真跑。
package im

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Platform 是 IM 平台枚举。
type Platform string

const (
	Feishu   Platform = "feishu"   // 飞书（Lark）
	DingTalk Platform = "dingtalk" // 钉钉
	WeCom    Platform = "wecom"    // 企业微信
)

// ParsePlatform 解析平台字符串，非法值返回 false。
func ParsePlatform(s string) (Platform, bool) {
	switch Platform(s) {
	case Feishu, DingTalk, WeCom:
		return Platform(s), true
	default:
		return "", false
	}
}

// InboundMessage 是 IM 入站消息的统一形态（各平台解析后的共同最小集）。
// ChatID 语义 = 「回发目标地址」：飞书=chat_id(oc_xxx)、钉钉=conversationId(cidxxx)、
// 企微=FromUserName(userid)。SenderID 用于绑定匹配：飞书=open_id、钉钉=senderStaffId、
// 企微=FromUserName(userid)。
type InboundMessage struct {
	Platform Platform
	SenderID string // IM 平台侧的用户标识（用于绑定匹配）
	ChatID   string // 回发目标地址
	Text     string // 文本内容（仅 text 消息）
}

// Sender 是 IM 出站回发抽象：把 text 发回 msg 的来源 chat。
// 生产实现为 HTTPSender（POST 平台机器人 webhook）；测试注入 mock 或 httptest.Server。
type Sender interface {
	Send(ctx context.Context, msg InboundMessage, text string) error
}

// HTTPSender 是生产回发实现：HTTP POST JSON 到平台自定义机器人 webhook URL。
type HTTPSender struct {
	client  *http.Client
	webhook string
	timeout time.Duration
}

// NewHTTPSender 构造回发客户端。webhook 为空时 Send 返回 ErrNoWebhook（调用方可跳过回发）。
// timeout<=0 时使用默认 10s。
func NewHTTPSender(webhook string, timeout time.Duration) *HTTPSender {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &HTTPSender{client: &http.Client{Timeout: timeout}, webhook: webhook, timeout: timeout}
}

// ErrNoWebhook 表示未配置出站 webhook URL，回发不可用（best-effort 调用方可忽略）。
var ErrNoWebhook = fmt.Errorf("im: outbound webhook url not configured")

// Send 构造平台格式的 JSON payload 并 POST 到 webhook URL。
// 非 2xx 响应返回带状态码的错误（便于排障，如 token 失效/被限流）。
func (s *HTTPSender) Send(ctx context.Context, msg InboundMessage, text string) error {
	if s.webhook == "" {
		return ErrNoWebhook
	}
	payload, err := OutboundPayload(msg.Platform, text)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.webhook, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("im: send to %s failed: status=%d", msg.Platform, resp.StatusCode)
	}
	return nil
}

// OutboundPayload 按平台构造出站回发的 JSON payload（纯函数，便于单测断言格式）。
// 三种平台自定义机器人文本消息均为 {msg_type|msgtype,text:{content}}，仅键名不同。
func OutboundPayload(p Platform, text string) ([]byte, error) {
	switch p {
	case Feishu:
		return json.Marshal(map[string]any{
			"msg_type": "text",
			"content":  map[string]string{"text": text},
		})
	case DingTalk, WeCom:
		return json.Marshal(map[string]any{
			"msgtype": "text",
			"text":    map[string]string{"content": text},
		})
	default:
		return nil, fmt.Errorf("im: unsupported platform %q", p)
	}
}
