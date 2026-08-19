// Package smoke 实现「真实模型冒烟测试套件」（M6-06）。
//
// 定位：一套面向生产的冒烟测试，运维可在接入真实 LLM 后直接跑（设置环境变量
// SMOKE_LLM_BASE_URL / SMOKE_LLM_API_KEY 即走真实模型）；在无真实模型的 CI / 沙箱
// 环境下自动回落到内存 Mock OpenAI 服务，保证套件始终可绿，同时完整覆盖三条核心链路：
//
//  1. promptiter 写回不破坏对话：优化把改进指令写回 AgentInstruction 后，生产对话
//     （引擎单代理模式消费 InstructionOverride）必须仍可正常对话，且写回的指令确实作为
//     系统提示词抵达模型；回滚后对话同样不破坏。
//  2. evolution 质量门控不误杀：一条高质量候选必须被放行（不被误杀），同时空泛候选
//     仍被拦截（门控保持有效，而非一味放行）。
//  3. eval 多次跑分数稳定：同一评估集多次同步运行，聚合平均分必须完全一致。
//
// 默认回落 Mock 的关键设计：Mock LLM 的回复取决于「系统提示词是否包含写回标记的指令」，
// 从而在没有真实模型的情况下，真实复现「更好的指令 → 更好的输出」这一生产行为，
// 并顺势验证 InstructionOverride 真的抵达了模型上下文。
package smoke

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/ayanmw/multiagent2/server/internal/eval"
	"github.com/ayanmw/multiagent2/server/internal/engine"
	"github.com/ayanmw/multiagent2/server/internal/evolution"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/promptiter"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// llmStub 是 Mock / 真实 LLM 端点的统一句柄。
// baseURL/apiKey 直接喂给引擎的 openai 客户端；system 捕获最近一次请求的系统提示词
// （真实模型路径下为 nil，跳过捕获断言）。
type llmStub struct {
	baseURL string
	apiKey  string
	system  *string
	stop    func()
}

// startLLM 启动一个 Mock OpenAI 兼容流式端点（或接入真实端点）。
// marker 非空时：系统提示词包含该标记 → 回复 "expected"（模拟「更好指令→更好输出」），
// 否则回复 "wrong"。这样 promptiter 的基线评估失败、候选评估成功，从而走通「接受写回」。
func startLLM(t *testing.T, marker string) *llmStub {
	t.Helper()
	if real := os.Getenv("SMOKE_LLM_BASE_URL"); real != "" {
		return &llmStub{baseURL: real, apiKey: os.Getenv("SMOKE_LLM_API_KEY"), system: nil, stop: func() {}}
	}
	var captured string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		sys := ""
		if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
			var body struct {
				Messages []struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"messages"`
			}
			_ = json.Unmarshal(raw, &body)
			for _, m := range body.Messages {
				if m.Role == "system" {
					sys = m.Content
				}
			}
		}
		captured = sys

		reply := "wrong"
		if marker != "" && strings.Contains(sys, marker) {
			reply = "expected"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		chunks := []string{
			fmt.Sprintf(`data: {"id":"s","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"%s"},"finish_reason":null}]}`, reply),
			`data: {"id":"s","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"content":""},"finish_reason":"stop"}]}`,
			`data: [DONE]`,
		}
		for _, ch := range chunks {
			fmt.Fprintf(w, "%s\n\n", ch)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	return &llmStub{baseURL: srv.URL, apiKey: "test-key", system: &captured, stop: srv.Close}
}

// smokeModelID 返回冒烟测试使用的模型 id（M7.5-02）：
// 读 SMOKE_LLM_MODEL 环境变量——真实网关路径下应设为网关认识的模型 id（如 auto 或
// deepseek-v4-pro，网关把未知 id 当显式模型、失败不回退，故不能用 mock-model）；
// 未设置时回落 "mock-model"（Mock 路径语义不变）。
func smokeModelID() string {
	if v := os.Getenv("SMOKE_LLM_MODEL"); v != "" {
		return v
	}
	return "mock-model"
}

// resolver 返回把任意模型 id 解析到本 Mock/真实端点的 ModelResolver（promptiter 评估
// 经此真正走引擎 → LLM，复现生产评估路径）。
func (st *llmStub) resolver() eval.ModelResolver {
	base := st.baseURL
	key := st.apiKey
	return func(_ context.Context, _ uint, _ string) (engine.ModelConfig, error) {
		return engine.ModelConfig{ModelID: smokeModelID(), BaseURL: base, APIKey: key, Protocol: "openai"}, nil
	}
}

// newSmokeDB 建一个内存 SQLite 并迁移冒烟测试所需的全部表（免 gcc，纯 Go 驱动）。
func newSmokeDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if s, err := db.DB(); err == nil {
		s.SetMaxOpenConns(1)
	}
	if err := db.AutoMigrate(
		&model.EvalDataset{}, &model.EvalCase{}, &model.EvalRun{}, &model.EvalResult{},
		&model.AgentInstruction{}, &model.PromptIterRun{},
	); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	return db
}

// fixedReflector 是 promptiter.Reflector 的桩：直接返回预设的改进指令与理由。
type fixedReflector struct {
	improved  string
	reasoning string
}

func (f *fixedReflector) Reflect(_ context.Context, _ uint, _ string, _ []promptiter.WeakCase) (string, string, error) {
	return f.improved, f.reasoning, nil
}

// stableRunner 是 eval.CaseRunner 的桩：固定返回 output（确定性，用于验证分数稳定）。
type stableRunner struct{ output string }

func (r *stableRunner) RunCase(_ context.Context, _ uint, _ string, _ string) (string, int64, error) {
	return r.output, 10, nil
}

// dummyResolve 是 ModelResolver 的桩（stableRunner 不真正用模型配置）。
func dummyResolve(_ context.Context, _ uint, _ string) (engine.ModelConfig, error) {
	return engine.ModelConfig{ModelID: "m"}, nil
}

// TestSmoke_PromptIterWriteBackDoesNotBreakDialog 覆盖 M6-06 第一项：
// promptiter 的改进指令写回 AgentInstruction 后，生产对话（引擎消费 InstructionOverride）
// 必须仍可正常对话，且写回指令确实抵达模型系统提示词；回滚后对话同样不破坏。
func TestSmoke_PromptIterWriteBackDoesNotBreakDialog(t *testing.T) {
	db := newSmokeDB(t)
	uid := uint(1)
	name := model.DefaultInstructionName
	marker := "OVERRIDE_MARKER_" + strings.ToUpper(strings.ReplaceAll(t.Name(), "/", "_"))

	stub := startLLM(t, marker)
	defer stub.stop()

	// 准备评估集与两条精确匹配用例（期望 "expected"）。
	ds := &model.EvalDataset{UserID: uid, Name: "ds", DefaultGrader: model.GraderExact, DefaultModel: "m"}
	_ = ds.Validate()
	if err := repo.CreateEvalDataset(db, ds); err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	for _, in := range []string{"q1", "q2"} {
		if err := repo.CreateEvalCase(db, &model.EvalCase{DatasetID: ds.ID, Input: in, Expected: "expected", Grader: model.GraderExact}); err != nil {
			t.Fatalf("create case: %v", err)
		}
	}

	// 改进指令包含写回标记；反射器直接返回它（模拟真实模型产出的改进）。
	improved := "你是更专业的编程助手。" + marker + "请先理解需求，再给出可运行代码并解释关键步骤。"
	reflector := &fixedReflector{improved: improved, reasoning: "基线弱项源于指令未要求先理解需求"}
	// 评估经引擎 → Mock LLM：基线（无标记）→ wrong；候选（含标记）→ expected。
	svc := promptiter.NewService(db, stub.resolver(), nil, reflector)

	run, err := svc.Optimize(context.Background(), promptiter.Request{UserID: uid, DatasetID: ds.ID, Threshold: 0.5})
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}
	if run.Status != model.PromptIterStatusAccepted {
		t.Fatalf("期望 accepted（写回应被接受），实际 %s (err=%q)", run.Status, run.Error)
	}

	// 写回后：DB 指令内容应包含标记。
	content, gerr := repo.GetInstructionContent(db, uid, name)
	if gerr != nil {
		t.Fatalf("get instruction content: %v", gerr)
	}
	if !strings.Contains(content, marker) {
		t.Fatalf("写回的指令未包含标记，写回可能失败：%q", content)
	}

	// 生产对话：用写回指令作为 InstructionOverride 建引擎，跑一轮对话。
	eng, eerr := engine.New(engine.ModelConfig{
		ModelID: smokeModelID(), BaseURL: stub.baseURL, APIKey: stub.apiKey,
		Protocol: "openai", InstructionOverride: content,
	})
	if eerr != nil {
		t.Fatalf("new engine with override: %v", eerr)
	}
	defer eng.Close()
	reply, cerr := eng.Chat(context.Background(), "sess-writeback", "写回后还能正常对话吗？", nil)
	if cerr != nil {
		t.Fatalf("写回后对话失败: %v", cerr)
	}
	if reply == "" {
		t.Fatal("写回后对话返回空回复（对话被破坏）")
	}
	if stub.system != nil && !strings.Contains(*stub.system, marker) {
		t.Fatalf("写回指令未作为系统提示词注入对话：system=%q", *stub.system)
	}
	t.Logf("✅ promptiter 写回后对话正常：reply=%q", reply)

	// 回滚后再对话不应破坏（指令恢复为改前空内容，引擎回退内置默认指令）。
	if _, rerr := svc.Rollback(context.Background(), uid, run.ID); rerr != nil {
		t.Fatalf("Rollback: %v", rerr)
	}
	after, _ := repo.GetInstructionContent(db, uid, name) // 改前为空 → ""
	eng2, eerr2 := engine.New(engine.ModelConfig{
		ModelID: smokeModelID(), BaseURL: stub.baseURL, APIKey: stub.apiKey,
		Protocol: "openai", InstructionOverride: after,
	})
	if eerr2 != nil {
		t.Fatalf("new engine after rollback: %v", eerr2)
	}
	defer eng2.Close()
	reply2, cerr2 := eng2.Chat(context.Background(), "sess-rollback", "回滚后还能正常对话吗？", nil)
	if cerr2 != nil {
		t.Fatalf("回滚后对话失败: %v", cerr2)
	}
	if reply2 == "" {
		t.Fatal("回滚后对话返回空回复（对话被破坏）")
	}
	t.Logf("✅ promptiter 回滚后对话正常：reply=%q", reply2)
}

// TestSmoke_EvolutionQualityGateNoFalseKill 覆盖 M6-06 第二项（正向）：
// 一条高质量候选必须被质量门控放行（不被误杀）。
func TestSmoke_EvolutionQualityGateNoFalseKill(t *testing.T) {
	// 真实技能形态：合法名、描述充分、正文结构化（标题+步骤+有序列表）且长度充足、无占位短语。
	goodBody := `# Git 提交规范

## 适用场景
本技能用于规范化 git 提交信息，统一团队提交风格，便于后续回溯与自动化生成变更日志。

## 步骤
1. 确认本次改动范围，避免把不相关的修改混在同一个提交里。
2. 使用约定式提交格式：<type>(<scope>): <subject>。
3. 正文补充「为什么」而不是「改了什么」，帮助审阅者理解动机。
4. 提交前运行相关测试，确保没有引入回归问题影响主干稳定。

## 注意事项
提交信息应保持语言一致，避免中英混杂导致可读性下降，从而影响协作效率与代码评审质量。`
	name := "git-commit-convention"
	desc := "规范化 git 提交信息的约定式提交技能，包含适用场景、步骤与注意事项。"
	res := evolution.QualityGate(name, desc, goodBody)
	if !res.Passed {
		t.Fatalf("高质量候选被误杀（不应被拒）：%v", res.Notes)
	}
	t.Logf("✅ 高质量候选通过质量门控（未被误杀）")
}

// TestSmoke_EvolutionQualityGateRejectsBad 作为对照：空泛/占位候选仍被拦截，
// 证明门控保持有效（而非一味放行），从而「不误杀」是校准后的结论而非门控失效。
func TestSmoke_EvolutionQualityGateRejectsBad(t *testing.T) {
	res := evolution.QualityGate("bad", "todo", "TODO 待补充，这里以后再写具体内容。")
	if res.Passed {
		t.Fatalf("空泛候选不应通过门控，但被放行：%v", res.Notes)
	}
	t.Logf("✅ 空泛候选被正确拦截：%v", res.Notes)
}

// TestSmoke_EvalScoresStableAcrossRuns 覆盖 M6-06 第三项：
// 同一评估集多次同步运行，聚合平均分必须完全一致（分数稳定）。
func TestSmoke_EvalScoresStableAcrossRuns(t *testing.T) {
	db := newSmokeDB(t)
	uid := uint(1)
	ds := &model.EvalDataset{UserID: uid, Name: "ds", DefaultGrader: model.GraderExact, DefaultModel: "m"}
	_ = ds.Validate()
	if err := repo.CreateEvalDataset(db, ds); err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	if err := repo.CreateEvalCase(db, &model.EvalCase{DatasetID: ds.ID, Input: "1+1=?", Expected: "2", Grader: model.GraderExact}); err != nil {
		t.Fatalf("create case: %v", err)
	}

	svc := eval.NewService(db, dummyResolve, &stableRunner{output: "2"}, nil)

	const runs = 5
	scores := make([]float64, 0, runs)
	for i := 0; i < runs; i++ {
		run, _, err := svc.RunSync(context.Background(), uid, ds.ID, "", "", 1)
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if run.Status != model.EvalRunStatusDone {
			t.Fatalf("run %d 状态异常 %s (err=%q)", i, run.Status, run.Error)
		}
		scores = append(scores, run.ScoreAvg)
	}
	first := scores[0]
	for i, s := range scores {
		if s != first {
			t.Fatalf("多次跑分数不稳定：run0=%.4f run%d=%.4f 全量=%v", first, i, s, scores)
		}
	}

	// 额外验证「多次重复采样内」也稳定：单次运行 repeats=20，平均分应与单次一致。
	run, _, err := svc.RunSync(context.Background(), uid, ds.ID, "", "", 20)
	if err != nil {
		t.Fatalf("repeats run: %v", err)
	}
	if run.TotalAttempts != 20 {
		t.Fatalf("期望 20 次尝试，实际 %d", run.TotalAttempts)
	}
	if run.ScoreAvg != first {
		t.Fatalf("重复采样平均分 %.4f 与单次 %.4f 不一致", run.ScoreAvg, first)
	}
	t.Logf("✅ eval 多次跑分数稳定（%d 次均为 %.4f，repeats=20 亦一致）", runs, first)
}
