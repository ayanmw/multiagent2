package im

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ---------------------------------------------------------------------------
// 企业微信 —— 接收消息服务器（明文回调模式）
//
// 入站验签（明文模式）：
//   query 带 msg_signature / timestamp / nonce，
//   msg_signature = sha1hex( sort(token, timestamp, nonce, body) )，
//   其中 token 即「接收消息服务器配置」里的 Token（secret 配置项）。
//
// 注意：企业微信生产环境建议开启「安全模式」（AES 加解密），此时需对 msg_encrypt
// 解密后再解析；本实现为明文模式（务实降级），文档 docs/im-channel.md 已注明。
//
// 回发：机器人 webhook，payload {"msgtype":"text","text":{"content":...}}；
// 企微机器人主动回发按 userid（touser）寻址，故 ChatID = FromUserName。
// ---------------------------------------------------------------------------

// weComEvent 是企业微信明文回调的最小信封。
type weComEvent struct {
	ToUserName   string `json:"ToUserName"`
	FromUserName string `json:"FromUserName"`
	MsgType      string `json:"MsgType"`
	Content      string `json:"Content"`
	MsgID        string `json:"MsgId"`
}

// ParseWeComEvent 解析企业微信回调为 InboundMessage。仅接受 MsgType=text。
func ParseWeComEvent(body []byte) (InboundMessage, error) {
	var ev weComEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		return InboundMessage{}, fmt.Errorf("im/wecom: invalid event json: %w", err)
	}
	if ev.MsgType != "" && ev.MsgType != "text" {
		return InboundMessage{}, fmt.Errorf("im/wecom: unsupported MsgType %q", ev.MsgType)
	}
	msg := InboundMessage{
		Platform: WeCom,
		SenderID: ev.FromUserName,
		ChatID:   ev.FromUserName, // 企微机器人回发以 userid 寻址（touser）
		Text:     strings.TrimSpace(ev.Content),
	}
	if msg.SenderID == "" {
		return InboundMessage{}, fmt.Errorf("im/wecom: missing FromUserName")
	}
	if msg.Text == "" {
		return InboundMessage{}, fmt.Errorf("im/wecom: empty text message")
	}
	return msg, nil
}

// VerifyWeCom 校验企业微信明文回调签名：sha1hex( sort(secret, timestamp, nonce, body) )。
// secret（接收消息服务器 Token）为空时直接放行。
func VerifyWeCom(secret, timestamp, nonce, signature string, body []byte) bool {
	if secret == "" {
		return true
	}
	if timestamp == "" || nonce == "" || signature == "" {
		return false
	}
	parts := []string{secret, timestamp, nonce, string(body)}
	sort.Strings(parts)
	h := sha1.Sum([]byte(strings.Join(parts, "")))
	expected := hex.EncodeToString(h[:])
	return expected == signature
}
