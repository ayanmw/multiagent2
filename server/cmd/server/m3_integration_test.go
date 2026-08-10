package main

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/artifact"
	"github.com/ayanmw/multiagent2/server/internal/config"
	"github.com/ayanmw/multiagent2/server/internal/metrics"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/provider"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/gin-gonic/gin"
)

// TestM3_Enterprise_E2E 是 M3（企业化）在 HTTP 层的集成验证：
// 注册 → 登录 → 建 workspace → 建 Provider/模型 → 建会话（绑 workspace）
// → 执行命令（审计可见）→ 超预算暂停 → 人工检查点审批 → artifact 浏览 → 指标可见
// 全链路。覆盖 M3-01（审计）/M3-03（用量）/M3-04（预算护栏）/M3-05（检查点）
// /M3-06（artifact 浏览器）/M3-09（可观测性）在真实路由与中间件下的协同。
//
// 全程不调真实 LLM：命令执行由脚本化 mock（newM1HTTPMockLLM）驱动 tool_call，
// 检查点审批经 HTTP 端点实际执行命令，验证 human-in-the-loop 闭环。
//
// 本测试位于 cmd/server，依赖 repo.NewDB（glebarez/sqlite 纯 Go 驱动，无 CGO），
// 故在本沙箱 CGO_ENABLED=0 下亦可编译运行（与 M0/M1 集成测试同源基础设施）。
func TestM3_Enterprise_E2E(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 可观测性（M3-09）：生产环境由 main() 调用 metrics.Init；测试路由复用 buildRouter，
	// 需手动初始化，使 /metrics 端点可用（未初始化时返回 404）。
	if err := metrics.Init(metrics.Config{Enabled: true}); err != nil {
		t.Fatalf("metrics.Init: %v", err)
	}
	defer func() { _ = metrics.Init(metrics.Config{Enabled: false}) }()

	mockLLM := newM1HTTPMockLLM()
	defer mockLLM.Close()

	dbPath := filepath.Join(t.TempDir(), "m3-e2e.db")
	enc := sha256.Sum256([]byte("m3-e2e-enc-key"))
	wsRoot := filepath.Join(t.TempDir(), "ws")
	cfg := &config.Config{
		DBPath:        dbPath,
		Port:          "0",
		JWTSecret:     "m3-e2e-secret",
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
	// 启用状态外置（M1-16），使 artifact 浏览器端点返回真实内容（enabled=true）。
	stateStore := artifact.NewMemoryStore()
	r := buildRouter(db, cfg, disc, stateStore, true, nil, nil, nil, buildGateway(db, cfg, stateStore, true, nil, nil, nil))
	c := &e2eClient{t: t, r: r}

	// 1) 注册 → 登录（默认 developer 角色，具备 audit/usage/budget/checkpoints 权限）。
	code, reg := c.do("POST", "/api/auth/register", map[string]any{
		"username": "m3dev", "email": "m3dev@example.com", "password": "secret123",
	})
	if code != http.StatusCreated {
		t.Fatalf("注册失败: %d %v", code, reg)
	}
	c.tok = reg["token"].(string)
	uid := uint(reg["user"].(map[string]any)["id"].(float64))
	t.Logf("✅ 注册登录完成 uid=%d", uid)

	// 2) 建 workspace（M1-07）。
	code, ws := c.do("POST", "/api/workspaces", map[string]any{"name": "m3-ws", "description": "M3 E2E"})
	if code != http.StatusCreated {
		t.Fatalf("建 workspace 失败: %d %v", code, ws)
	}
	wsKey := ws["key"].(string)
	wsLocal := ws["local_path"].(string)
	t.Logf("✅ workspace 已建 key=%s local=%s", wsKey, wsLocal)

	// 3) 建 OpenAI Provider → 同步模型 → 启用+默认。
	code, prov := c.do("POST", "/api/providers", map[string]any{
		"name": "m3-openai", "protocol": "openai", "base_url": mockLLM.URL + "/v1", "api_key": "k",
	})
	if code != http.StatusCreated {
		t.Fatalf("建 Provider 失败: %d %v", code, prov)
	}
	pid := uint(prov["id"].(float64))
	code, sync := c.do("POST", fmt.Sprintf("/api/providers/%d/models/sync", pid), nil)
	if code != http.StatusOK {
		t.Fatalf("同步模型失败: %d %v", code, sync)
	}
	models := sync["models"].([]any)
	mid := uint(models[0].(map[string]any)["id"].(float64))
	c.do("PUT", fmt.Sprintf("/api/providers/%d/models/%d", pid, mid),
		map[string]any{"enabled": true, "is_default": true})
	t.Logf("✅ Provider/模型就绪 mid=%d", mid)

	// 4) 建 Session（绑定 workspace）。
	code, sess := c.do("POST", "/api/sessions", map[string]any{"title": "M3 企业化 E2E"})
	if code != http.StatusCreated {
		t.Fatalf("建 Session 失败: %d %v", code, sess)
	}
	sk := sess["session_key"].(string)

	// 5) 执行命令 → 审计可见（M3-01）。
	runPrompt := "请在当前工作区执行以下 shell 命令，并汇报执行结果与输出：\necho M1RUN_OK > done.txt"
	bodyA := c.sse(fmt.Sprintf("/api/chat/%s/stream", sk), map[string]any{
		"message": runPrompt, "model_id": mid, "workspace_key": wsKey,
	})
	evA := parseAGUI(bodyA)
	if evA.has("RUN_ERROR") || !evA.has("RUN_FINISHED") {
		t.Fatalf("执行命令异常: body=%s", bodyA)
	}
	data := findFile(wsRoot, "done.txt")
	if data == nil || !strings.Contains(string(data), "M1RUN_OK") {
		t.Fatalf("命令未在工作目录落盘 done.txt；data=%q", string(data))
	}
	t.Logf("✅ 命令执行落盘 done.txt")

	// 审计日志应记录该命令的 allow 决策。
	code, aud := c.do("GET", "/api/audit", nil)
	if code != http.StatusOK {
		t.Fatalf("查询审计失败: %d %v", code, aud)
	}
	logs := aud["audit_logs"].([]any)
	auditHit := false
	for _, l := range logs {
		e := l.(map[string]any)
		if cmd, _ := e["command"].(string); strings.Contains(cmd, "echo M1RUN_OK") && e["decision"] == "allow" {
			auditHit = true
		}
	}
	if !auditHit {
		t.Fatalf("审计未记录命令执行（共 %d 条）: %v", len(logs), logs)
	}
	t.Logf("✅ 审计可见：命令执行已落库（共 %d 条审计）", len(logs))

	// 6) 超预算暂停（M3-04）：读取已用 token，设置上限=已用量 → 下一轮对话被拦截。
	code, usg := c.do("GET", "/api/usage", nil)
	if code != http.StatusOK {
		t.Fatalf("查询用量失败: %d %v", code, usg)
	}
	used := int64(0)
	if tot, ok := usg["totals"].(map[string]any); ok {
		if tt, ok := tot["total_tokens"].(float64); ok {
			used = int64(tt)
		}
	}
	if used <= 0 {
		t.Fatalf("用量未被记录（M3-03 计量缺失）: used=%d", used)
	}
	maxTok := used // Used >= Max 即触发拦截，确定性不依赖具体数值。
	code, bput := c.do("PUT", "/api/budgets", map[string]any{
		"scope": "user", "scope_key": "", "max_tokens": maxTok,
	})
	if code != http.StatusOK {
		t.Fatalf("设置预算失败: %d %v", code, bput)
	}
	t.Logf("✅ 预算护栏已设：max=%d（已用 %d）", maxTok, used)

	bodyB := c.sse(fmt.Sprintf("/api/chat/%s/stream", sk), map[string]any{
		"message": "普通消息", "model_id": mid, "workspace_key": wsKey,
	})
	evB := parseAGUI(bodyB)
	if !evB.has("RUN_ERROR") {
		t.Fatalf("预算未拦截对话: body=%s", bodyB)
	}
	budgetHit := false
	for _, e := range evB.errors {
		if strings.Contains(e, "预算耗尽") {
			budgetHit = true
		}
	}
	if !budgetHit {
		t.Fatalf("预算拦截消息不符: %v", evB.errors)
	}
	t.Logf("✅ 超预算暂停生效（对话被拦截，写预算审计）")

	// 7) 人工检查点审批（M3-05 human-in-the-loop）：
	//    直接落库一条 pending 检查点，再经 HTTP 端点 approve → 实际执行命令并写结果。
	cp := &model.Checkpoint{
		SessionID: sk,
		UserID:    uid,
		Command:   "echo CP_OK > cp.txt",
		Workdir:   wsLocal,
		Reason:    "命中 ask 危险策略（测试）",
		Status:    model.CheckpointPending,
		Context:   "M3 E2E",
	}
	if err := repo.CreateCheckpoint(db.DB, cp); err != nil {
		t.Fatalf("落库检查点失败: %v", err)
	}
	// 列表可见（developer 看全员）。
	code, cpList := c.do("GET", "/api/checkpoints", nil)
	if code != http.StatusOK {
		t.Fatalf("查询检查点失败: %d %v", code, cpList)
	}
	if _, ok := cpList["checkpoints"].([]any); !ok {
		t.Fatalf("检查点列表响应异常: %v", cpList)
	}
	// approve → 实际执行命令。
	code, res := c.do("POST", fmt.Sprintf("/api/checkpoints/%d/resolve", cp.ID),
		map[string]any{"action": "approve", "comment": "auto-test"})
	if code != http.StatusOK {
		t.Fatalf("审批检查点失败: %d %v", code, res)
	}
	if res["status"] != model.CheckpointApproved {
		t.Fatalf("检查点状态应为 approved: %v", res)
	}
	cpData := findFile(wsRoot, "cp.txt")
	if cpData == nil || !strings.Contains(string(cpData), "CP_OK") {
		t.Fatalf("检查点审批后命令未执行: data=%q", string(cpData))
	}
	// 审批执行写入允许审计。
	code, aud2 := c.do("GET", "/api/audit", nil)
	logs2 := aud2["audit_logs"].([]any)
	cpAudit := false
	for _, l := range logs2 {
		e := l.(map[string]any)
		if cmd, _ := e["command"].(string); strings.Contains(cmd, "echo CP_OK") && e["decision"] == "allow" {
			cpAudit = true
		}
	}
	if !cpAudit {
		t.Fatalf("检查点执行未审计（共 %d 条）: %v", len(logs2), logs2)
	}
	t.Logf("✅ 人工检查点闭环：approve 后命令执行并审计")

	// 8) Artifact 浏览器（M3-06）：直接写入一条 artifact → 列表/查看可见。
	if err := stateStore.Write("sess:"+sk, "PLAN.md", "# M3 PLAN\n- 验证 artifact 浏览器\n"); err != nil {
		t.Fatalf("写入 artifact 失败: %v", err)
	}
	code, art := c.do("GET", "/api/sessions/"+sk+"/artifacts", nil)
	if code != http.StatusOK {
		t.Fatalf("查询 artifact 列表失败: %d %v", code, art)
	}
	if !art["enabled"].(bool) {
		t.Fatalf("artifact 未启用（enableState 未生效）: %v", art)
	}
	arts := art["artifacts"].([]any)
	if len(arts) == 0 {
		t.Fatalf("artifact 列表为空")
	}
	gotPlan := false
	for _, a := range arts {
		if a.(map[string]any)["name"].(string) == "PLAN.md" {
			gotPlan = true
		}
	}
	if !gotPlan {
		t.Fatalf("PLAN.md 未出现在 artifact 列表: %v", arts)
	}
	code, content := c.do("GET", "/api/sessions/"+sk+"/artifacts/PLAN.md", nil)
	if code != http.StatusOK {
		t.Fatalf("查看 artifact 失败: %d %v", code, content)
	}
	if cStr, _ := content["content"].(string); !strings.Contains(cStr, "验证 artifact 浏览器") {
		t.Fatalf("artifact 内容不符: %v", content)
	}
	t.Logf("✅ Artifact 浏览器：列表 %d 项，PLAN.md 可读", len(arts))

	// 9) 指标可见（M3-09）：概览 JSON + Prometheus /metrics 文本。
	code, mon := c.do("GET", "/api/monitoring/overview", nil)
	if code != http.StatusOK {
		t.Fatalf("运行监控概览失败: %d %v", code, mon)
	}
	if _, ok := mon["llm_calls"].(float64); !ok {
		t.Fatalf("监控概览缺少 llm_calls 字段: %v", mon)
	}
	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics 失败: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "codeagent_llm_calls_total") {
		t.Fatalf("/metrics 缺少指标导出: %s", rec.Body.String())
	}
	t.Logf("✅ 指标可见：概览 JSON + /metrics Prometheus 文本")

	t.Log("🎉 M3 企业化全链路 E2E 通过：登录→执行命令→审计可见→超预算暂停→检查点审批→artifact 浏览→指标可见")
}
