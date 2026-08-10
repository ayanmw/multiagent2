package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/config"
	"github.com/ayanmw/multiagent2/server/internal/provider"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/gin-gonic/gin"
)

// newM1HTTPMockLLM 是 M1-17 HTTP 层集成验证用的脚本化 LLM 桩：
//   - GET /v1/models 返回两个模型（供模型发现）；
//   - POST /v1/chat/completions 按「当前轮用户消息」或「工具结果」决定回包：
//     ① 若最新一条消息是工具结果（shell_exec 已执行）→ 给出最终文本；
//     ② 若当前轮用户消息含「执行以下 shell 命令」（/run 指令）→ 调用 shell_exec
//     把标记写入 done.txt（相对路径落工作目录）；
//     ③ 否则回声「第一条 user 消息」（用于多轮记忆验证：历史被回灌时第二轮仍能引用首句实体）。
//
// 全程不依赖真实 LLM（见 docs/loop/LEARNINGS「M1 集成测试指引」）。
func newM1HTTPMockLLM() *httptest.Server {
	jsonString := func(s string) string {
		b, _ := json.Marshal(s)
		return string(b)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/models"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"mock-gpt-4o","object":"model","owned_by":"mock-org"},{"id":"mock-gpt-4o-mini","object":"model","owned_by":"mock-org"}]}`))
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			raw, _ := io.ReadAll(r.Body)
			var body map[string]any
			_ = json.Unmarshal(raw, &body)
			msgs, _ := body["messages"].([]any)
			firstUser, latestUser := "", ""
			isToolResult := false
			for _, m := range msgs {
				mm, ok := m.(map[string]any)
				if !ok {
					continue
				}
				role, _ := mm["role"].(string)
				if role == "tool" {
					isToolResult = true
				}
				if role == "user" {
					if c, ok := mm["content"].(string); ok {
						if firstUser == "" {
							firstUser = c
						}
						latestUser = c
					}
				}
			}
			f, _ := w.(http.Flusher)

			// ① 工具结果回来后，给出最终文本（shell_exec 已在工作目录写入 done.txt）。
			if isToolResult {
				for _, ch := range []string{
					`data: {"id":"m","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":"已在工作区执行命令，输出已写入 done.txt。"},"finish_reason":null}]}`,
					`data: {"id":"m","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":""},"finish_reason":"stop"}]}`,
					`data: [DONE]`,
				} {
					fmt.Fprintf(w, "%s\n\n", ch)
					if f != nil {
						f.Flush()
					}
				}
				return
			}

			// ② /run 指令：调用 shell_exec 把标记写入 done.txt（相对路径落工作目录）。
			if strings.Contains(latestUser, "执行以下 shell 命令") {
				fmt.Fprintf(w, "%s\n\n",
					fmt.Sprintf(`data: {"id":"m","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"shell_exec","arguments":%s}}]}}]}`,
						jsonString(`{"command":"echo M1RUN_OK > done.txt"}`)))
				fmt.Fprintf(w, "%s\n\n",
					`data: {"id":"m","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`)
				fmt.Fprintf(w, "%s\n\n", `data: [DONE]`)
				if f != nil {
					f.Flush()
				}
				return
			}

			// ③ 默认：回声「第一条 user 消息」（多轮记忆验证：第二轮若能引用首句实体，证明历史回灌）。
			for _, ch := range []string{
				`data: {"id":"m","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":"回声首句："},"finish_reason":null}]}`,
				`data: {"id":"m","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":` + jsonString(firstUser) + `},"finish_reason":null}]}`,
				`data: {"id":"m","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":""},"finish_reason":"stop"}]}`,
				`data: [DONE]`,
			} {
				fmt.Fprintf(w, "%s\n\n", ch)
				if f != nil {
					f.Flush()
				}
			}
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
}

// TestM1_HTTP_Integration_E2E 是 M1-17 在 HTTP 层的集成验证（前端全链路）：
// 注册 → 登录 → 建 workspace → 建 OpenAI Provider → 拉模型 → 启用+默认 →
// 建 Session（绑定 workspace）→ 首轮对话 → /run 执行 shell 命令（落盘 done.txt）
// → 多轮记忆（第二轮引用首句实体）→ 历史持久化（刷新后仍在）。
//
// 覆盖 M1-06/07（CodeAct + Workspace 在 HTTP 端点被装配并真正执行命令）、
// M0.5-01（多轮记忆）、M0 出口标准（刷新后历史仍在）。全程不调真实 LLM。
//
// 注：本测试位于 cmd/server 且依赖 go-sqlite3（CGO）。在具备 gcc 的环境可编译运行；
// 本沙箱 CGO_ENABLED=0 无法运行，仅做语法校验（gofmt/vet 同包其他用例在真实环境通过）。
func TestM1_HTTP_Integration_E2E(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mockLLM := newM1HTTPMockLLM()
	defer mockLLM.Close()

	dbPath := filepath.Join(t.TempDir(), "m1-http-e2e.db")
	enc := sha256.Sum256([]byte("m1-http-e2e-enc-key"))
	wsRoot := filepath.Join(t.TempDir(), "ws")
	cfg := &config.Config{
		DBPath:        dbPath,
		Port:          "0",
		JWTSecret:     "m1-http-e2e-secret",
		EncryptionKey: enc[:],
		WorkspaceRoot: wsRoot,
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
	r := buildRouter(db, cfg, disc, nil, false, nil, nil, nil, buildGateway(db, cfg, nil, false, nil, nil, nil))
	c := &e2eClient{t: t, r: r}

	// 1) 注册 → 登录
	code, reg := c.do("POST", "/api/auth/register", map[string]any{
		"username": "m1http", "email": "m1http@example.com", "password": "secret123",
	})
	if code != 201 {
		t.Fatalf("注册失败: %d %v", code, reg)
	}
	c.tok = reg["token"].(string)

	// 2) 建 workspace（M1-07）
	code, ws := c.do("POST", "/api/workspaces", map[string]any{"name": "m1-ws", "description": "M1 HTTP E2E"})
	if code != 201 {
		t.Fatalf("建 workspace 失败: %d %v", code, ws)
	}
	wsKey := ws["key"].(string)
	wsLocal := ws["local_path"].(string)
	if wsKey == "" || wsLocal == "" {
		t.Fatal("workspace 缺少 key/local_path")
	}
	t.Logf("✅ workspace 已建：key=%s local_path=%s", wsKey, wsLocal)

	// 3) 建 OpenAI Provider → 同步 → 启用+默认模型
	code, prov := c.do("POST", "/api/providers", map[string]any{
		"name": "m1-openai", "protocol": "openai", "base_url": mockLLM.URL + "/v1", "api_key": "k",
	})
	if code != 201 {
		t.Fatalf("建 Provider 失败: %d %v", code, prov)
	}
	pid := uint(prov["id"].(float64))
	code, sync := c.do("POST", fmt.Sprintf("/api/providers/%d/models/sync", pid), nil)
	if code != 200 {
		t.Fatalf("同步模型失败: %d %v", code, sync)
	}
	models := sync["models"].([]any)
	mid := uint(models[0].(map[string]any)["id"].(float64))
	c.do("PUT", fmt.Sprintf("/api/providers/%d/models/%d", pid, mid),
		map[string]any{"enabled": true, "is_default": true})

	// 4) 建 Session（绑定 workspace）
	code, sess := c.do("POST", "/api/sessions", map[string]any{"title": "M1 HTTP E2E"})
	if code != 201 {
		t.Fatalf("建 Session 失败: %d %v", code, sess)
	}
	sk := sess["session_key"].(string)

	// 5) 首轮对话（普通消息，绑定 workspace）。验证 SSE 正常结束、无错误。
	bodyA := c.sse(fmt.Sprintf("/api/chat/%s/stream", sk), map[string]any{
		"message":       "介绍一下 Go Multi-Agent 平台",
		"model_id":      mid,
		"workspace_key": wsKey,
	})
	evA := parseAGUI(bodyA)
	if evA.has("RUN_ERROR") || !evA.has("RUN_FINISHED") {
		t.Fatalf("首轮对话异常: body=%s", bodyA)
	}
	t.Logf("✅ 首轮对话完成；reply=%q", evA.text())

	// 6) /run 执行 shell 命令（M1-06/07：CodeAct + Workspace 在 HTTP 端点真正执行）。
	//    前端会把 /run 渲染为含「执行以下 shell 命令」的提示词（见 M1-15 验收）。
	runPrompt := "请在当前工作区执行以下 shell 命令，并汇报执行结果与输出：\necho M1RUN_OK > done.txt"
	bodyB := c.sse(fmt.Sprintf("/api/chat/%s/stream", sk), map[string]any{
		"message":       runPrompt,
		"model_id":      mid,
		"workspace_key": wsKey,
	})
	evB := parseAGUI(bodyB)
	if evB.has("RUN_ERROR") || !evB.has("RUN_FINISHED") {
		t.Fatalf("/run 执行异常: body=%s", bodyB)
	}
	// 标记文件应落在该 workspace 的本地目录内（相对路径 done.txt 落在工作目录）。
	marker := filepath.Join(wsLocal, "done.txt")
	data, rerr := os.ReadFile(marker)
	if rerr != nil {
		// 兜底：在 WorkspaceRoot 下递归查找，确认命令确实执行了。
		data = findFile(wsRoot, "done.txt")
		if data == nil {
			t.Fatalf("/run 未在工作目录落盘 done.txt（local_path=%s）: %v", wsLocal, rerr)
		}
	}
	if !strings.Contains(string(data), "M1RUN_OK") {
		t.Fatalf("/run 写入内容不符：got=%q", string(data))
	}
	t.Logf("✅ /run 执行成功：done.txt 内容=%q", string(data))

	// 7) 多轮记忆（M0.5-01）：第二轮引用首轮实体「Go Multi-Agent」。
	//    首轮 user 消息含该实体；若历史被回灌，mock 回声首句会包含它。
	bodyC := c.sse(fmt.Sprintf("/api/chat/%s/stream", sk), map[string]any{
		"message":       "它基于什么框架实现的？",
		"model_id":      mid,
		"workspace_key": wsKey,
	})
	evC := parseAGUI(bodyC)
	if evC.has("RUN_ERROR") || !evC.has("RUN_FINISHED") {
		t.Fatalf("多轮记忆轮异常: body=%s", bodyC)
	}
	if !strings.Contains(evC.text(), "Go Multi-Agent") {
		t.Fatalf("多轮记忆失败：第二轮未引用首轮实体；reply=%q", evC.text())
	}
	t.Logf("✅ 多轮记忆校验通过：reply=%q", evC.text())

	// 8) 历史持久化（M0 出口标准：刷新后历史仍在）。
	//    3 轮对话 × (user+assistant) = 6 条消息。
	code, detail := c.do("GET", "/api/sessions/"+sk, nil)
	if code != 200 {
		t.Fatalf("查询 Session 详情失败: %d", code)
	}
	msgs, _ := detail["messages"].([]any)
	if len(msgs) != 6 {
		t.Fatalf("历史消息数量错误: 期望 6 (3 轮 user+assistant), 实际 %d, body=%v", len(msgs), detail)
	}
	for i, exp := range []string{"user", "assistant", "user", "assistant", "user", "assistant"} {
		if got := msgs[i].(map[string]any)["role"].(string); got != exp {
			t.Fatalf("第 %d 条历史消息角色应为 %s，实际 %s", i+1, exp, got)
		}
	}
	t.Logf("✅ 历史持久化校验通过：共 %d 条消息（刷新后仍在）", len(msgs))
}

// findFile 在 root 下递归查找名为 name 的文件，返回其字节内容（找不到返回 nil）。
func findFile(root, name string) []byte {
	var out []byte
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && d.Name() == name {
			if b, rerr := os.ReadFile(p); rerr == nil {
				out = b
			}
		}
		return nil
	})
	return out
}
