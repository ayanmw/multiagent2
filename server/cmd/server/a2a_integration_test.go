package main

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/artifact"
	"github.com/ayanmw/multiagent2/server/internal/config"
	"github.com/ayanmw/multiagent2/server/internal/provider"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/gin-gonic/gin"
)

// a2aRPC 发送一个 A2A JSON-RPC 请求（POST /api/a2a），返回顶层响应信封与 HTTP 状态码。
// 用于验证「外部 A2A client 能向本平台发起一个任务并拿到结果」（M5-07 验收标准）。
func (c *e2eClient) a2a(method string, params map[string]any) (int, map[string]any) {
	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      "a2a-req-1",
		"method":  method,
		"params":  params,
	}
	code, out := c.do("POST", "/api/a2a", body)
	// e2eClient.do 已把响应体反序列化为 map，JSON-RPC 嵌套的 result 也保留。
	return code, out
}

// setupA2ABackend 复用 M3 E2E 的基础设施，构造一个带 mock LLM + 已启用默认模型的后端路由。
// 返回 router 与已登录（developer）的 e2eClient，便于直接发 A2A 请求。
func setupA2ABackend(t *testing.T) (*e2eClient, *httptestServerCloser) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	mockLLM := newM1HTTPMockLLM()

	dbPath := filepath.Join(t.TempDir(), "a2a-e2e.db")
	enc := sha256.Sum256([]byte("a2a-e2e-enc-key"))
	cfg := &config.Config{
		DBPath:        dbPath,
		Port:          "0",
		JWTSecret:     "a2a-e2e-secret",
		EncryptionKey: enc[:],
		WorkspaceRoot: filepath.Join(t.TempDir(), "ws"),
	}
	db, err := repo.NewDB(cfg)
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	// 测试结束释放 SQLite 连接，避免 t.TempDir() 清理时文件被锁导致测试被判 FAIL。
	t.Cleanup(func() {
		if sqlDB, e := db.DB.DB(); e == nil {
			_ = sqlDB.Close()
		}
	})
	disc := provider.NewDiscoverer(cfg.EncryptionKey, time.Minute)
	stateStore := artifact.NewMemoryStore()
	r := buildRouter(db, cfg, disc, stateStore, true, nil, nil, nil, buildGateway(db, cfg, stateStore, true, nil, nil, nil))
	c := &e2eClient{t: t, r: r}

	code, reg := c.do("POST", "/api/auth/register", map[string]any{
		"username": "a2adev", "email": "a2adev@example.com", "password": "secret123",
	})
	if code != http.StatusCreated {
		t.Fatalf("注册失败: %d %v", code, reg)
	}
	c.tok = reg["token"].(string)

	// 建 OpenAI Provider → 同步模型 → 启用+默认，供 A2A 引擎调用 mock LLM。
	code, prov := c.do("POST", "/api/providers", map[string]any{
		"name": "a2a-openai", "protocol": "openai", "base_url": mockLLM.URL + "/v1", "api_key": "k",
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

	return c, &httptestServerCloser{srv: mockLLM}
}

// httptestServerCloser 简单包装 httptest.Server，便于 defer 关闭。
type httptestServerCloser struct{ srv *httptest.Server }

func (h *httptestServerCloser) Close() { h.srv.Close() }

// TestA2A_SendTask_Success 验证外部 A2A client 经 message/send 向本平台发起任务并拿到结果。
func TestA2A_SendTask_Success(t *testing.T) {
	c, closer := setupA2ABackend(t)
	defer closer.Close()

	code, out := c.a2a("message/send", map[string]any{
		"id":      "task-1",
		"message": map[string]any{"role": "user", "parts": []any{map[string]any{"text": "你好 A2A"}}},
	})
	if code != http.StatusOK {
		t.Fatalf("A2A message/send 失败: %d %v", code, out)
	}
	if out["error"] != nil {
		t.Fatalf("A2A 返回错误: %v", out["error"])
	}
	// JSON-RPC 的 result 即 A2A Task 对象本身（对齐 A2A 规范，不额外包裹 task 字段）。
	result, ok := out["result"].(map[string]any)
	if !ok {
		t.Fatalf("A2A 响应缺少 result(Task): %v", out)
	}
	if result["id"] != "task-1" {
		t.Fatalf("task.id 应为 task-1: %v", result["id"])
	}
	status, ok := result["status"].(map[string]any)
	if !ok || status["state"] != "completed" {
		t.Fatalf("task 状态应为 completed: %v", result["status"])
	}
	// 断言 agent 回复可见（mock LLM 回声首句：回声首句：你好 A2A）。
	history, ok := result["history"].([]any)
	if !ok || len(history) < 2 {
		t.Fatalf("history 应含 user+agent 两条: %v", result["history"])
	}
	agentMsg := history[1].(map[string]any)
	parts := agentMsg["parts"].([]any)
	reply := parts[0].(map[string]any)["text"].(string)
	if reply == "" {
		t.Fatalf("agent 回复为空: %v", agentMsg)
	}
	t.Logf("✅ A2A message/send 成功：task=%s state=completed reply=%q", result["id"], reply)
}

// TestA2A_MultiRound_Continuity 验证同一 task id 的多次 message/send 复用同一会话（多轮续聊）。
func TestA2A_MultiRound_Continuity(t *testing.T) {
	c, closer := setupA2ABackend(t)
	defer closer.Close()

	// 第一轮。
	code, out := c.a2a("message/send", map[string]any{
		"id":      "task-multi",
		"message": map[string]any{"role": "user", "parts": []any{map[string]any{"text": "首轮问题"}}},
	})
	if code != http.StatusOK || out["error"] != nil {
		t.Fatalf("A2A 第一轮失败: %d %v", code, out)
	}
	// 第二轮（同 task id，应复用会话并产生新回复，状态仍 completed）。
	code, out = c.a2a("message/send", map[string]any{
		"id":      "task-multi",
		"message": map[string]any{"role": "user", "parts": []any{map[string]any{"text": "续轮追问"}}},
	})
	if code != http.StatusOK || out["error"] != nil {
		t.Fatalf("A2A 第二轮失败: %d %v", code, out)
	}
	result, ok := out["result"].(map[string]any)
	if !ok {
		t.Fatalf("A2A 第二轮响应缺少 result: code=%d out=%v", code, out)
	}
	status := result["status"].(map[string]any)
	if status["state"] != "completed" {
		t.Fatalf("第二轮 task 状态应为 completed: %v", status)
	}
	// 多轮续聊验证：第二轮经统一 Gateway 加载了第一轮的历史（会话按 task id 复用），
	// mock LLM 回声「首条 user 消息」——首条仍是第一轮的问题「首轮问题」，故第二轮
	// 回复必含「首轮问题」（而非本轮的「续轮追问」），证明跨轮记忆生效。
	history := result["history"].([]any)
	agentMsg := history[1].(map[string]any)
	reply := agentMsg["parts"].([]any)[0].(map[string]any)["text"].(string)
	if !contains(reply, "首轮问题") {
		t.Fatalf("第二轮回复未携带第一轮上下文（多轮记忆失效）: reply=%q", reply)
	}
	t.Logf("✅ A2A 多轮续聊：同 task id 第二轮 completed，回复携带首轮上下文 reply=%q", reply)
}

// contains 是测试内的简单子串判定。
func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// TestA2A_InvalidRequest 验证 JSON-RPC 协议校验（jsonrpc 字段、缺文本）返回规范错误码。
func TestA2A_InvalidRequest(t *testing.T) {
	c, closer := setupA2ABackend(t)
	defer closer.Close()

	// ① jsonrpc 字段错误 → -32600。
	code, out := c.do("POST", "/api/a2a", map[string]any{
		"jsonrpc": "1.0", "id": "x", "method": "message/send",
		"params": map[string]any{"message": map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hi"}}}},
	})
	if code != http.StatusBadRequest || out["error"] == nil {
		t.Fatalf("jsonrpc 错误应返回 -32600: %d %v", code, out)
	}
	if out["error"].(map[string]any)["code"].(float64) != -32600 {
		t.Fatalf("错误码应为 -32600: %v", out["error"])
	}

	// ② 缺文本 → -32602。
	code, out = c.a2a("message/send", map[string]any{
		"id":      "task-empty",
		"message": map[string]any{"role": "user", "parts": []any{}},
	})
	if code != http.StatusBadRequest || out["error"] == nil {
		t.Fatalf("缺文本应返回 -32602: %d %v", code, out)
	}
	if out["error"].(map[string]any)["code"].(float64) != -32602 {
		t.Fatalf("错误码应为 -32602: %v", out["error"])
	}

	// ③ 不支持的方法 → -32601。
	code, out = c.a2a("tasks/unknown", map[string]any{
		"message": map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hi"}}},
	})
	if code != http.StatusNotFound || out["error"] == nil {
		t.Fatalf("不支持方法应返回 -32601: %d %v", code, out)
	}
	if out["error"].(map[string]any)["code"].(float64) != -32601 {
		t.Fatalf("错误码应为 -32601: %v", out["error"])
	}
	t.Log("✅ A2A JSON-RPC 协议校验：jsonrpc/-32600、缺文本/-32602、方法/-32601 均符合规范")
}
