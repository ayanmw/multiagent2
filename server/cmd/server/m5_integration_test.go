package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/api"
	"github.com/ayanmw/multiagent2/server/internal/artifact"
	"github.com/ayanmw/multiagent2/server/internal/config"
	"github.com/ayanmw/multiagent2/server/internal/engine"
	"github.com/ayanmw/multiagent2/server/internal/evolution"
	"github.com/ayanmw/multiagent2/server/internal/eval"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/promptiter"
	"github.com/ayanmw/multiagent2/server/internal/provider"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/gin-gonic/gin"
)

// TestM5_Evolution_E2E 是 M5（进化）在 HTTP 层的集成验证，覆盖
// 「CLI 对话 → RAG 检索 → evolution 提取并审批技能 → evaluation 回归 → promptiter 优化」
// 全链路（M5-01..08 协同）：
//
//   - ① CLI 对话：复用与 CLI 完全相同的 REST+SSE 端点（CLI 的 chat 子命令即 POST 此 SSE），
//     经 mock LLM 跑通「建会话 → 发消息 → 历史落库」，为 evolution 扫描提供 transcript。
//   - ② RAG 检索（M5-02）：建知识库 → 索引文档 → 检索命中（knowledge 包本地 sqlite 向量，无 LLM）。
//   - ③ 技能进化飞轮（M5-03/04）：触发扫描（注入 mock Extractor，绕开真实 LLM 提取）→
//     生成 pending 候选 → approve → 发布进共享技能库（落盘 SKILL.md）。
//   - ④ 评估回归（M5-05）：建评估集 + 用例 → 运行（注入 mock Runner/Judge，确定性评分）→
//     出分数报告与逐条结果。
//   - ⑤ GEPA 反射式优化（M5-06）：针对评估集触发一次优化（mock Reflector 产出改进指令，
//     基线/候选评分经引擎走 mock LLM）→ 运行到达终态（accepted/no_improvement）。
//
// 全程不依赖真实 LLM 语义：evolution 提取 / eval 评分 / promptiter 反射均注入 mock 实现，
// 仅「CLI 对话」与「promptiter 基线/候选评估」两条路径经引擎打到 mock LLM（回声，确定性）。
//
// 本测试位于 cmd/server，依赖 repo.NewDB（glebarez/sqlite 纯 Go 驱动，无 CGO），
// 故在 CGO_ENABLED=0 沙箱下亦可编译运行（与 M0/M1/M3/M4 集成测试同源基础设施）。
func TestM5_Evolution_E2E(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// mock LLM（回声）：CLI 对话与 promptiter 基线/候选评估均经引擎打到它。
	mockLLM := newM1HTTPMockLLM()
	defer mockLLM.Close()

	// 技能发布根目录（approve 落盘 SKILL.md 处），必须指向临时目录——
	// 经 config.Load 读取 SKILLS_ROOT env 落到 cfg.SkillsRoot()，再传入路由。
	skillsRoot := t.TempDir()
	skillsData := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "m5-e2e.db")
	enc := sha256.Sum256([]byte("m5-e2e-enc-key"))
	wsRoot := filepath.Join(t.TempDir(), "ws")
	os.Setenv("SKILLS_ROOT", skillsRoot)
	os.Setenv("SKILLS_DATA_DIR", skillsData)
	os.Setenv("DB_PATH", dbPath)
	defer os.Unsetenv("SKILLS_ROOT")
	defer os.Unsetenv("SKILLS_DATA_DIR")
	defer os.Unsetenv("DB_PATH")

	cfg := config.Load()
	cfg.DBPath = dbPath
	cfg.Port = "0"
	cfg.JWTSecret = "m5-e2e-secret"
	cfg.EncryptionKey = enc[:]
	cfg.WorkspaceRoot = wsRoot

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
	stateStore := artifact.NewMemoryStore()

	// —— 注入进化里程碑服务（mock 实现绕开真实 LLM，保证确定性）——
	mockExtractor := &m5MockExtractor{cand: &evolution.RawCandidate{
		Name:        "go_refactor",
		Description: "从会话 transcript 提炼的可复用 Go 重构技能，覆盖常见坏味道识别与修复步骤。",
		// 正文需 > 质量门控 MinBodyLen(200 rune) 且含结构（#/步骤），确定性过门。
		Body: "# Go 重构技能\n\n" +
			"## 适用场景\n当代码出现重复逻辑、过长函数、上帝类或散落的相似实现时，应触发本技能进行结构化重构，降低维护成本并提升可读性。\n\n" +
			"## 步骤\n" +
			"1. 识别坏味道：扫描重复代码、过长函数、神类、过长参数列表等常见坏味道并定位影响范围。\n" +
			"2. 抽取公共函数为独立方法，命名以意图而非实现为准，并补充对应的单元测试覆盖。\n" +
			"3. 运行测试套件确认行为保持不变，使用 `go test ./...` 作为回归闸门。\n" +
			"4. 评估改动影响面，必要时引入小步提交并复核调用方是否仍可编译通过。\n\n" +
			"## 注意事项\n重构前后必须保证测试通过，禁止引入新依赖；优先小步快跑，每次改动都应有可验证的测试支撑，避免一次性大规模重写带来回归风险。",
	}}
	api.SetEvolutionService(evolution.NewService(db.DB, mockExtractor))

	mockRunner := &m5MockRunner{}
	mockJudge := &m5MockJudge{score: 1.0}
	api.SetEvalService(eval.NewService(db.DB, nil, mockRunner, mockJudge))

	// promptiter 的基线/候选评估经引擎打 mock LLM，故 resolve 指向 mock 服务器。
	piResolver := func(_ context.Context, _ uint, _ string) (engine.ModelConfig, error) {
		return engine.ModelConfig{
			ModelID:  "mock",
			BaseURL:  mockLLM.URL + "/v1",
			APIKey:   "k",
			Protocol: "openai",
			Timeout:  30 * time.Second,
		}, nil
	}
	mockReflector := &m5MockReflector{improved: "你是一名资深 Go 工程师。优先抽取重复逻辑为函数，并在每次改动后运行 `go test ./...` 确认无回归。"}
	api.SetPromptIterService(promptiter.NewService(db.DB, piResolver, mockJudge, mockReflector))
	// 不挂回归检查器：approve 走 M5-04 直接发布语义（M5-08 联动由 regression_test 单独覆盖）。
	api.SetRegressionChecker(nil)
	defer func() {
		api.SetEvolutionService(nil)
		api.SetEvalService(nil)
		api.SetPromptIterService(nil)
		api.SetRegressionChecker(nil)
	}()

	gw := buildGateway(db, cfg, stateStore, true, nil, nil, nil)
	r := buildRouter(db, cfg, disc, stateStore, true, nil, nil, nil, gw)
	c := &e2eClient{t: t, r: r}

	// —— ① CLI 对话（经 SSE 端点，与 CLI chat 子命令同一数据通路）——
	code, reg := c.do("POST", "/api/auth/register", map[string]any{
		"username": "m5dev", "email": "m5dev@example.com", "password": "secret123",
	})
	if code != http.StatusCreated {
		t.Fatalf("注册失败: %d %v", code, reg)
	}
	c.tok = reg["token"].(string)
	uid := uint(reg["user"].(map[string]any)["id"].(float64))

	code, prov := c.do("POST", "/api/providers", map[string]any{
		"name": "m5-openai", "protocol": "openai", "base_url": mockLLM.URL + "/v1", "api_key": "k",
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

	code, sess := c.do("POST", "/api/sessions", map[string]any{"title": "M5 进化 E2E"})
	if code != http.StatusCreated {
		t.Fatalf("建会话失败: %d %v", code, sess)
	}
	sk := sess["session_key"].(string)
	// 两轮对话 → 会话落 4 条消息（>= evolution 最小消息数 4），为扫描提供 transcript。
	for i := 0; i < 2; i++ {
		body := c.sse(fmt.Sprintf("/api/chat/%s/stream", sk), map[string]any{
			"message": "请帮我重构这段 Go 代码，提取重复逻辑。", "model_id": mid,
		})
		ev := parseAGUI(body)
		if ev.has("RUN_ERROR") || !ev.has("RUN_FINISHED") {
			t.Fatalf("对话异常: body=%s", body)
		}
	}
	msgs, merr := repo.ListSessionMessages(db.DB, uint(sess["id"].(float64)))
	if merr != nil || len(msgs) < 4 {
		t.Fatalf("对话历史未落库足够消息: n=%d err=%v", len(msgs), merr)
	}
	t.Logf("✅ CLI 对话：会话 %s 落库 %d 条消息（为进化扫描提供 transcript）", sk, len(msgs))

	// —— ② RAG 检索（M5-02）——
	code, kb := c.do("POST", "/api/knowledge", map[string]any{"name": "go-kb", "description": "Go 知识库"})
	if code != http.StatusOK {
		t.Fatalf("建知识库失败: %d %v", code, kb)
	}
	kbID := uint(kb["id"].(float64))
	code, idx := c.do("POST", fmt.Sprintf("/api/knowledge/%d/documents", kbID), map[string]any{
		"name": "goroutine.md", "content": "Go 使用 goroutine 实现并发，channel 用于 goroutine 间通信，select 处理多路复用。",
		"content_type": "text",
	})
	if code != http.StatusOK || int(idx["indexed_chunks"].(float64)) <= 0 {
		t.Fatalf("索引文档失败: %d %v", code, idx)
	}
	code, sr := c.do("POST", fmt.Sprintf("/api/knowledge/%d/search", kbID), map[string]any{
		"query": "goroutine 并发", "top_k": 3,
	})
	if code != http.StatusOK || int(sr["total"].(float64)) == 0 {
		t.Fatalf("RAG 检索未命中: %d %v", code, sr)
	}
	t.Logf("✅ RAG 检索：知识库 %d 索引并检索命中 %v 条", kbID, sr["total"])

	// —— ③ 技能进化飞轮：扫描 → 候选 → 审批发布（M5-03/04）——
	code, scan := c.do("POST", "/api/skill-candidates/scan", nil)
	if code != http.StatusOK {
		t.Fatalf("扫描失败: %d %v", code, scan)
	}
	if int(scan["created"].(float64)) < 1 {
		t.Fatalf("扫描应至少创建 1 条候选，实际: %v", scan)
	}
	code, cl := c.do("GET", "/api/skill-candidates", nil)
	if code != http.StatusOK {
		t.Fatalf("列候选失败: %d %v", code, cl)
	}
	cands := cl["skill_candidates"].([]any)
	if len(cands) == 0 {
		t.Fatalf("候选列表为空")
	}
	candID := uint(cands[0].(map[string]any)["id"].(float64))
	code, res := c.do("POST", fmt.Sprintf("/api/skill-candidates/%d/resolve", candID),
		map[string]any{"decision": "approve"})
	if code != http.StatusOK {
		t.Fatalf("审批候选失败: %d %v", code, res)
	}
	if res["status"] != string(model.SkillCandidateApproved) {
		t.Fatalf("审批后应 approved，实际 %v", res["status"])
	}
	// 共享技能库应落盘 SKILL.md。
	published := filepath.Join(skillsRoot, "go_refactor", "SKILL.md")
	if data, rerr := os.ReadFile(published); rerr != nil || len(data) == 0 {
		t.Fatalf("审批后共享技能库未落盘: %v", rerr)
	}
	t.Logf("✅ 进化飞轮：扫描生成候选 → approve → 发布共享技能库 %s", published)

	// —— ④ 评估回归（M5-05）——
	code, ds := c.do("POST", "/api/eval/datasets", map[string]any{
		"name": "refactor-ds", "description": "重构质量评估集", "default_grader": "contains",
	})
	if code != http.StatusOK {
		t.Fatalf("建评估集失败: %d %v", code, ds)
	}
	dsID := uint(ds["id"].(float64))
	// 用例期望不在输入内 → 基线评分 0（weak），驱动 promptiter 走到反射-接受路径。
	code, cs := c.do("POST", fmt.Sprintf("/api/eval/datasets/%d/cases", dsID), map[string]any{
		"input": "请重构这段代码", "expected": "the refactored result is 42",
	})
	if code != http.StatusOK {
		t.Fatalf("建用例失败: %d %v", code, cs)
	}
	code, run := c.do("POST", fmt.Sprintf("/api/eval/datasets/%d/run", dsID),
		map[string]any{"model": "", "grader": "", "repeats": 1})
	if code != http.StatusOK {
		t.Fatalf("运行评估失败: %d %v", code, run)
	}
	runID := uint(run["id"].(float64))
	waitEvalDone(t, db, uid, runID, 10*time.Second)
	code, results := c.do("GET", fmt.Sprintf("/api/eval/runs/%d/results", runID), nil)
	if code != http.StatusOK {
		t.Fatalf("查评估结果失败: %d %v", code, results)
	}
	if int(results["total"].(float64)) < 1 {
		t.Fatalf("评估结果不应为空: %v", results)
	}
	t.Logf("✅ 评估回归：评估集 %d 运行 %d 产出 %v 条结果", dsID, runID, results["total"])

	// —— ⑤ GEPA 反射式优化（M5-06）——
	code, opt := c.do("POST", "/api/promptiter/optimize", map[string]any{
		"dataset_id": dsID, "instruction_name": "default", "role": "single", "repeats": 1,
	})
	if code != http.StatusAccepted {
		t.Fatalf("触发优化应 202，实际 %d %v", code, opt)
	}
	optID := uint(opt["id"].(float64))
	waitPromptIterDone(t, db, uid, optID, 30*time.Second)
	if mockReflector.calls < 1 {
		t.Fatalf("优化应触发反射（弱项→改进指令），但 Reflector 未被调用")
	}
	code, optRun := c.do("GET", fmt.Sprintf("/api/promptiter/runs/%d", optID), nil)
	if code != http.StatusOK {
		t.Fatalf("查优化运行失败: %d %v", code, optRun)
	}
	optStatus := optRun["status"].(string)
	if optStatus == "running" || optStatus == "failed" {
		t.Fatalf("优化运行应以终态结束，实际 %s (err=%v)", optStatus, optRun["error"])
	}
	t.Logf("✅ GEPA 优化：运行 %d 到达终态 %s（反射被调用 %d 次，基线 %.2f / 候选 %.2f）",
		optID, optStatus, mockReflector.calls, optRun["baseline_score"], optRun["candidate_score"])

	t.Log("🎉 M5 进化全链路 E2E 通过：CLI对话→RAG检索→进化扫描审批发布→评估回归→GEPA优化")
}

// waitEvalDone 轮询直到指定评估运行到达终态（done/failed）。
func waitEvalDone(t *testing.T, db *repo.DB, uid, runID uint, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		run, err := repo.GetEvalRun(db.DB, uid, runID)
		if err == nil && run.Status != model.EvalRunStatusRunning {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待评估运行 %d 完成超时", runID)
		}
		time.Sleep(30 * time.Millisecond)
	}
}

// waitPromptIterDone 轮询直到指定优化运行到达终态。
func waitPromptIterDone(t *testing.T, db *repo.DB, uid, runID uint, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		run, err := repo.GetPromptIterRun(db.DB, uid, runID)
		if err == nil && run.Status != model.PromptIterStatusRunning {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待优化运行 %d 完成超时", runID)
		}
		time.Sleep(30 * time.Millisecond)
	}
}

// ---- M5 E2E 专用 mock 实现（与 evolution/eval/promptiter 接口一致，绕开真实 LLM）----

// m5MockExtractor 返回预设候选（已满足质量门控），用于确定性扫描。
type m5MockExtractor struct {
	cand  *evolution.RawCandidate
	calls int
}

func (m *m5MockExtractor) Extract(_ context.Context, _ uint, _ string) (*evolution.RawCandidate, error) {
	m.calls++
	return m.cand, nil
}

// m5MockRunner 不调模型，直接返回期望文本（使 contains 评分器稳定得 1.0）。
type m5MockRunner struct{ calls int }

func (m *m5MockRunner) RunCase(_ context.Context, _ uint, _ string, input string) (string, int64, error) {
	m.calls++
	return input, 1, nil
}

// m5MockJudge 恒给满分（llm 评分器用；本 E2E 用 contains 评分器不依赖它，但保留实现）。
type m5MockJudge struct{ score float64 }

func (m *m5MockJudge) Judge(_ context.Context, _ uint, _, _, _ string) (float64, error) {
	return m.score, nil
}

// m5MockReflector 返回预设改进指令，验证 GEPA 反射步被触发。
type m5MockReflector struct {
	improved  string
	reasoning string
	calls     int
}

func (m *m5MockReflector) Reflect(_ context.Context, _ uint, _ string, _ []promptiter.WeakCase) (string, string, error) {
	m.calls++
	return m.improved, m.reasoning, nil
}
