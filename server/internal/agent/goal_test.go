package codeagent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	goalpkg "github.com/ayanmw/multiagent2/server/internal/goal"
)

// newGoalTestCtx 构造一个带 Invocation（含固定 session id）的 ctx，
// 模拟框架在模型回调 / 工具调用时注入的运行时上下文。
func newGoalTestCtx(sessionID string) (context.Context, *agent.Invocation) {
	inv := agent.NewInvocation(
		agent.WithInvocationID("inv-1"),
		agent.WithInvocationSession(&session.Session{ID: sessionID}),
	)
	return agent.NewInvocationContext(context.Background(), inv), inv
}

// finalResponse 构造一个「成功的最终文本响应」（会被目标契约审查的那一类）。
func finalResponse(text string) *model.Response {
	return &model.Response{
		Done: true,
		Choices: []model.Choice{
			{Index: 0, Message: model.Message{Role: model.RoleAssistant, Content: text}},
		},
	}
}

// callTool 按工具名找到工具并以 JSON 入参调用它。
func callTool(t *testing.T, ctx context.Context, tools []tool.Tool, name, args string) map[string]any {
	t.Helper()
	for _, tl := range tools {
		decl := tl.Declaration()
		if decl == nil || decl.Name != name {
			continue
		}
		ct, ok := tl.(tool.CallableTool)
		if !ok {
			t.Fatalf("工具 %q 不可调用", name)
		}
		out, err := ct.Call(ctx, []byte(args))
		if err != nil {
			t.Fatalf("调用 %s(%s) 失败: %v", name, args, err)
		}
		raw, _ := json.Marshal(out)
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("工具 %s 返回值无法解析: %v (%s)", name, err, string(raw))
		}
		return m
	}
	t.Fatalf("未找到工具 %q", name)
	return nil
}

// callToolExpectErr 调用工具并要求其返回错误。
func callToolExpectErr(t *testing.T, ctx context.Context, tools []tool.Tool, name, args string) error {
	t.Helper()
	for _, tl := range tools {
		decl := tl.Declaration()
		if decl == nil || decl.Name != name {
			continue
		}
		ct, _ := tl.(tool.CallableTool)
		_, err := ct.Call(ctx, []byte(args))
		if err == nil {
			t.Fatalf("调用 %s(%s) 期望报错但成功了", name, args)
		}
		return err
	}
	t.Fatalf("未找到工具 %q", name)
	return nil
}

func TestGoalEnforcer_NameAndTools(t *testing.T) {
	e := NewGoalEnforcer()
	if e.Name() != GoalExtensionName {
		t.Fatalf("扩展名不符: %q", e.Name())
	}
	want := map[string]bool{ToolCreateGoal: false, ToolGetGoal: false, ToolUpdateGoal: false}
	tools := e.Tools()
	if len(tools) != 3 {
		t.Fatalf("应当贡献 3 个目标工具，got %d", len(tools))
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
}

func TestGoalTools_Roundtrip(t *testing.T) {
	store := goalpkg.NewStore(0)
	e := NewGoalEnforcer(WithGoalStore(store))
	tools := e.Tools()
	ctx, _ := newGoalTestCtx("s1")

	// 未立目标时 get_goal 返回 ok=false 并提示先建目标（而不是抛错中断 Agent）。
	got := callTool(t, ctx, tools, ToolGetGoal, `{}`)
	if got["ok"] != false {
		t.Fatalf("未立目标时 get_goal.ok 应为 false: %+v", got)
	}
	if !strings.Contains(got["hint"].(string), ToolCreateGoal) {
		t.Fatalf("get_goal 应提示调用 create_goal: %+v", got)
	}

	// 未立目标时 update_goal 必须报错（避免模型跳过立目标环节）。
	err := callToolExpectErr(t, ctx, tools, ToolUpdateGoal, `{"status":"complete"}`)
	if !strings.Contains(err.Error(), ToolCreateGoal) {
		t.Fatalf("update_goal 的错误应引导调用 create_goal: %v", err)
	}

	// 建立目标。
	created := callTool(t, ctx, tools, ToolCreateGoal,
		`{"title":"创建 hello.txt","description":"M1-11 验收","acceptance_criteria":["文件存在","内容正确"]}`)
	if created["ok"] != true {
		t.Fatalf("create_goal 失败: %+v", created)
	}
	if !store.IsOpen("sess:s1") {
		t.Fatal("create_goal 之后作用域 sess:s1 应当存在未收敛目标")
	}

	// 非法状态必须被拒绝。
	if err := callToolExpectErr(t, ctx, tools, ToolUpdateGoal, `{"status":"done"}`); err == nil {
		t.Fatal("非法状态应被拒绝")
	}

	// blocked 缺原因必须被拒绝。
	if err := callToolExpectErr(t, ctx, tools, ToolUpdateGoal, `{"status":"blocked"}`); err == nil {
		t.Fatal("blocked 缺少原因应被拒绝")
	}

	// 汇报进展：仍未收敛。
	upd := callTool(t, ctx, tools, ToolUpdateGoal, `{"progress":"已写入文件"}`)
	if upd["ok"] != true || !strings.Contains(upd["hint"].(string), "拦截") {
		t.Fatalf("未收敛时 update_goal 应提示会被拦截: %+v", upd)
	}

	// 收敛。
	done := callTool(t, ctx, tools, ToolUpdateGoal, `{"status":"complete","progress":"全部验收标准满足"}`)
	if done["ok"] != true {
		t.Fatalf("update_goal(complete) 失败: %+v", done)
	}
	if store.IsOpen("sess:s1") {
		t.Fatal("complete 之后不应再视为未收敛")
	}
	if !strings.Contains(done["hint"].(string), "可以向用户给出最终答复") {
		t.Fatalf("收敛后应提示可以收尾: %+v", done)
	}
}

func TestGoalEnforcer_BlocksPrematureFinal(t *testing.T) {
	store := goalpkg.NewStore(0)
	e := NewGoalEnforcer(WithGoalStore(store), WithGoalMaxNudges(5))
	ctx, inv := newGoalTestCtx("s2")

	// 1) 未立目标就想收工 → 拦截。
	res, err := e.afterModel(ctx, &model.AfterModelArgs{Response: finalResponse("我做完了")})
	if err != nil {
		t.Fatalf("afterModel 报错: %v", err)
	}
	if res == nil || res.CustomResponse == nil {
		t.Fatal("未立目标时的最终答复必须被拦截")
	}
	if res.CustomResponse.Done {
		t.Fatal("拦截响应的 Done 必须为 false，否则 llmflow 会退出循环")
	}
	if len(res.CustomResponse.Choices) != 0 {
		t.Fatal("拦截响应必须清空 Choices，避免把过早答复泄漏给前端")
	}

	// 2) 立目标后仍未收敛 → 继续拦截。
	if _, cerr := store.Create("sess:s2", "t", "", nil); cerr != nil {
		t.Fatalf("Create 失败: %v", cerr)
	}
	res, _ = e.afterModel(ctx, &model.AfterModelArgs{Response: finalResponse("这次真的做完了")})
	if res == nil || res.CustomResponse == nil {
		t.Fatal("目标未收敛时的最终答复必须被拦截")
	}

	// 3) 目标 complete → 放行。
	done := goalpkg.StatusComplete
	if _, uerr := store.Update("sess:s2", goalpkg.Patch{Status: &done}); uerr != nil {
		t.Fatalf("Update 失败: %v", uerr)
	}
	res, _ = e.afterModel(ctx, &model.AfterModelArgs{Response: finalResponse("目标达成")})
	if res != nil {
		t.Fatal("目标已 complete 时必须放行最终答复")
	}

	// 4) blocked 同样放行（客观受阻允许结束并向用户说明）。
	blocked := goalpkg.StatusBlocked
	reason := "缺少凭据"
	if _, uerr := store.Update("sess:s2", goalpkg.Patch{Status: &blocked, Blocker: &reason}); uerr != nil {
		t.Fatalf("Update 失败: %v", uerr)
	}
	if res, _ = e.afterModel(ctx, &model.AfterModelArgs{Response: finalResponse("被卡住了")}); res != nil {
		t.Fatal("目标 blocked 时必须放行最终答复")
	}
	_ = inv
}

func TestGoalEnforcer_PassesThroughNonFinalResponses(t *testing.T) {
	e := NewGoalEnforcer() // requireGoal=true 且未立目标：只要响应不是最终答复就必须放行
	ctx, _ := newGoalTestCtx("s3")

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

	// 模型调用本身出错时也必须原样透出。
	if res, _ := e.afterModel(ctx, &model.AfterModelArgs{
		Response: finalResponse("x"), Error: context.Canceled,
	}); res != nil {
		t.Fatal("模型调用出错时不应拦截")
	}
}

func TestGoalEnforcer_FailOpenAfterBudget(t *testing.T) {
	e := NewGoalEnforcer(WithGoalMaxNudges(2))
	ctx, _ := newGoalTestCtx("s4")

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

func TestGoalEnforcer_RequireGoalDisabled(t *testing.T) {
	e := NewGoalEnforcer(WithRequireGoal(false))
	ctx, _ := newGoalTestCtx("s5")
	if res, _ := e.afterModel(ctx, &model.AfterModelArgs{Response: finalResponse("直接收工")}); res != nil {
		t.Fatal("关闭 requireGoal 后，未立目标不应拦截")
	}
}

func TestGoalEnforcer_BeforeModel(t *testing.T) {
	store := goalpkg.NewStore(0)
	e := NewGoalEnforcer(WithGoalStore(store), WithGoalMaxNudges(3))
	ctx, _ := newGoalTestCtx("s6")

	// 目标已收敛时：不关流式、不注入催办。
	if _, cerr := store.Create("sess:s6", "t", "", nil); cerr != nil {
		t.Fatalf("Create 失败: %v", cerr)
	}
	done := goalpkg.StatusComplete
	if _, uerr := store.Update("sess:s6", goalpkg.Patch{Status: &done}); uerr != nil {
		t.Fatalf("Update 失败: %v", uerr)
	}
	req := &model.Request{}
	req.GenerationConfig.Stream = true
	if _, err := e.beforeModel(ctx, &model.BeforeModelArgs{Request: req}); err != nil {
		t.Fatalf("beforeModel 报错: %v", err)
	}
	if !req.GenerationConfig.Stream {
		t.Fatal("目标已收敛时不应关闭流式（最终答复要能逐字输出）")
	}
	if len(req.Messages) != 0 {
		t.Fatal("目标已收敛时不应注入催办消息")
	}

	// 目标未收敛：关闭流式；未被拦截过则不注入催办。
	running := goalpkg.StatusInProgress
	if _, uerr := store.Update("sess:s6", goalpkg.Patch{Status: &running}); uerr != nil {
		t.Fatalf("Update 失败: %v", uerr)
	}
	req = &model.Request{}
	req.GenerationConfig.Stream = true
	_, _ = e.beforeModel(ctx, &model.BeforeModelArgs{Request: req})
	if req.GenerationConfig.Stream {
		t.Fatal("目标未收敛时必须关闭流式，否则过早答复会先于拦截决策抵达前端")
	}
	if len(req.Messages) != 0 {
		t.Fatal("未发生拦截时不应注入催办消息")
	}

	// 发生拦截后：下一轮注入催办，且只注入一次。
	if res, _ := e.afterModel(ctx, &model.AfterModelArgs{Response: finalResponse("提前收工")}); res == nil {
		t.Fatal("目标未收敛时应当被拦截")
	}
	req = &model.Request{}
	req.GenerationConfig.Stream = true
	_, _ = e.beforeModel(ctx, &model.BeforeModelArgs{Request: req})
	if len(req.Messages) != 1 {
		t.Fatalf("拦截后应注入 1 条催办消息，got %d", len(req.Messages))
	}
	nudge := req.Messages[0].Content
	for _, want := range []string{"目标契约", "已被拦截", ToolUpdateGoal} {
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

func TestGoalEnforcer_NoGoalNudgeMentionsCreate(t *testing.T) {
	e := NewGoalEnforcer(WithGoalMaxNudges(3))
	ctx, _ := newGoalTestCtx("s7")
	if res, _ := e.afterModel(ctx, &model.AfterModelArgs{Response: finalResponse("直接收工")}); res == nil {
		t.Fatal("未立目标时应被拦截")
	}
	req := &model.Request{}
	_, _ = e.beforeModel(ctx, &model.BeforeModelArgs{Request: req})
	if len(req.Messages) != 1 || !strings.Contains(req.Messages[0].Content, ToolCreateGoal) {
		t.Fatalf("未立目标的催办应引导调用 create_goal: %+v", req.Messages)
	}
}

func TestGoalScope(t *testing.T) {
	if got := goalScope(nil); got != "default" {
		t.Fatalf("nil invocation 的作用域应为 default，got %q", got)
	}
	inv := agent.NewInvocation(agent.WithInvocationID("inv-9"))
	if got := goalScope(inv); got != "inv:inv-9" {
		t.Fatalf("无会话时应退化为 invocation 作用域，got %q", got)
	}
	inv2 := agent.NewInvocation(
		agent.WithInvocationID("inv-9"),
		agent.WithInvocationSession(&session.Session{ID: "abc"}),
	)
	if got := goalScope(inv2); got != "sess:abc" {
		t.Fatalf("有会话时应按 session 隔离，got %q", got)
	}
}

func TestTeamInstruction_GoalSection(t *testing.T) {
	base := teamInstruction(TeamConfig{EnableSubAgents: true}.normalized())
	if strings.Contains(base, ToolCreateGoal) {
		t.Fatal("未开启目标契约时不应注入目标契约规程")
	}
	withGoal := teamInstruction(TeamConfig{EnableSubAgents: true, EnableGoal: true}.normalized())
	for _, want := range []string{ToolCreateGoal, ToolGetGoal, ToolUpdateGoal, "complete", "blocked"} {
		if !strings.Contains(withGoal, want) {
			t.Fatalf("目标契约规程缺少 %q", want)
		}
	}
	// 同时开启 Reviewer 时两段规程都要在。
	both := teamInstruction(TeamConfig{EnableSubAgents: true, EnableReviewer: true, EnableGoal: true}.normalized())
	if !strings.Contains(both, RoleReviewer) || !strings.Contains(both, ToolCreateGoal) {
		t.Fatal("Reviewer 回环与目标契约规程应当共存")
	}
	// 单代理模式（未开子代理）下目标契约不生效——契约只装 Orchestrator。
	if (TeamConfig{EnableGoal: true}).goalEnabled() {
		t.Fatal("未开启子代理模式时目标契约不应生效")
	}
}

func TestTeamConfig_NormalizedGoalDefaults(t *testing.T) {
	got := TeamConfig{EnableSubAgents: true, EnableGoal: true}.normalized()
	if got.MaxGoalNudges != DefaultMaxGoalNudges {
		t.Fatalf("MaxGoalNudges 默认值应为 %d，got %d", DefaultMaxGoalNudges, got.MaxGoalNudges)
	}
	if got2 := (TeamConfig{MaxGoalNudges: 7}).normalized(); got2.MaxGoalNudges != 7 {
		t.Fatalf("显式配置应被保留，got %d", got2.MaxGoalNudges)
	}
}
