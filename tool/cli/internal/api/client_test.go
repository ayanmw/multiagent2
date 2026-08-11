package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestParseFrame 验证 AG-UI SSE 帧解析：跳过心跳注释、正确抽取 data: 行事件。
func TestParseFrame(t *testing.T) {
	var got []AGUIEvent
	frame := "data: {\"type\":\"RUN_STARTED\",\"threadId\":\"s1\",\"runId\":\"r1\"}\n\n" +
		"data: {\"type\":\"TEXT_MESSAGE_CONTENT\",\"messageId\":\"m1\",\"delta\":\"你好\"}\n\n" +
		"data: {\"type\":\"TEXT_MESSAGE_CONTENT\",\"messageId\":\"m1\",\"delta\":\"世界\"}\n\n" +
		": heartbeat comment\n\n" +
		"data: {\"type\":\"RUN_FINISHED\",\"threadId\":\"s1\",\"runId\":\"r1\"}\n\n"
	parseFrame(frame, func(ev AGUIEvent) { got = append(got, ev) })
	if len(got) != 4 {
		t.Fatalf("want 4 events, got %d", len(got))
	}
	if got[0].Type != "RUN_STARTED" {
		t.Errorf("ev0 type = %s", got[0].Type)
	}
	if got[1].Delta != "你好" || got[2].Delta != "世界" {
		t.Errorf("delta mismatch: %q %q", got[1].Delta, got[2].Delta)
	}
	if got[3].Type != "RUN_FINISHED" {
		t.Errorf("ev3 type = %s", got[3].Type)
	}
}

// TestClient_StreamChat 用 httptest 验证 StreamChat 端到端：鉴权头、流式增量拼接、事件顺序。
func TestClient_StreamChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat/sess-x/stream" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer tk" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"type\":\"RUN_STARTED\",\"runId\":\"r1\"}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: {\"type\":\"TEXT_MESSAGE_CONTENT\",\"delta\":\"你好\"}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: {\"type\":\"TEXT_MESSAGE_CONTENT\",\"delta\":\"世界\"}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: {\"type\":\"RUN_FINISHED\"}\n\n")
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tk")
	var deltas []string
	var types []string
	err := c.StreamChat(context.Background(), "sess-x", "hi", 0, "", func(ev AGUIEvent) {
		types = append(types, ev.Type)
		if ev.Delta != "" {
			deltas = append(deltas, ev.Delta)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(deltas, "") != "你好世界" {
		t.Errorf("deltas = %v", deltas)
	}
	if len(types) == 0 || types[0] != "RUN_STARTED" || types[len(types)-1] != "RUN_FINISHED" {
		t.Errorf("types = %v", types)
	}
}

// TestClient_DoError 验证非 2xx 响应会返回可读错误（含后端 error 字段）。
func TestClient_DoError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid credentials"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "")
	if _, err := c.Login(context.Background(), "a", "b"); err == nil {
		t.Fatal("expected error")
	} else if !strings.Contains(err.Error(), "invalid credentials") {
		t.Errorf("error message = %q", err.Error())
	}
}
