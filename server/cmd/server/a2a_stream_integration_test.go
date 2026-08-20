package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/a2a"
	"github.com/ayanmw/multiagent2/server/internal/artifact"
	"github.com/ayanmw/multiagent2/server/internal/config"
	"github.com/ayanmw/multiagent2/server/internal/provider"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/gin-gonic/gin"
)

// setupA2AStreamBackend 构造一个真实可寻址的 A2A 后端（httptest.Server 包装 buildRouter +
// mock LLM + 已启用默认模型），供 a2a.Client 以真实 HTTP 调 message/send 与 message/stream
// （M8-01 验收：外部 client 长任务拿到进度流）。
// 返回 server 地址与已登录（developer）用户的 JWT（client 以 Authorization: Bearer 调用）。
func setupA2AStreamBackend(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	mockLLM := newM1HTTPMockLLM()
	t.Cleanup(mockLLM.Close)

	dbPath := filepath.Join(t.TempDir(), "a2a-stream-e2e.db")
	enc := sha256.Sum256([]byte("a2a-stream-e2e-enc-key"))
	cfg := &config.Config{
		DBPath:        dbPath,
		Port:          "0",
		JWTSecret:     "a2a-stream-e2e-secret",
		EncryptionKey: enc[:],
		WorkspaceRoot: filepath.Join(t.TempDir(), "ws"),
	}
	db, err := repo.NewDB(cfg)
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, e := db.DB.DB(); e == nil {
			_ = sqlDB.Close()
		}
	})
	disc := provider.NewDiscoverer(cfg.EncryptionKey, time.Minute)
	stateStore := artifact.NewMemoryStore()
	r := buildRouter(db, cfg, disc, stateStore, true, nil, nil, nil, buildGateway(db, cfg, stateStore, true, nil, nil, nil))
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	c := &e2eClient{t: t, r: r}
	code, reg := c.do("POST", "/api/auth/register", map[string]any{
		"username": "a2astream", "email": "a2astream@example.com", "password": "secret123",
	})
	if code != http.StatusCreated {
		t.Fatalf("注册失败: %d %v", code, reg)
	}
	token := reg["token"].(string)
	c.tok = token // e2eClient.do 后续请求（建 Provider 等）依赖 c.tok 鉴权。

	code, prov := c.do("POST", "/api/providers", map[string]any{
		"name": "a2a-stream-openai", "protocol": "openai", "base_url": mockLLM.URL + "/v1", "api_key": "k",
	})
	if code != http.StatusCreated {
		t.Fatalf("建 Provider 失败: %d %v", code, prov)
	}
	pid := uint(prov["id"].(float64))
	code, sync := c.do("POST", "/api/providers/"+fmt.Sprintf("%d", pid)+"/models/sync", nil)
	if code != http.StatusOK {
		t.Fatalf("同步模型失败: %d %v", code, sync)
	}
	models := sync["models"].([]any)
	mid := uint(models[0].(map[string]any)["id"].(float64))
	c.do("PUT", "/api/providers/"+fmt.Sprintf("%d", pid)+"/models/"+fmt.Sprintf("%d", mid),
		map[string]any{"enabled": true, "is_default": true})

	return srv, token
}

// TestA2A_Stream_ProgressAndComplete 验证 message/stream（M8-01 服务端）：
// 外部 client 以 a2a.Client 订阅任务进度流，应依次收到 working 进度帧（携带文本增量）
// 与 completed 终帧（携带完整回复），并收到产物帧 reply.txt；最终 Task 状态正确。
func TestA2A_Stream_ProgressAndComplete(t *testing.T) {
	srv, token := setupA2AStreamBackend(t)
	client := a2a.NewClient(srv.URL, "")
	client.Headers = map[string]string{"Authorization": "Bearer " + token}

	var statuses []a2a.TaskStatus
	var artifacts []a2a.Artifact
	task, err := client.StreamMessage(context.Background(), a2a.TaskSendParams{
		ID:      "task-stream-1",
		Message: a2a.Message{Role: "user", Parts: []a2a.Part{{Text: "你好 A2A 流式"}}},
	}, func(s a2a.TaskStatus) { statuses = append(statuses, s) }, func(a a2a.Artifact) { artifacts = append(artifacts, a) })
	if err != nil {
		t.Fatalf("message/stream 失败: %v", err)
	}
	if task.ID != "task-stream-1" {
		t.Fatalf("task.id 应为 task-stream-1: %v", task.ID)
	}
	if task.Status.State != "completed" {
		t.Fatalf("最终状态应为 completed: %+v", task.Status)
	}

	// 进度流断言：至少一个带增量文本的 working 中间帧 + completed 终帧。
	hasWorking, hasCompleted := false, false
	for _, s := range statuses {
		switch s.State {
		case "working":
			hasWorking = true
			if s.Message == nil || len(s.Message.Parts) == 0 || s.Message.Text() == "" {
				t.Fatalf("working 进度帧应携带增量文本: %+v", s)
			}
		case "completed":
			hasCompleted = true
		}
	}
	if !hasWorking || !hasCompleted {
		t.Fatalf("进度流应含 working 与 completed 帧: %+v", statuses)
	}
	// 完整回复：mock LLM 回声「回声首句：你好 A2A 流式」（可见于终帧与产物帧）。
	reply := task.Status.Message.Text()
	if !contains(reply, "回声首句：你好 A2A 流式") {
		t.Fatalf("终帧回复异常: %q", reply)
	}
	if len(artifacts) != 1 || artifacts[0].Name != "reply.txt" ||
		artifacts[0].Parts[0].Text != reply {
		t.Fatalf("产物帧应为 reply.txt 且内容等于完整回复: %+v", artifacts)
	}
	t.Logf("✅ A2A message/stream：进度帧=%d（含 working/completed）产物=%d reply=%q",
		len(statuses), len(artifacts), reply)
}

// TestA2A_Stream_MultiRound 验证 message/stream 同一 task id 的多轮续聊
// （与 message/send 共享统一 Gateway 会话，第二轮经历史回灌仍引用首轮上下文）。
func TestA2A_Stream_MultiRound(t *testing.T) {
	srv, token := setupA2AStreamBackend(t)
	client := a2a.NewClient(srv.URL, "")
	client.Headers = map[string]string{"Authorization": "Bearer " + token}

	ctx := context.Background()
	if _, err := client.StreamMessage(ctx, a2a.TaskSendParams{
		ID:      "task-stream-multi",
		Message: a2a.Message{Role: "user", Parts: []a2a.Part{{Text: "首轮流式问题"}}},
	}, nil, nil); err != nil {
		t.Fatalf("第一轮 message/stream 失败: %v", err)
	}
	final, err := client.StreamMessage(ctx, a2a.TaskSendParams{
		ID:      "task-stream-multi",
		Message: a2a.Message{Role: "user", Parts: []a2a.Part{{Text: "续轮追问"}}},
	}, nil, nil)
	if err != nil {
		t.Fatalf("第二轮 message/stream 失败: %v", err)
	}
	if final.Status.State != "completed" {
		t.Fatalf("第二轮最终状态应为 completed: %+v", final.Status)
	}
	// mock LLM 回声「第一条 user 消息」：第二轮应携带首轮上下文「首轮流式问题」。
	reply := final.Status.Message.Text()
	if !contains(reply, "首轮流式问题") {
		t.Fatalf("第二轮回复未携带首轮上下文（多轮记忆失效）: %q", reply)
	}
	t.Logf("✅ A2A message/stream 多轮续聊：第二轮 completed 且携带首轮上下文 reply=%q", reply)
}

// TestA2AClient_ExternalAgent 验证平台可作 A2A client 调外部 Agent（M8-01 client 侧）：
// 拉 Agent Card → message/send → message/stream（进度流 + 产物），全程走真实 HTTP。
func TestA2AClient_ExternalAgent(t *testing.T) {
	jsonOf := func(v any) string {
		b, _ := json.Marshal(v)
		return string(b)
	}
	// mock 外部 Agent：Agent Card + /api/a2a（message/send / message/stream）。
	extSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/.well-known/agent.json":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(a2a.AgentCard{
				ProtocolVersion: a2a.ProtocolVersion,
				Name:            "external-agent",
				Description:     "外部演示 Agent",
				URL:             r.Host + "/api/a2a",
				Capabilities:    a2a.Capabilities{Streaming: true, StateTransitionHistory: true},
				Skills:          []a2a.Skill{{ID: "ext-skill", Name: "外部技能", Description: "外部 Agent 提供的一项能力"}},
			})
		case r.URL.Path == "/api/a2a":
			var req a2a.JSONRPCRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			switch req.Method {
			case a2a.MethodMessageSend:
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(a2a.JSONRPCResponse{
					JSONRPC: "2.0", ID: req.ID,
					Result: a2a.Task{
						ID: "ext-task-1", SessionID: "sess-ext",
						Status: a2a.TaskStatus{State: "completed", Timestamp: a2a.NowRFC3339()},
						History: []a2a.Message{
							{Role: "user", Parts: []a2a.Part{{Text: req.Params.Message.Text()}}},
							{Role: "agent", Parts: []a2a.Part{{Text: "外部 Agent 已处理：" + req.Params.Message.Text()}}},
						},
					},
				})
			case a2a.MethodMessageStream:
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Cache-Control", "no-cache")
				f, _ := w.(http.Flusher)
				// 首帧（无 event 行）：JSON-RPC 响应，result=Task working。
				fmt.Fprintf(w, "data: %s\n\n", jsonOf(a2a.JSONRPCResponse{
					JSONRPC: "2.0", ID: req.ID,
					Result: a2a.Task{ID: "ext-task-1", SessionID: "sess-ext", Status: a2a.TaskStatus{State: "working"}},
				}))
				// 中间帧：进度（增量文本）。
				fmt.Fprintf(w, "event: task.status_update\ndata: %s\n\n", jsonOf(a2a.TaskStatusUpdateEvent{
					ID:     "ext-task-1",
					Status: a2a.TaskStatus{State: "working", Timestamp: a2a.NowRFC3339(), Message: &a2a.Message{Role: "agent", Parts: []a2a.Part{{Text: "处理中..."}}}},
				}))
				// 终帧：completed + 完整回复。
				fmt.Fprintf(w, "event: task.status_update\ndata: %s\n\n", jsonOf(a2a.TaskStatusUpdateEvent{
					ID:     "ext-task-1",
					Status: a2a.TaskStatus{State: "completed", Timestamp: a2a.NowRFC3339(), Message: &a2a.Message{Role: "agent", Parts: []a2a.Part{{Text: "外部 Agent 完成"}}}},
				}))
				// 产物帧。
				fmt.Fprintf(w, "event: task.artifact_update\ndata: %s\n\n", jsonOf(a2a.TaskArtifactUpdateEvent{
					ID:       "ext-task-1",
					Artifact: a2a.Artifact{ArtifactID: "a1", Name: "out.txt", Parts: []a2a.Part{{Text: "外部产物"}}},
				}))
				if f != nil {
					f.Flush()
				}
			default:
				http.Error(w, "unsupported method: "+req.Method, http.StatusBadRequest)
			}
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	t.Cleanup(extSrv.Close)

	ctx := context.Background()
	client := a2a.NewClient(extSrv.URL, "ext-key")

	// ① 拉 Agent Card：应声明协议版本与流式能力。
	card, err := client.FetchAgentCard(ctx)
	if err != nil {
		t.Fatalf("FetchAgentCard 失败: %v", err)
	}
	if card.ProtocolVersion != a2a.ProtocolVersion || !card.Capabilities.Streaming {
		t.Fatalf("Agent Card 应声明协议 %s 且 streaming=true: %+v", a2a.ProtocolVersion, card)
	}

	// ② message/send（非流式）。
	task, err := client.SendMessage(ctx, a2a.TaskSendParams{
		ID:      "ext-task-1",
		Message: a2a.Message{Role: "user", Parts: []a2a.Part{{Text: "帮我做个演示"}}},
	})
	if err != nil {
		t.Fatalf("SendMessage 失败: %v", err)
	}
	if task.Status.State != "completed" {
		t.Fatalf("send 任务状态应为 completed: %+v", task.Status)
	}
	if len(task.History) < 2 || task.History[1].Text() != "外部 Agent 已处理：帮我做个演示" {
		t.Fatalf("send 返回历史异常: %+v", task.History)
	}

	// ③ message/stream（流式）：进度帧 + completed + 产物。
	var statuses []a2a.TaskStatus
	var artifacts []a2a.Artifact
	streamTask, err := client.StreamMessage(ctx, a2a.TaskSendParams{
		ID:      "ext-task-1",
		Message: a2a.Message{Role: "user", Parts: []a2a.Part{{Text: "流式演示"}}},
	}, func(s a2a.TaskStatus) { statuses = append(statuses, s) }, func(a a2a.Artifact) { artifacts = append(artifacts, a) })
	if err != nil {
		t.Fatalf("StreamMessage 失败: %v", err)
	}
	if streamTask.Status.State != "completed" {
		t.Fatalf("stream 任务最终状态应为 completed: %+v", streamTask.Status)
	}
	if streamTask.ID != "ext-task-1" {
		t.Fatalf("stream task.id 应为 ext-task-1: %v", streamTask.ID)
	}
	hasWorking, hasCompleted := false, false
	for _, s := range statuses {
		if s.State == "working" && s.Message != nil && s.Message.Text() == "处理中..." {
			hasWorking = true
		}
		if s.State == "completed" && s.Message != nil && s.Message.Text() == "外部 Agent 完成" {
			hasCompleted = true
		}
	}
	if !hasWorking || !hasCompleted {
		t.Fatalf("外部 Agent 进度流应含 working(处理中...) 与 completed(外部 Agent 完成): %+v", statuses)
	}
	if len(artifacts) != 1 || artifacts[0].Name != "out.txt" || artifacts[0].Parts[0].Text != "外部产物" {
		t.Fatalf("外部 Agent 产物帧异常: %+v", artifacts)
	}
	t.Log("✅ A2A client 调外部 Agent：Agent Card 发现 + message/send + message/stream（进度流+产物）全部成功")
}
