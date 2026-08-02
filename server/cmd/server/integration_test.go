package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/auth"
	"github.com/ayanmw/multiagent2/server/internal/config"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/provider"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/gin-gonic/gin"
)

// createUserDirect 直接在 DB 落库一个指定角色的用户，返回其自增 ID（用于 SessionKey 复合唯一场景）。
func createUserDirect(t *testing.T, db *repo.DB, username string, role string) uint {
	t.Helper()
	r, err := repo.GetRoleByName(db.DB, role)
	if err != nil {
		t.Fatalf("GetRoleByName(%s): %v", role, err)
	}
	hash, err := auth.HashPassword("secret123")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	u := &model.User{
		Username:     username,
		Email:        username + "@example.com",
		PasswordHash: hash,
		DisplayName:  username,
		RoleID:       r.ID,
		Status:       model.UserStatusActive,
	}
	if err := repo.CreateUser(db.DB, u); err != nil {
		t.Fatalf("CreateUser(%s): %v", username, err)
	}
	return u.ID
}

// e2eClient 是一个极简的进程内 HTTP 客户端，直接把请求喂给 Gin 引擎，
// 避免「跨命令文件系统隔离」导致 DB 写入不可见的问题（见 LEARNINGS）。
type e2eClient struct {
	t   *testing.T
	r   *gin.Engine
	tok string
}

func (c *e2eClient) do(method, path string, body any) (int, map[string]any) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			c.t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.tok != "" {
		req.Header.Set("Authorization", "Bearer "+c.tok)
	}
	rec := httptest.NewRecorder()
	c.r.ServeHTTP(rec, req)

	var out map[string]any
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
	}
	return rec.Code, out
}

// sse 发送一个 POST 请求（M0.5-06：message 改由 body 传递）并取回完整的 SSE 响应体。
func (c *e2eClient) sse(path string, body any) string {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			c.t.Fatalf("marshal sse body: %v", err)
		}
		reader = bytes.NewReader(b)
	}
	req := httptest.NewRequest("POST", path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.tok != "" {
		req.Header.Set("Authorization", "Bearer "+c.tok)
	}
	rec := httptest.NewRecorder()
	c.r.ServeHTTP(rec, req)
	return rec.Body.String()
}

// newMockLLM 是一个 OpenAI 兼容的本地桩服务：
//   - GET /v1/models 返回两个模型（供 M0-08 模型发现）
//   - POST /v1/chat/completions 把「请求中第一条 user 消息内容」回声进回复，
//     用于验证多轮记忆（M0.5-01）：若历史被回灌，第二轮回复会引用第一轮实体。
func newMockLLM() *httptest.Server {
	// jsonString 把字符串安全地序列化为 JSON 字符串字面量（含转义）。
	jsonString := func(s string) string {
		b, _ := json.Marshal(s)
		return string(b)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/models"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[` +
				`{"id":"mock-gpt-4o","object":"model","owned_by":"mock-org"},` +
				`{"id":"mock-gpt-4o-mini","object":"model","owned_by":"mock-org"}]}`))
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			// 解析请求体，取第一条 user 消息内容作为回声前缀。
			firstUser := ""
			var body map[string]any
			if raw, rerr := io.ReadAll(r.Body); rerr == nil && len(raw) > 0 {
				_ = json.Unmarshal(raw, &body)
			}
			if msgs, ok := body["messages"].([]any); ok {
				for _, m := range msgs {
					if mm, ok := m.(map[string]any); ok && mm["role"] == "user" {
						if c, ok := mm["content"].(string); ok {
							firstUser = c
							break
						}
					}
				}
			}
			chunks := []string{
				`data: {"id":"mock","object":"chat.completion.chunk","created":1699200000,"model":"mock-gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"回声首句："},"finish_reason":null}]}`,
				`data: {"id":"mock","object":"chat.completion.chunk","created":1699200000,"model":"mock-gpt-4o","choices":[{"index":0,"delta":{"content":` + jsonString(firstUser) + `},"finish_reason":null}]}`,
				`data: {"id":"mock","object":"chat.completion.chunk","created":1699200000,"model":"mock-gpt-4o","choices":[{"index":0,"delta":{"content":""},"finish_reason":"stop"}]}`,
				`data: [DONE]`,
			}
			for _, ch := range chunks {
				fmt.Fprintf(w, "%s\n\n", ch)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
}

// aguiEvents 收集解析后的 AG-UI SSE 事件，便于断言。
type aguiEvents struct {
	types  []string
	texts  []string
	errors []string
}

func (a *aguiEvents) has(t string) bool {
	for _, x := range a.types {
		if x == t {
			return true
		}
	}
	return false
}

func (a *aguiEvents) text() string {
	var sb strings.Builder
	for _, x := range a.texts {
		sb.WriteString(x)
	}
	return sb.String()
}

// parseAGUI 把 SSE 响应体解析成 AG-UI 事件序列。
func parseAGUI(body string) *aguiEvents {
	ev := &aguiEvents{}
	for _, frame := range strings.Split(body, "\n\n") {
		frame = strings.TrimSpace(frame)
		if !strings.HasPrefix(frame, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(frame, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(payload), &obj); err != nil {
			continue
		}
		typ, _ := obj["type"].(string)
		if typ == "" {
			continue
		}
		ev.types = append(ev.types, typ)
		switch typ {
		case "RUN_ERROR":
			if m, ok := obj["message"].(string); ok {
				ev.errors = append(ev.errors, m)
			}
		case "TEXT_MESSAGE_CONTENT":
			if d, ok := obj["delta"].(string); ok {
				ev.texts = append(ev.texts, d)
			}
		}
	}
	return ev
}

// TestM0_Integration_E2E 端到端验证 M0 全链路：
// 注册 → 登录 → 建 OpenAI Provider → 拉模型 → 启用模型 → 建 Session
// → SSE 流式对话 → 历史持久化（刷新后仍在）。
func TestM0_Integration_E2E(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mockLLM := newMockLLM()
	defer mockLLM.Close()

	// 临时 SQLite 文件，避免污染 data/codeagent.db。
	dbPath := filepath.Join(t.TempDir(), "m0-e2e.db")
	enc := sha256.Sum256([]byte("m0-e2e-enc-key"))
	cfg := &config.Config{
		DBPath:        dbPath,
		Port:          "0",
		JWTSecret:     "m0-e2e-secret",
		EncryptionKey: enc[:],
		WorkspaceRoot: filepath.Join(t.TempDir(), "ws"),
	}
	db, err := repo.NewDB(cfg)
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	// 测试结束关闭连接，释放 SQLite 文件锁，便于 t.TempDir 清理。
	defer func() {
		if sqlDB, e := db.DB.DB(); e == nil {
			_ = sqlDB.Close()
		}
	}()
	disc := provider.NewDiscoverer(cfg.EncryptionKey, time.Minute)
	r := buildRouter(db, cfg, disc)

	c := &e2eClient{t: t, r: r}

	// 1) 注册
	code, reg := c.do("POST", "/api/auth/register", map[string]any{
		"username":     "alice",
		"email":        "alice@example.com",
		"password":     "secret123",
		"display_name": "Alice",
	})
	if code != 201 {
		t.Fatalf("注册失败: 期望 201, 实际 %d, body=%v", code, reg)
	}
	tok, _ := reg["token"].(string)
	if tok == "" {
		t.Fatal("注册响应缺少 token")
	}
	c.tok = tok

	// 2) 登录（用 account 字段，可为用户名或邮箱）
	code, login := c.do("POST", "/api/auth/login", map[string]any{
		"account":  "alice",
		"password": "secret123",
	})
	if code != 200 {
		t.Fatalf("登录失败: 期望 200, 实际 %d, body=%v", code, login)
	}
	if _, ok := login["token"].(string); !ok {
		t.Fatal("登录响应缺少 token")
	}

	// 3) 创建 OpenAI Provider（指向本地 mock）
	code, prov := c.do("POST", "/api/providers", map[string]any{
		"name":        "mock-openai",
		"protocol":    "openai",
		"base_url":    mockLLM.URL + "/v1",
		"api_key":     "test-key",
		"description": "M0 集成验证用 mock 后端",
	})
	if code != 201 {
		t.Fatalf("创建 Provider 失败: 期望 201, 实际 %d, body=%v", code, prov)
	}
	pid := uint(prov["id"].(float64))
	if !prov["has_api_key"].(bool) {
		t.Fatal("Provider 应返回 has_api_key=true")
	}

	// 4) 同步模型列表（M0-08）
	code, sync := c.do("POST", fmt.Sprintf("/api/providers/%d/models/sync", pid), nil)
	if code != 200 {
		t.Fatalf("同步模型失败: 期望 200, 实际 %d, body=%v", code, sync)
	}
	models, _ := sync["models"].([]any)
	if len(models) != 2 {
		t.Fatalf("同步模型数量错误: 期望 2, 实际 %d, body=%v", len(models), sync)
	}
	if sync["cached"].(bool) {
		t.Fatal("首次同步不应命中缓存")
	}

	// 5) 启用 + 设为默认模型（M0-09）
	first := models[0].(map[string]any)
	mid := uint(first["id"].(float64))
	code, upd := c.do("PUT", fmt.Sprintf("/api/providers/%d/models/%d", pid, mid),
		map[string]any{"enabled": true, "is_default": true})
	if code != 200 {
		t.Fatalf("启用模型失败: 期望 200, 实际 %d, body=%v", code, upd)
	}
	if !upd["enabled"].(bool) || !upd["is_default"].(bool) {
		t.Fatal("模型应被启用且设为默认")
	}

	// 6) 已启用模型列表只应含这一个（M0-10 Agent 选模型池）
	code, en := c.do("GET", "/api/models", nil)
	if code != 200 {
		t.Fatalf("查询已启用模型失败: 期望 200, 实际 %d", code)
	}
	emodels, _ := en["models"].([]any)
	if len(emodels) != 1 {
		t.Fatalf("已启用模型数量错误: 期望 1, 实际 %d, body=%v", len(emodels), en)
	}

	// 7) 新建 Session（M0-12）
	code, sess := c.do("POST", "/api/sessions", map[string]any{"title": "M0 集成验证对话"})
	if code != 201 {
		t.Fatalf("创建 Session 失败: 期望 201, 实际 %d, body=%v", code, sess)
	}
	sk := sess["session_key"].(string)
	if sk == "" {
		t.Fatal("Session 缺少 session_key")
	}

	// 8) SSE 流式对话（M0-11 出口标准：收到流式回复；M0.5-06 改 POST body）
	body := c.sse(fmt.Sprintf("/api/chat/%s/stream", sk), map[string]any{
		"message":  "你好，介绍一下 Go Multi-Agent",
		"model_id": mid,
	})
	events := parseAGUI(body)
	if !events.has("RUN_STARTED") {
		t.Fatalf("SSE 缺少 RUN_STARTED; body=%s", body)
	}
	if events.has("RUN_ERROR") {
		t.Fatalf("SSE 出现 RUN_ERROR: %v; body=%s", events.errors, body)
	}
	reply := events.text()
	if reply == "" {
		t.Fatalf("SSE 流式回复为空; body=%s", body)
	}
	if !events.has("RUN_FINISHED") {
		t.Fatalf("SSE 缺少 RUN_FINISHED; body=%s", body)
	}
	t.Logf("✅ SSE 流式回复: %q", reply)

	// 8.1) 回归校验（M0.5-06）：SSE 端点仅接受 POST，GET 必须被拒（405），
	//      message 不再出现在 query 中（避免明文进入访问日志）。
	getReq := httptest.NewRequest("GET", fmt.Sprintf("/api/chat/%s/stream?message=x", sk), nil)
	getReq.Header.Set("Authorization", "Bearer "+c.tok)
	getRec := httptest.NewRecorder()
	r.ServeHTTP(getRec, getReq)
	if getRec.Code == http.StatusOK {
		t.Fatalf("SSE 端点不应接受 GET 请求（M0.5-06 要求 message 改 POST body）; code=%d body=%s",
			getRec.Code, getRec.Body.String())
	}
	t.Logf("✅ SSE 端点 GET 已被拒（code=%d），message 不再走 query", getRec.Code)

	// 9) 历史持久化：模拟「刷新页面后再拉详情」（M0 出口标准：刷新后历史仍在）
	code, detail := c.do("GET", "/api/sessions/"+sk, nil)
	if code != 200 {
		t.Fatalf("查询 Session 详情失败: 期望 200, 实际 %d", code)
	}
	msgs, _ := detail["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("历史消息数量错误: 期望 2 (user+assistant), 实际 %d, body=%v", len(msgs), detail)
	}
	if msgs[0].(map[string]any)["role"] != "user" {
		t.Fatal("首条历史消息应为 user")
	}
	if msgs[1].(map[string]any)["role"] != "assistant" {
		t.Fatal("第二条历史消息应为 assistant")
	}
	t.Logf("✅ 历史持久化校验通过：user=%q, assistant=%q",
		msgs[0].(map[string]any)["content"], msgs[1].(map[string]any)["content"])

	// 10) 多轮记忆验证（M0.5-01）：同一会话内追问，模型应引用第一轮提到的实体。
	//     第一轮 user 消息含「Go Multi-Agent」；若历史被回灌，第二轮回声首句会包含它。
	body2 := c.sse(fmt.Sprintf("/api/chat/%s/stream", sk), map[string]any{
		"message":  "那它基于什么框架实现的？",
		"model_id": mid,
	})
	events2 := parseAGUI(body2)
	if events2.has("RUN_ERROR") {
		t.Fatalf("SSE 第二轮出现 RUN_ERROR: %v; body=%s", events2.errors, body2)
	}
	if !events2.has("RUN_FINISHED") {
		t.Fatalf("SSE 第二轮缺少 RUN_FINISHED; body=%s", body2)
	}
	reply2 := events2.text()
	if !strings.Contains(reply2, "Go Multi-Agent") {
		t.Fatalf("多轮记忆失败：第二轮回复未引用第一轮实体『Go Multi-Agent』; body=%s", body2)
	}
	t.Logf("✅ 多轮记忆校验通过：第二轮回复引用了第一轮实体: %q", reply2)
}

// TestM0_5_Regression 是 M0.5 阶段的整合回归用例：在单一测试里同时验证三个已修复缺陷
// 在最新代码下仍生效，作为 M0.5 结项证据（多轮记忆 M0.5-01 / RBAC 403 M0.5-02 / SessionKey 复合唯一 M0.5-03）。
// 场景 A、B 走 HTTP 全链路；场景 C 走 repo 层直接验证复合唯一索引（CreateSessionHandler 自动生成 key，HTTP 无法指定）。
func TestM0_5_Regression(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mockLLM := newMockLLM()
	defer mockLLM.Close()

	dbPath := filepath.Join(t.TempDir(), "m0_5_regression.db")
	enc := sha256.Sum256([]byte("m0_5_regression-enc-key"))
	cfg := &config.Config{
		DBPath:        dbPath,
		Port:          "0",
		JWTSecret:     "m0_5_regression-secret",
		EncryptionKey: enc[:],
		WorkspaceRoot: filepath.Join(t.TempDir(), "ws"),
	}
	db, err := repo.NewDB(cfg)
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer func() {
		if sqlDB, e := db.DB.DB(); e == nil {
			_ = sqlDB.Close()
		}
	}()
	disc := provider.NewDiscoverer(cfg.EncryptionKey, time.Minute)
	r := buildRouter(db, cfg, disc)

	// ===== 场景 A：多轮记忆（M0.5-01）=====
	// developer 注册并跑通「建 Provider → 启用模型 → 两轮对话引用同一实体」全链路。
	dev := &e2eClient{t: t, r: r}
	code, reg := dev.do("POST", "/api/auth/register", map[string]any{
		"username": "m05dev", "email": "m05dev@example.com", "password": "secret123",
	})
	if code != 201 {
		t.Fatalf("A 注册失败: %d %v", code, reg)
	}
	dev.tok = reg["token"].(string)

	code, prov := dev.do("POST", "/api/providers", map[string]any{
		"name": "m05-openai", "protocol": "openai", "base_url": mockLLM.URL + "/v1", "api_key": "k",
	})
	if code != 201 {
		t.Fatalf("A 建 Provider 失败: %d %v", code, prov)
	}
	pid := uint(prov["id"].(float64))

	code, sync := dev.do("POST", fmt.Sprintf("/api/providers/%d/models/sync", pid), nil)
	if code != 200 {
		t.Fatalf("A sync 失败: %d %v", code, sync)
	}
	models := sync["models"].([]any)
	mid := uint(models[0].(map[string]any)["id"].(float64))
	dev.do("PUT", fmt.Sprintf("/api/providers/%d/models/%d", pid, mid),
		map[string]any{"enabled": true, "is_default": true})

	code, sess := dev.do("POST", "/api/sessions", map[string]any{"title": "M0.5 记忆回归"})
	if code != 201 {
		t.Fatalf("A 建 Session 失败: %d %v", code, sess)
	}
	sk := sess["session_key"].(string)

	// 第一轮：user 消息含实体「Go Multi-Agent」
	body1 := dev.sse(fmt.Sprintf("/api/chat/%s/stream", sk), map[string]any{
		"message":  "用一句话介绍 Go Multi-Agent",
		"model_id": mid,
	})
	e1 := parseAGUI(body1)
	if e1.has("RUN_ERROR") || !e1.has("RUN_FINISHED") {
		t.Fatalf("A 第一轮对话异常: body=%s", body1)
	}
	// 第二轮：若历史被回灌给模型，mock 回声首句会包含第一轮实体
	body2 := dev.sse(fmt.Sprintf("/api/chat/%s/stream", sk), map[string]any{
		"message":  "它基于什么框架实现？",
		"model_id": mid,
	})
	e2 := parseAGUI(body2)
	if e2.has("RUN_ERROR") || !e2.has("RUN_FINISHED") {
		t.Fatalf("A 第二轮对话异常: body=%s", body2)
	}
	if !strings.Contains(e2.text(), "Go Multi-Agent") {
		t.Fatalf("A 多轮记忆回归失败：第二轮未引用第一轮实体; body=%s", body2)
	}
	t.Logf("✅ 场景A 多轮记忆：第二轮回复=%q", e2.text())

	// ===== 场景 B：RBAC 403（M0.5-02）=====
	// 造一个 viewer 角色用户（默认 developer 不含受限场景），其写路由应一律 403。
	viewerTok := createViewerToken(t, db, cfg.JWTSecret)
	v := &e2eClient{t: t, r: r, tok: viewerTok}
	writeRoutes := []struct {
		method, path string
	}{
		{"POST", "/api/providers"},
		{"PUT", "/api/providers/999"},
		{"DELETE", "/api/providers/999"},
		{"POST", "/api/providers/999/models/sync"},
		{"PUT", "/api/providers/999/models/999"},
		{"POST", "/api/auth/apikeys"},
		{"GET", "/api/auth/apikeys"},
		{"DELETE", "/api/auth/apikeys/999"},
		{"DELETE", "/api/sessions/whatever"},
	}
	for _, rt := range writeRoutes {
		code, _ := v.do(rt.method, rt.path, map[string]any{"name": "x"})
		if code != http.StatusForbidden {
			t.Errorf("B viewer %s %s 应 403, 实际 %d", rt.method, rt.path, code)
		}
	}
	// viewer 读路由应放行（证明不是「全拒」，而是按权限矩阵区分）。
	if code, _ := v.do("GET", "/api/providers", nil); code != http.StatusOK {
		t.Errorf("B viewer GET /api/providers 应 200, 实际 %d", code)
	}
	t.Logf("✅ 场景B RBAC：viewer 9 条写路由均 403，读路由放行")

	// ===== 场景 C：SessionKey 复合唯一（M0.5-03）=====
	// 两个真实用户用同一 session_key，应通过复合唯一索引各自落一行（不冲突、不越权）；
	// 同一用户重复 key 复用同一行。
	u1 := createUserDirect(t, db, "m05u1", model.RoleDeveloper)
	u2 := createUserDirect(t, db, "m05u2", model.RoleDeveloper)
	const dupKey = "dup-key-m05"
	s1, err := repo.GetOrCreateSession(db.DB, u1, dupKey)
	if err != nil {
		t.Fatalf("C GetOrCreateSession(u1) 失败: %v", err)
	}
	s2, err := repo.GetOrCreateSession(db.DB, u2, dupKey)
	if err != nil {
		t.Fatalf("C GetOrCreateSession(u2) 失败: %v", err)
	}
	if s1.ID == s2.ID {
		t.Fatalf("C 跨用户复用同一 session_key 竟落同一行（复合唯一索引失效）")
	}
	if s1.SessionKey != dupKey || s2.SessionKey != dupKey {
		t.Fatalf("C session_key 未被保留: s1=%q s2=%q", s1.SessionKey, s2.SessionKey)
	}
	// 同用户同 key → 复用同一行（不产生脏数据）。
	s1b, err := repo.GetOrCreateSession(db.DB, u1, dupKey)
	if err != nil {
		t.Fatalf("C GetOrCreateSession(u1) 再次失败: %v", err)
	}
	if s1b.ID != s1.ID {
		t.Fatalf("C 同用户同 key 应复用同一行: 首次 id=%d 复用 id=%d", s1.ID, s1b.ID)
	}
	t.Logf("✅ 场景C SessionKey 复合唯一：跨用户同 key 落 2 行(id=%d/%d)，同用户复用同行(id=%d)",
		s1.ID, s2.ID, s1b.ID)
}
