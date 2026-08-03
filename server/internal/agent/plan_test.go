package codeagent

import (
	"context"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/model"

	planpkg "github.com/ayanmw/multiagent2/server/internal/plan"
)

// ptrPlanStatus 构造步骤状态的指针（供 UpdateStep patch 使用）。
func ptrPlanStatus(s planpkg.StepStatus) *planpkg.StepStatus { return &s }

// ptrStr 构造字符串指针（供 patch 的 Note 字段使用）。
func ptrStr(v string) *string { return &v }

func TestPlanEnforcer_NameAndTools(t *testing.T) {
	e := NewPlanEnforcer()
	if e.Name() != PlanExtensionName {
		t.Fatalf("扩展名不符: %q", e.Name())
	}
	want := map[string]bool{ToolCreatePlan: false, ToolGetPlan: false, ToolUpdateStep: false, ToolAddSteps: false}
	tools := e.Tools()
	if len(tools) != 4 {
		t.Fatalf("应当贡献 4 个计划工具，got %d", len(tools))
	}
	for _, tl := range tools {
		decl := tl.Declaration()
		if decl == nil {
			t.Fatal("工具缺少 Declaration")
		}
		if _, ok := want[decl.Name]; !ok {
			t.Fatalf("出现未预期的工具 %q", decl.Name)
		}
		want[decl.Name] = true
		if decl.Description == "" {
			t.Fatalf("工具 %q 缺少描述", decl.Name)
		}
	}
	for n, seen := range want {
		if !seen {
			t.Fatalf("缺少工具 %q", n)
		}
	}
	if e.Store() == nil {
		t.Fatal("Store 不应为 nil")
	}
}

func TestPlanTools_Roundtrip(t *testing.T) {
	store := planpkg.NewStore(0)
	e := NewPlanEnforcer(WithPlanStore(store))
	tools := e.Tools()
	ctx, _ := newGoalTestCtx("p1")

	// 未建计划时 get_plan 返回 ok=false 并提示先建计划（而非抛错中断 Agent）。
	got := callTool(t, ctx, tools, ToolGetPlan, `{}`)
	if got["ok"] != false {
		t.Fatalf("未建计划时 get_plan.ok 应为 false: %+v", got)
	}
	if !strings.Contains(got["hint"].(string), ToolCreatePlan) {
		t.Fatalf("get_plan 应提示调用 create_plan: %+v", got)
	}

	// 未建计划时 update_step 必须报错（避免模型跳过建计划环节）。
	err := callToolExpectErr(t, ctx, tools, ToolUpdateStep, `{"step_id":"s1","status":"done"}`)
	if !strings.Contains(err.Error(), ToolCreatePlan) {
		t.Fatalf("update_step 的错误应引导调用 create_plan: %v", err)
	}

	// 建立计划（2 步）。
	created := callTool(t, ctx, tools, ToolCreatePlan,
		`{"title":"完成任务X","steps":[{"title":"写文件A","detail":"细节"},{"title":"写文件B"}]}`)
	if created["ok"] != true {
		t.Fatalf("create_plan 失败: %+v", created)
	}
	if !store.IsOpen("sess:p1") {
		t.Fatal("create_plan 之后作用域 sess:p1 应当存在未收敛计划")
	}

	// get_plan 应能回读（含 counts / next_step）。
	got2 := callTool(t, ctx, tools, ToolGetPlan, `{}`)
	if got2["counts"] == nil || got2["next_step"] == nil {
		t.Fatalf("get_plan 应返回 counts 与 next_step: %+v", got2)
	}

	// 更新步骤：in_progress。
	if upd := callTool(t, ctx, tools, ToolUpdateStep, `{"step_id":"s1","status":"in_progress"}`); upd["ok"] != true {
		t.Fatalf("update_step(in_progress) 失败: %+v", upd)
	}
	// 完成 step1 需带 note。
	done1 := callTool(t, ctx, tools, ToolUpdateStep, `{"step_id":"1","status":"done","note":"已写A"}`)
	if done1["ok"] != true {
		t.Fatalf("update_step(done) 失败: %+v", done1)
	}

	// skipped 缺理由必须被拒绝。
	if err := callToolExpectErr(t, ctx, tools, ToolUpdateStep, `{"step_id":"s2","status":"skipped"}`); err == nil {
		t.Fatal("skipped 缺少理由应被拒绝")
	}
	skip := callTool(t, ctx, tools, ToolUpdateStep, `{"step_id":"s2","status":"skipped","note":"无需"}`)
	if skip["ok"] != true {
		t.Fatalf("update_step(skipped) 失败: %+v", skip)
	}
	if store.IsOpen("sess:p1") {
		t.Fatal("全部步骤收敛后不应再视为未收敛")
	}
}

func TestPlanTools_AddSteps(t *testing.T) {
	store := planpkg.NewStore(0)
	e := NewPlanEnforcer(WithPlanStore(store))
	tools := e.Tools()
	ctx, _ := newGoalTestCtx("p3")
	callTool(t, ctx, tools, ToolCreatePlan, `{"title":"t","steps":[{"title":"a"}]}`)
	added := callTool(t, ctx, tools, ToolAddSteps, `{"steps":[{"title":"b"},{"title":"c"}]}`)
	if added["ok"] != true {
		t.Fatalf("add_steps 失败: %+v", added)
	}
	p, err := store.Get("sess:p3")
	if err != nil {
		t.Fatalf("store.Get 失败: %v", err)
	}
	if len(p.Steps) != 3 {
		t.Fatalf("add_steps 后应有 3 步, got %d", len(p.Steps))
	}
}

func TestPlanEnforcer_BlocksPrematureFinal(t *testing.T) {
	store := planpkg.NewStore(0)
	e := NewPlanEnforcer(WithPlanStore(store), WithPlanMaxNudges(5))
	ctx, _ := newGoalTestCtx("p2")

	// 1) 未建计划（requirePlan=false 默认）→ 放行（一句话能答完的不强制建计划）。
	res, err := e.afterModel(ctx, &model.AfterModelArgs{Response: finalResponse("我做完了")})
	if err != nil {
		t.Fatalf("afterModel 报错: %v", err)
	}
	if res != nil {
		t.Fatal("未建计划（requirePlan=false）时的最终答复必须放行")
	}

	// 2) 建计划后未收敛 → 拦截。
	if _, cerr := store.Create("sess:p2", "t", []planpkg.StepSpec{{Title: "a"}}); cerr != nil {
		t.Fatalf("Create 失败: %v", cerr)
	}
	res, _ = e.afterModel(ctx, &model.AfterModelArgs{Response: finalResponse("这次真的做完了")})
	if res == nil || res.CustomResponse == nil {
		t.Fatal("计划未收敛时的最终答复必须被拦截")
	}
	if res.CustomResponse.Done {
		t.Fatal("拦截响应的 Done 必须为 false，否则 llmflow 会退出循环")
	}
	if len(res.CustomResponse.Choices) != 0 {
		t.Fatal("拦截响应必须清空 Choices，避免把过早答复泄漏给前端")
	}

	// 3) 计划收敛（全部 done）→ 放行。
	if _, uerr := store.UpdateStep("sess:p2", "s1", planpkg.StepPatch{Status: ptrPlanStatus(planpkg.StepDone), Note: ptrStr("x")}); uerr != nil {
		t.Fatalf("UpdateStep 失败: %v", uerr)
	}
	res, _ = e.afterModel(ctx, &model.AfterModelArgs{Response: finalResponse("计划达成")})
	if res != nil {
		t.Fatal("计划已收敛时必须放行最终答复")
	}
}

func TestPlanEnforcer_PassesThroughNonFinalResponses(t *testing.T) {
	e := NewPlanEnforcer() // requirePlan=false：只要响应不是最终答复就必须放行
	ctx, _ := newGoalTestCtx("p3b")

	cases := map[string]*model.Response{
		"nil 响应":  nil,
		"流式分片":    {Done: false, IsPartial: true, Choices: []model.Choice{{Delta: model.Message{Content: "半截"}}}},
		"未完成":     {Done: false, Choices: []model.Choice{{Message: model.Message{Content: "x"}}}},
		"错误响应":    {Done: true, Error: &model.ResponseError{Message: "boom"}},
		"无内容 done": {Done: true},
		"工具调用": {Done: true, Choices: []model.Choice{{
			Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{
				ID: "c1", Type: "function", Function: model.FunctionDefinitionParam{Name: "coder", Arguments: []byte(`{}`)},
			}}},
		}},
	}}
	for name, rsp := range cases {
		res, err := e.afterModel(ctx, &model.AfterModelArgs{Response: rsp})
		if err != nil {
			t.Fatalf("[%s] afterModel 报错: %v", name, err)
		}
		if res != nil {
			t.Fatalf("[%s] 非最终响应不应被拦截", name)
		}
	}
	if res, _ := e.afterModel(ctx, &model.AfterModelArgs{
		Response: finalResponse("x"), Error: context.Canceled,
	}); res != nil {
		t.Fatal("模型调用出错时不应拦截")
	}
}

func TestPlanEnforcer_FailOpenAfterBudget(t *testing.T) {
	e := NewPlanEnforcer(WithPlanMaxNudges(2))
	store := planpkg.NewStore(0)
	if _, err := store.Create("sess:fb", "t", []planpkg.StepSpec{{Title: "a"}}); err != nil {
		t.Fatalf("Create 失败: %v", err)
	}
	// 把同一个 store 注入 enforcer（AfterModel 读 store，需要一致）。
	e.store = store
	ctx, _ := newGoalTestCtx("fb")

	for i := 1; i <= 2; i++ {
		res, _ := e.afterModel(ctx, &model.AfterModelArgs{Response: finalResponse("收工")})
		if res == nil {
			t.Fatalf("第 %d 次应当被拦截（预算 2）", i)
		}
	}
	res, _ := e.afterModel(ctx, &model.AfterModelArgs{Response: finalResponse("收工")})
	if res != nil {
		t.Fatal("拦截预算耗尽后必须 fail-open 放行，否则会把 Runner 卡死")
	}
}

func TestPlanEnforcer_RequirePlanEnabled(t *testing.T) {
	e := NewPlanEnforcer(WithRequirePlan(true))
	ctx, _ := newGoalTestCtx("p5")
	if res, _ := e.afterModel(ctx, &model.AfterModelArgs{Response: finalResponse("直接收工")}); res == nil {
		t.Fatal("开启 requirePlan 后，未建计划也应拦截")
	}
}

func TestPlanEnforcer_BeforeModel(t *testing.T) {
	store := planpkg.NewStore(0)
	e := NewPlanEnforcer(WithPlanStore(store), WithPlanMaxNudges(3))
	ctx, _ := newGoalTestCtx("p6")

	// 已收敛：不关流、不注入。
	if _, cerr := store.Create("sess:p6", "t", []planpkg.StepSpec{{Title: "a"}}); cerr != nil {
		t.Fatalf("Create 失败: %v", cerr)
	}
	done := planpkg.StepDone
	if _, uerr := store.UpdateStep("sess:p6", "s1", planpkg.StepPatch{Status: &done, Note: ptrStr("x")}); uerr != nil {
		t.Fatalf("UpdateStep 失败: %v", uerr)
	}
	req := &model.Request{}
	req.GenerationConfig.Stream = true
	if _, err := e.beforeModel(ctx, &model.BeforeModelArgs{Request: req}); err != nil {
		t.Fatalf("beforeModel 报错: %v", err)
	}
	if !req.GenerationConfig.Stream {
		t.Fatal("计划已收敛时不应关闭流式（最终答复要能逐字输出）")
	}
	if len(req.Messages) != 0 {
		t.Fatal("计划已收敛时不应注入催办消息")
	}

	// 未收敛：关流；未拦截过不注入。
	pend := planpkg.StepPending
	if _, uerr := store.UpdateStep("sess:p6", "s1", planpkg.StepPatch{Status: &pend}); uerr != nil {
		t.Fatalf("UpdateStep 失败: %v", uerr)
	}
	req = &model.Request{}
	req.GenerationConfig.Stream = true
	_, _ = e.beforeModel(ctx, &model.BeforeModelArgs{Request: req})
	if req.GenerationConfig.Stream {
		t.Fatal("计划未收敛时必须关闭流式，否则过早答复会先于拦截决策抵达前端")
	}
	if len(req.Messages) != 0 {
		t.Fatal("未发生拦截时不应注入催办消息")
	}

	// 发生拦截后：下一轮注入催办，且只注入一次。
	if res, _ := e.afterModel(ctx, &model.AfterModelArgs{Response: finalResponse("提前收工")}); res == nil {
		t.Fatal("计划未收敛时应当被拦截")
	}
	req = &model.Request{}
	req.GenerationConfig.Stream = true
	_, _ = e.beforeModel(ctx, &model.BeforeModelArgs{Request: req})
	if len(req.Messages) != 1 {
		t.Fatalf("拦截后应注入 1 条催办消息，got %d", len(req.Messages))
	}
	nudge := req.Messages[0].Content
	for _, want := range []string{"计划执行", "已被拦截", ToolUpdateStep} {
		if !strings.Contains(nudge, want) {
			t.Fatalf("催办文案缺少 %q:\n%s", want, nudge)
		}
	}
	req2 := &model.Request{}
	_, _ = e.beforeModel(ctx, &model.BeforeModelArgs{Request: req2})
	if len(req2.Messages) != 0 {
		t.Fatal("催办标记必须被消费，不能每轮重复注入")
	}
}

func TestTeamInstruction_PlanSection(t *testing.T) {
	base := teamInstruction(TeamConfig{EnableSubAgents: true}.normalized())
	if strings.Contains(base, ToolCreatePlan) {
		t.Fatal("未开启 Plan 时不应注入 Plan 规程")
	}
	withPlan := teamInstruction(TeamConfig{EnableSubAgents: true, EnablePlan: true}.normalized())
	for _, want := range []string{ToolCreatePlan, ToolGetPlan, ToolUpdateStep, ToolAddSteps} {
		if !strings.Contains(withPlan, want) {
			t.Fatalf("Plan 规程缺少 %q", want)
		}
	}
	// 同时开启 Reviewer/Goal 时三段规程都要在。
	both := teamInstruction(TeamConfig{EnableSubAgents: true, EnablePlan: true, EnableGoal: true, EnableReviewer: true}.normalized())
	if !strings.Contains(both, ToolCreatePlan) || !strings.Contains(both, ToolCreateGoal) || !strings.Contains(both, RoleReviewer) {
		t.Fatal("Plan + Goal + Reviewer 规程应当共存于 Orchestrator 指令")
	}
	// 单代理模式（未开子代理）下 Plan 不生效——契约只装 Orchestrator。
	if (TeamConfig{EnablePlan: true}).planEnabled() {
		t.Fatal("未开启子代理模式时 Plan 不应生效")
	}
}

func TestTeamConfig_NormalizedPlanDefaults(t *testing.T) {
	got := TeamConfig{EnableSubAgents: true, EnablePlan: true}.normalized()
	if got.MaxPlanNudges != DefaultMaxPlanNudges {
		t.Fatalf("MaxPlanNudges 默认值应为 %d，got %d", DefaultMaxPlanNudges, got.MaxPlanNudges)
	}
	if got2 := (TeamConfig{MaxPlanNudges: 7}).normalized(); got2.MaxPlanNudges != 7 {
		t.Fatalf("显式配置应被保留，got %d", got2.MaxPlanNudges)
	}
}
