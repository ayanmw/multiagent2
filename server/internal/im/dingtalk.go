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
// 钉钉 —— 机器人回调（旧版 outgoing webhook / 新版 stream 模式的入站 JSON 同构）
//
// 入站验签（outgoing webhook 回调地址签名）：
//   timestamp 与 sign 位于 URL query（或 header），
//   sign = base64( HMAC-SHA256(timestamp + "\n" + secret, secret) )
//
// 回发：自定义机器人 webhook，payload {"msgtype":"text","text":{"content":...}}
// ---------------------------------------------------------------------------

// dingTalkEvent 是钉钉机器人回调的最小公共信封（新旧模式字段并集）。
type dingTalkEvent struct {
	ConversationID string `json:"conversationId"` // cidxxx
	SenderStaffID  string `json:"senderStaffId"`  // 新版：发送者 staffId
	SenderID       string `json:"senderId"`       // 旧版：发送者 id
	MsgType        string `json:"msgtype"`
	Text           struct {
		Content string `json:"content"`
	} `json:"text"`
}

// ParseDingTalkEvent 解析钉钉机器人回调为 InboundMessage。仅接受 msgtype=text。
func ParseDingTalkEvent(body []byte) (InboundMessage, error) {
	var ev dingTalkEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		return InboundMessage{}, fmt.Errorf("im/dingtalk: invalid event json: %w", err)
	}
	if ev.MsgType != "" && ev.MsgType != "text" {
		return InboundMessage{}, fmt.Errorf("im/dingtalk: unsupported msgtype %q", ev.MsgType)
	}
	sender := ev.SenderStaffID
	if sender == "" {
		sender = ev.SenderID
	}
	msg := InboundMessage{
		Platform: DingTalk,
		SenderID: sender,
		ChatID:   ev.ConversationID,
		Text:     strings.TrimSpace(ev.Text.Content),
	}
	if msg.SenderID == "" || msg.ChatID == "" {
		return InboundMessage{}, fmt.Errorf("im/dingtalk: missing sender or conversationId")
	}
	if msg.Text == "" {
		return InboundMessage{}, fmt.Errorf("im/dingtalk: empty text message")
	}
	return msg, nil
}

// VerifyDingTalk 校验钉钉回调签名：base64(HMAC-SHA256(timestamp+"\n"+secret, secret))。
// secret 为空时直接放行。timestamp/sign 缺任一且 secret 非空则拒绝。
func VerifyDingTalk(secret, timestamp, sign string, body []byte) bool {
	if secret == "" {
		return true
	}
	if timestamp == "" || sign == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "\n" + secret))
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(sign))
}
