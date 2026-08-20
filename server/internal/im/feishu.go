package im

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// ---------------------------------------------------------------------------
// 飞书（Lark）—— 事件订阅 v2.0 消息事件 im.message.receive_v1
//
// 入站验签（事件订阅「加密策略」可选配置）：
//   header X-Lark-Request-Timestamp + X-Lark-Signature，
//   signature = base64( HMAC-SHA256(encrypt_key, timestamp + "\n" + body) )
//
// 回发：自定义机器人 webhook，payload {"msg_type":"text","content":{"text":...}}
// ---------------------------------------------------------------------------

// feishuEvent 是飞书事件订阅 v2.0 的通用信封（只取解析需要的字段）。
type feishuEvent struct {
	Schema string `json:"schema"`
	Header struct {
		EventType string `json:"event_type"`
		Token     string `json:"token"`
	} `json:"header"`
	Event struct {
		Sender struct {
			SenderID struct {
				OpenID string `json:"open_id"`
			} `json:"sender_id"`
			SenderType string `json:"sender_type"`
		} `json:"sender"`
		Message struct {
			MessageID   string `json:"message_id"`
			ChatID      string `json:"chat_id"`
			MessageType string `json:"message_type"`
			Content     string `json:"content"` // text 消息为 JSON 字符串 {"text":"..."}
		} `json:"message"`
	} `json:"event"`
}

// feishuTextContent 解析 message.content（JSON 字符串）。
type feishuTextContent struct {
	Text string `json:"text"`
}

// ParseFeishuEvent 解析飞书消息事件为 InboundMessage。
// 仅接受 im.message.receive_v1 且 message_type=text；其余类型返回明确错误。
func ParseFeishuEvent(body []byte) (InboundMessage, error) {
	var ev feishuEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		return InboundMessage{}, fmt.Errorf("im/feishu: invalid event json: %w", err)
	}
	if ev.Header.EventType != "im.message.receive_v1" {
		return InboundMessage{}, fmt.Errorf("im/feishu: unsupported event_type %q", ev.Header.EventType)
	}
	if ev.Event.Sender.SenderType != "user" {
		return InboundMessage{}, fmt.Errorf("im/feishu: unsupported sender_type %q", ev.Event.Sender.SenderType)
	}
	if ev.Event.Message.MessageType != "text" {
		return InboundMessage{}, fmt.Errorf("im/feishu: unsupported message_type %q", ev.Event.Message.MessageType)
	}
	var tc feishuTextContent
	if err := json.Unmarshal([]byte(ev.Event.Message.Content), &tc); err != nil {
		return InboundMessage{}, fmt.Errorf("im/feishu: invalid text content: %w", err)
	}
	msg := InboundMessage{
		Platform: Feishu,
		SenderID: ev.Event.Sender.SenderID.OpenID,
		ChatID:   ev.Event.Message.ChatID,
		Text:     strings.TrimSpace(tc.Text),
	}
	if msg.SenderID == "" || msg.ChatID == "" {
		return InboundMessage{}, fmt.Errorf("im/feishu: missing sender_id or chat_id")
	}
	if msg.Text == "" {
		return InboundMessage{}, fmt.Errorf("im/feishu: empty text message")
	}
	return msg, nil
}

// VerifyFeishu 校验飞书事件签名：base64(HMAC-SHA256(secret, timestamp+"\n"+body))，
// 与 header X-Lark-Signature 恒定时间比较。secret 为空时直接放行（未启用验签）。
func VerifyFeishu(secret, timestamp, signature string, body []byte) bool {
	if secret == "" {
		return true
	}
	if timestamp == "" || signature == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "\n"))
	mac.Write(body)
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}
