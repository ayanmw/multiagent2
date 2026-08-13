package a2a

import "testing"

func TestMessage_Text(t *testing.T) {
	cases := []struct {
		name string
		msg  Message
		want string
	}{
		{"text part", Message{Role: "user", Parts: []Part{{Text: "你好"}}}, "你好"},
		{"empty parts", Message{Role: "user", Parts: []Part{{Kind: "file"}}}, ""},
		{"no parts", Message{Role: "user"}, ""},
		{"second text wins", Message{Role: "user", Parts: []Part{{Text: "a"}, {Text: "b"}}}, "a"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.msg.Text(); got != c.want {
				t.Fatalf("Text()=%q want %q", got, c.want)
			}
		})
	}
}

func TestJSONRPCError(t *testing.T) {
	resp := JSONRPCError("req-1", -32601, "unsupported")
	if resp.JSONRPC != "2.0" {
		t.Fatalf("jsonrpc 应为 2.0")
	}
	if resp.Error == nil || resp.Error.Code != -32601 {
		t.Fatalf("error code 错误")
	}
	if resp.ID != "req-1" {
		t.Fatalf("id 应透传")
	}
	if resp.Result != nil {
		t.Fatalf("错误响应不应含 result")
	}
}
