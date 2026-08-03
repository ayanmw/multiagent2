package codeagent

import (
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/model"

	"github.com/ayanmw/multiagent2/server/internal/artifact"
)

// TestStateEnforcer_NameAndTools 断言扩展名与四个状态工具齐全、描述非空。
func TestStateEnforcer_NameAndTools(t *testing.T) {
	e := NewStateEnforcer()
	if e.Name() != StateExtensionName {
		t.Fatalf("扩展名不符: %q", e.Name())
	}
	want := map[string]bool{
		ToolReadState:      false,
		ToolUpdatePlan:     false,
		ToolUpdateProgress: false,
		ToolAppendLearning: false,
	}
	tools := e.Tools()
	if len(tools) != 4 {
		t.Fatalf("应当贡献 4 个状态工具，got %d", len(tools))
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

// TestStateEnforcer_ToolsPersistToStore 断言 update_plan/update_progress/append_learning
// 真的把状态写入注入的 artifact.Store，且 read_state 能把三者读回。
func TestStateEnforcer_ToolsPersistToStore(t *testing.T) {
	store := artifact.NewMemoryStore()
	e := NewStateEnforcer(WithStateStore(store))
	tools := e.Tools()
	ctx, _ := newGoalTestCtx("s1")

	// update_plan：不传 # 时自动补「# 计划」标题。
	plan := callTool(t, ctx, tools, ToolUpdatePlan, `{"text":"实现 M1-16 工作状态外置"}`)
	if plan["ok"] != true {
		t.Fatalf("update_plan 失败: %+v", plan)
	}
	if !strings.HasPrefix(plan["content"].(string), "# 计划") {
		t.Fatalf("update_plan 应自动补 # 计划 标题: %+v", plan)
	}

	// update_progress：两次追加都应累积进同一份 PROGRESS。
	if p1 := callTool(t, ctx, tools, ToolUpdateProgress, `{"text":"已建 artifact 包"}`); p1["ok"] != true {
		t.Fatalf("update_progress #1 失败: %+v", p1)
	}
	if p2 := callTool(t, ctx, tools, ToolUpdateProgress, `{"text":"已接 engine"}`); p2["ok"] != true {
		t.Fatalf("update_progress #2 失败: %+v", p2)
	}
	prog, ok, _ := store.Read("sess:s1", artifact.ProgressArtifact)
	if !ok || !strings.Contains(prog, "已建 artifact 包") || !strings.Contains(prog, "已接 engine") {
		t.Fatalf("PROGRESS 未累积两次进展: %q", prog)
	}

	// append_learning。
	if l := callTool(t, ctx, tools, ToolAppendLearning, `{"text":"状态文件要按 session 作用域隔离"}`); l["ok"] != true {
		t.Fatalf("append_learning 失败: %+v", l)
	}

	// read_state：三者都回读得到。
	read := callTool(t, ctx, tools, ToolReadState, `{}`)
	if read["exists"] != true {
		t.Fatalf("read_state 应 exists=true: %+v", read)
	}
	if !strings.Contains(read["plan"].(string), "实现 M1-16") {
		t.Fatalf("read_state.plan 不符: %+v", read)
	}
	if !strings.Contains(read["progress"].(string), "已接 engine") {
		t.Fatalf("read_state.progress 不符: %+v", read)
	}
	if !strings.Contains(read["learnings"].(string), "session 作用域隔离") {
		t.Fatalf("read_state.learnings 不符: %+v", read)
	}

	// 空 text 必须被拒绝（避免模型写出空状态文件）。
	if err := callToolExpectErr(t, ctx, tools, ToolUpdatePlan, `{"text":""}`); err == nil {
		t.Fatal("空 text 应被拒绝")
	}
	if err := callToolExpectErr(t, ctx, tools, ToolUpdateProgress, `{"text":""}`); err == nil {
		t.Fatal("空 text 应被拒绝")
	}
	if err := callToolExpectErr(t, ctx, tools, ToolAppendLearning, `{"text":""}`); err == nil {
		t.Fatal("空 text 应被拒绝")
	}
}

// TestStateEnforcer_BeforeModelInjectsResume 是 M1-16 验收「中断后续跑能接上」的
// 代理级覆盖：run1 写状态 → 模拟进程重启（全新存储 + 全新扩展实例）→ run2 同一
// session 下的 beforeModel 必须把上次状态作为续跑上下文注入，且只注入一次。
func TestStateEnforcer_BeforeModelInjectsResume(t *testing.T) {
	root := t.TempDir()

	// —— run1：写入工作状态文件（长任务执行到一半被打断）——
	store1, _ := artifact.NewFileStore(root)
	e1 := NewStateEnforcer(WithStateStore(store1))
	ctx1, _ := newGoalTestCtx("rerun")
	callTool(t, ctx1, e1.Tools(), ToolUpdatePlan, `{"text":"步骤1已完成，步骤2进行中"}`)
	callTool(t, ctx1, e1.Tools(), ToolUpdateProgress, `{"text":"已完成步骤1"}`)
	callTool(t, ctx1, e1.Tools(), ToolAppendLearning, `{"text":"注意按 session 作用域隔离状态文件"}`)

	// —— 模拟重启：全新存储实例 + 全新扩展实例（进程内存清空，磁盘仍在）——
	store2, _ := artifact.NewFileStore(root)
	e2 := NewStateEnforcer(WithStateStore(store2))
	ctx2, _ := newGoalTestCtx("rerun") // 同一 session id → 同一作用域

	// run2 第一次模型调用前：beforeModel 读回上次状态并注入续跑消息。
	req := &model.Request{}
	if _, err := e2.beforeModel(ctx2, &model.BeforeModelArgs{Request: req}); err != nil {
		t.Fatalf("beforeModel 报错: %v", err)
	}
	if len(req.Messages) != 1 {
		t.Fatalf("run2 应注入 1 条续跑消息，got %d", len(req.Messages))
	}
	msg := req.Messages[0].Content
	for _, want := range []string{"[系统·续跑上下文]", "步骤1已完成", "已完成步骤1", "session 作用域隔离", ToolReadState} {
		if !strings.Contains(msg, want) {
			t.Fatalf("续跑消息缺少 %q:\n%s", want, msg)
		}
	}

	// 同一 run2 后续模型调用不应重复注入（每轮 run 只回灌一次）。
	req2 := &model.Request{}
	if _, err := e2.beforeModel(ctx2, &model.BeforeModelArgs{Request: req2}); err != nil {
		t.Fatalf("beforeModel 二次调用报错: %v", err)
	}
	if len(req2.Messages) != 0 {
		t.Fatalf("续跑消息只能注入一次，got %d", len(req2.Messages))
	}
}

// TestStateEnforcer_BeforeModelNoState 断言：作用域下没有任何状态文件时，
// beforeModel 不应注入任何续跑消息（全新长任务正常开局）。
func TestStateEnforcer_BeforeModelNoState(t *testing.T) {
	store := artifact.NewMemoryStore()
	e := NewStateEnforcer(WithStateStore(store))
	ctx, _ := newGoalTestCtx("fresh")
	req := &model.Request{}
	if _, err := e.beforeModel(ctx, &model.BeforeModelArgs{Request: req}); err != nil {
		t.Fatalf("beforeModel 报错: %v", err)
	}
	if len(req.Messages) != 0 {
		t.Fatalf("无先验状态时不应注入续跑消息，got %d", len(req.Messages))
	}
}
