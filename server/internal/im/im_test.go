package im

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
)

func hmacSHA256(key string, data []byte) []byte {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write(data)
	return mac.Sum(nil)
}

// ---- 飞书解析 ----

func TestParseFeishuEvent(t *testing.T) {
	body := []byte(`{"schema":"2.0","header":{"event_id":"e1","event_type":"im.message.receive_v1","token":"t"},
"event":{"sender":{"sender_id":{"open_id":"ou_123"},"sender_type":"user"},
"message":{"message_id":"om_1","chat_id":"oc_456","message_type":"text","content":"{\"text\":\"你好\"}"}}}`)
	msg, err := ParseFeishuEvent(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Platform != Feishu || msg.SenderID != "ou_123" || msg.ChatID != "oc_456" || msg.Text != "你好" {
		t.Fatalf("unexpected message: %+v", msg)
	}
}

func TestParseFeishuEvent_NonText(t *testing.T) {
	body := []byte(`{"header":{"event_type":"im.message.receive_v1"},"event":{"sender":{"sender_type":"user"},
"message":{"message_type":"image","content":"{}"}}}`)
	if _, err := ParseFeishuEvent(body); err == nil {
		t.Fatal("expected error for non-text message")
	}
}

func TestParseFeishuEvent_WrongEventType(t *testing.T) {
	body := []byte(`{"header":{"event_type":"im.chat.member.added"},"event":{}}`)
	if _, err := ParseFeishuEvent(body); err == nil {
		t.Fatal("expected error for wrong event_type")
	}
}

// ---- 飞书验签 ----

func TestVerifyFeishu(t *testing.T) {
	secret := "test-secret"
	body := []byte(`{"schema":"2.0","header":{"event_type":"im.message.receive_v1"}}`)
	// 正确签名：base64(HMAC-SHA256(secret, "1234567890\n"+body))
	sig := feishuSign(secret, "1234567890", body)
	if !VerifyFeishu(secret, "1234567890", sig, body) {
		t.Fatal("valid signature rejected")
	}
	if VerifyFeishu(secret, "1234567890", "bad-signature", body) {
		t.Fatal("invalid signature accepted")
	}
	if VerifyFeishu(secret, "", sig, body) {
		t.Fatal("missing timestamp should be rejected")
	}
	if !VerifyFeishu("", "1234567890", "", body) {
		t.Fatal("empty secret should bypass verification")
	}
}

// ---- 钉钉解析 ----

func TestParseDingTalkEvent(t *testing.T) {
	// 新版 stream 模式字段
	body := []byte(`{"senderStaffId":"staff-1","conversationId":"cid123","msgtype":"text","text":{"content":"部署一下"}}`)
	msg, err := ParseDingTalkEvent(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Platform != DingTalk || msg.SenderID != "staff-1" || msg.ChatID != "cid123" || msg.Text != "部署一下" {
		t.Fatalf("unexpected message: %+v", msg)
	}
	// 旧版 outgoing 字段回退
	body2 := []byte(`{"senderId":"staff-old","conversationId":"cid456","text":{"content":"hi"}}`)
	msg2, err := ParseDingTalkEvent(body2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg2.SenderID != "staff-old" || msg2.ChatID != "cid456" || msg2.Text != "hi" {
		t.Fatalf("unexpected message: %+v", msg2)
	}
}

func TestParseDingTalkEvent_NonText(t *testing.T) {
	body := []byte(`{"conversationId":"cid1","msgtype":"link","text":{}}`)
	if _, err := ParseDingTalkEvent(body); err == nil {
		t.Fatal("expected error for non-text msgtype")
	}
}

// ---- 钉钉验签 ----

func TestVerifyDingTalk(t *testing.T) {
	secret := "SECtest"
	timestamp := "1700000000000"
	body := []byte(`{"conversationId":"cid1"}`)
	sign := dingTalkSign(secret, timestamp, body)
	if !VerifyDingTalk(secret, timestamp, sign, body) {
		t.Fatal("valid signature rejected")
	}
	if VerifyDingTalk(secret, timestamp, "bad", body) {
		t.Fatal("invalid signature accepted")
	}
	if VerifyDingTalk(secret, "", sign, body) {
		t.Fatal("missing timestamp should be rejected")
	}
	if !VerifyDingTalk("", timestamp, "", body) {
		t.Fatal("empty secret should bypass verification")
	}
}

// ---- 企微解析 ----

func TestParseWeComEvent(t *testing.T) {
	body := []byte(`{"ToUserName":"ww-bot","FromUserName":"zhangsan","MsgType":"text","Content":"写周报","MsgId":"m1"}`)
	msg, err := ParseWeComEvent(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Platform != WeCom || msg.SenderID != "zhangsan" || msg.ChatID != "zhangsan" || msg.Text != "写周报" {
		t.Fatalf("unexpected message: %+v", msg)
	}
}

func TestParseWeComEvent_NonText(t *testing.T) {
	body := []byte(`{"FromUserName":"zhangsan","MsgType":"event","Event":"subscribe"}`)
	if _, err := ParseWeComEvent(body); err == nil {
		t.Fatal("expected error for non-text event")
	}
}

// ---- 企微验签 ----

func TestVerifyWeCom(t *testing.T) {
	secret := "wecom-token"
	timestamp := "1348831860"
	nonce := "nonce-1"
	body := []byte(`{"ToUserName":"ww-bot","FromUserName":"zhangsan","MsgType":"text","Content":"hi"}`)
	sig := weComSign(secret, timestamp, nonce, body)
	if !VerifyWeCom(secret, timestamp, nonce, sig, body) {
		t.Fatal("valid signature rejected")
	}
	if VerifyWeCom(secret, timestamp, nonce, "bad", body) {
		t.Fatal("invalid signature accepted")
	}
	if VerifyWeCom(secret, "", nonce, sig, body) {
		t.Fatal("missing timestamp should be rejected")
	}
	if !VerifyWeCom("", timestamp, nonce, "", body) {
		t.Fatal("empty secret should bypass verification")
	}
}

// ---- 出站 payload 格式 ----

func TestOutboundPayload(t *testing.T) {
	for _, tc := range []struct {
		p        Platform
		wantKey  string
		wantText string
	}{
		{Feishu, "msg_type", "hi"},
		{DingTalk, "msgtype", "hi"},
		{WeCom, "msgtype", "hi"},
	} {
		b, err := OutboundPayload(tc.p, "hi")
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", tc.p, err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("%s: invalid payload json: %v", tc.p, err)
		}
		if _, ok := m[tc.wantKey]; !ok {
			t.Fatalf("%s: missing key %q in %s", tc.p, tc.wantKey, string(b))
		}
	}
	if _, err := OutboundPayload(Platform("slack"), "hi"); err == nil {
		t.Fatal("expected error for unsupported platform")
	}
}

// ---- 签名辅助（与生产实现同算法，仅用于构造合法样本） ----

func feishuSign(secret, timestamp string, body []byte) string {
	return base64.StdEncoding.EncodeToString(hmacSHA256(secret, append([]byte(timestamp+"\n"), body...)))
}

func dingTalkSign(secret, timestamp string, body []byte) string {
	return base64.StdEncoding.EncodeToString(hmacSHA256(secret, []byte(timestamp+"\n"+secret)))
}

func weComSign(secret, timestamp, nonce string, body []byte) string {
	parts := []string{secret, timestamp, nonce, string(body)}
	sort.Strings(parts)
	h := sha1.Sum([]byte(strings.Join(parts, "")))
	return hex.EncodeToString(h[:])
}

// ---- HTTPSender 真实 HTTP 路径 ----

func TestHTTPSender_Send(t *testing.T) {
	var got []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		got = buf
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := NewHTTPSender(srv.URL, 0)
	err := s.Send(context.Background(), InboundMessage{Platform: Feishu, ChatID: "oc_1"}, "hello im")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) == "" {
		t.Fatal("server received no body")
	}
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("invalid payload: %v", err)
	}
	if m["msg_type"] != "text" {
		t.Fatalf("unexpected payload: %s", string(got))
	}
}

func TestHTTPSender_Send_NoWebhook(t *testing.T) {
	s := NewHTTPSender("", 0)
	err := s.Send(context.Background(), InboundMessage{Platform: Feishu}, "x")
	if err != ErrNoWebhook {
		t.Fatalf("expected ErrNoWebhook, got %v", err)
	}
}

func TestHTTPSender_Send_Non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	s := NewHTTPSender(srv.URL, 0)
	err := s.Send(context.Background(), InboundMessage{Platform: Feishu}, "x")
	if err == nil {
		t.Fatal("expected error on non-2xx")
	}
}
