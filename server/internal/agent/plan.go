// plan.go 实现 M1-12「CycleAgent / Plan-Execute」在框架侧的落地：
//
//	create_plan / get_plan / update_step / add_steps 四个工具 + PlanEnforcer 扩展。
//
// 为什么不是「新写一个 CycleAgent 类型」：
//   - 框架 v1.10.0 没有 CycleAgent 原语（只有 Chain/Parallel/Graph），
//     而 llmflow 本身就是一个「模型 → 工具 → 模型 → …」的循环；
//     真正缺的不是循环，而是**循环的终止条件**与**跨轮的状态载体**。
//   - 因此本文件把 Plan-Execute 实现为「外置计划 + 循环闸门」：
//     planner 用 create_plan 把计划写到 internal/plan 的 Store（外置，不靠上下文记忆），
//     executor 每做完一项调 update_step 更新进度，
//     PlanEnforcer 的 AfterModel 在「还有未完成步骤」时拦截最终答复、令 llmflow 继续循环，
//     BeforeModel 把最新的 PLAN/PROGRESS 回灌给模型（上下文被截断也不会丢失任务状态）。
//
// 与 M1-11 目标契约的分工：
//   - Goal 契约管「要不要收工」（目标是否达成/受阻）；
//   - Plan 循环管「怎么一步步干」（计划是否逐项执行完毕）。
//     两者都装在 Orchestrator 上，AfterModel 回调按安装顺序执行、
//     首个返回 CustomResponse 的短路（见框架 model.Callbacks.RunAfterModel），
//     故二者可叠加：先要求立目标，再要求把计划做完。
//
// 参考实现：与 goal.go 同款（对齐框架内置 agent/extension/todoenforcer）。
package codeagent

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/extension"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"

	planpkg "github.com/ayanmw/multiagent2/server/internal/plan"
)

// Plan-Execute 工具名（须匹配 ^[a-zA-Z0-9_-]+$）。
const (
	ToolCreatePlan = "create_plan"
	ToolGetPlan    = "get_plan"
	ToolUpdateStep = "update_step"
	ToolAddSteps   = "add_steps"
)

// PlanExtensionName 是扩展在框架中的唯一名称（用于去重与错误包装）。
const PlanExtensionName = "plan-execute"

// DefaultMaxPlanNudges 是默认「最多拦截几次计划未做完就收工的答复」。
// 超出后放行（fail-open），避免模型不配合时把整轮 Run 卡死。
const DefaultMaxPlanNudges = 3

// invocation 状态键（随 Invocation 生命周期消亡，天然按次请求隔离）。
const (
	stateKeyPlanNudgeCount    = "plan:nudge_count"
	stateKeyPlanRemindPending = "plan:remind_pending"
)

// PlanInstruction 是注入 Orchestrator 的 Plan-Execute 作业规程。
var PlanInstruction = "\n\n【计划执行循环（Plan-Execute，强制）】" +
	"\n1. 任务需要两步以上时，先调用 " + ToolCreatePlan +
	" 把它拆成有序步骤（每步是一个可独立验证的小任务，写清楚做什么）；" +
	"\n2. 然后进入执行循环：每次只推进一个步骤——" +
	"开工前调用 " + ToolUpdateStep + "(status=\"in_progress\") 认领，" +
	"做完后调用 " + ToolUpdateStep + "(status=\"done\", note=\"实际做了什么/结果如何\") 收尾；" +
	"\n3. 执行中发现遗漏的工作，用 " + ToolAddSteps + " 追加步骤，不要偷偷做掉不记录；" +
	"\n4. 某步确实不必做或做不了：" + ToolUpdateStep + "(status=\"skipped\"/\"failed\", note=\"具体原因\")，" +
	"理由必填，不允许静默跳过；" +
	"\n5. 忘记进度时调用 " + ToolGetPlan + " 回读计划与进展，不要凭记忆臆测；" +
	"\n6. 只要还有 pending / in_progress 的步骤，就不允许给出最终答复——" +
	"系统会拦截并要求你继续推进。全部步骤收敛后再向用户汇报。"

// PlanEnforcer 是 Plan-Execute 扩展：贡献四个计划工具，并用模型回调驱动执行循环。
type PlanEnforcer struct {
	store       *planpkg.Store
	maxNudges   int
	requirePlan bool
	tools       []tool.Tool
}

// 编译期接口断言。
var _ extension.Extension = (*PlanEnforcer)(nil)

// PlanOption 配置 PlanEnforcer。
type PlanOption func(*PlanEnforcer)

// WithPlanMaxNudges 设置最多拦截次数；<=0 时取 DefaultMaxPlanNudges。
func WithPlanMaxNudges(n int) PlanOption {
	return func(e *PlanEnforcer) {
		if n > 0 {
			e.maxNudges = n
		}
	}
}

// WithPlanStore 注入自定义 Store（测试或跨 Agent 共享计划时使用）。
func WithPlanStore(s *planpkg.Store) PlanOption {
	return func(e *PlanEnforcer) {
		if s != nil {
			e.store = s
		}
	}
}

// WithRequirePlan 设置「未立计划是否也拦截」，默认 false。
//
// 默认不拦截是刻意的：一句话就能答完的请求不该被逼着先写计划
// （「必须先立目标」由 M1-11 目标契约负责）；一旦计划存在，
// 就必须逐项执行完毕才允许收工。
func WithRequirePlan(v bool) PlanOption {
	return func(e *PlanEnforcer) { e.requirePlan = v }
}

// NewPlanEnforcer 构造 Plan-Execute 扩展。
func NewPlanEnforcer(opts ...PlanOption) *PlanEnforcer {
	e := &PlanEnforcer{
		store:       planpkg.NewStore(0),
		maxNudges:   DefaultMaxPlanNudges,
		requirePlan: false,
	}
	for _, o := range opts {
		if o != nil {
			o(e)
		}
	}
	e.tools = []tool.Tool{e.createPlanTool(), e.getPlanTool(), e.updateStepTool(), e.addStepsTool()}
	return e
}

// Store 暴露内部存储，供上层（测试 / 可观测 / M1-16 的 artifact 落盘）读取。
func (e *PlanEnforcer) Store() *planpkg.Store { return e.store }

// Tools 返回扩展贡献的四个计划工具（供不走 extension 装配路径的调用方复用）。
func (e *PlanEnforcer) Tools() []tool.Tool {
	if e == nil {
		return nil
	}
	return append([]tool.Tool(nil), e.tools...)
}

// Name 实现 extension.Extension。
func (e *PlanEnforcer) Name() string { return PlanExtensionName }

// Register 实现 extension.Extension：贡献工具 + 注册 BeforeModel/AfterModel 回调。
func (e *PlanEnforcer) Register(r *extension.Registry) {
	if r == nil {
		return
	}
	r.Tools(e.tools...)
	r.BeforeModel(e.beforeModel)
	r.AfterModel(e.afterModel)
}

// ---------------------------------------------------------------------------
// 状态
// ---------------------------------------------------------------------------

func planNudgeCount(inv *agent.Invocation) int {
	if inv == nil {
		return 0
	}
	v, _ := agent.GetStateValue[int](inv, stateKeyPlanNudgeCount)
	return v
}

func incPlanNudgeCount(inv *agent.Invocation) int {
	if inv == nil {
		return 0
	}
	n := planNudgeCount(inv) + 1
	inv.SetState(stateKeyPlanNudgeCount, n)
	return n
}

func planRemindPending(inv *agent.Invocation) bool {
	if inv == nil {
		return false
	}
	v, _ := agent.GetStateValue[bool](inv, stateKeyPlanRemindPending)
	return v
}

func setPlanRemindPending(inv *agent.Invocation, pending bool) {
	if inv == nil {
		return
	}
	if pending {
		inv.SetState(stateKeyPlanRemindPending, true)
		return
	}
	inv.DeleteState(stateKeyPlanRemindPending)
}

// ---------------------------------------------------------------------------
// 模型回调
// ---------------------------------------------------------------------------

// planBlockReason 说明本次为何要拦截（"" 表示放行）。
type planBlockReason string

const (
	planBlockNone     planBlockReason = ""
	planBlockNoPlan   planBlockReason = "no_plan"
	planBlockOpenPlan planBlockReason = "open_plan"
)

// evaluate 判定当前作用域是否应当阻止「最终答复」。
func (e *PlanEnforcer) evaluate(scope string) (planBlockReason, *planpkg.Plan) {
	p, err := e.store.Get(scope)
	if errors.Is(err, planpkg.ErrNotFound) {
		if e.requirePlan {
			return planBlockNoPlan, nil
		}
		return planBlockNone, nil
	}
	if err != nil || p == nil {
		return planBlockNone, nil
	}
	if p.IsOpen() {
		return planBlockOpenPlan, p
	}
	return planBlockNone, p
}

// beforeModel 在计划仍有未完成步骤时关闭流式（否则半截答复会先于拦截决策抵达前端），
// 并在上一轮被拦截后回灌最新的 PLAN/PROGRESS 与催办消息。
func (e *PlanEnforcer) beforeModel(
	ctx context.Context,
	args *model.BeforeModelArgs,
) (*model.BeforeModelResult, error) {
	if args == nil || args.Request == nil {
		return nil, nil
	}
	inv, _ := agent.InvocationFromContext(ctx)
	scope := goalScope(inv)

	pending := planRemindPending(inv)
	if pending {
		// 无条件消费标记，避免格式化结果为空时无限重复注入。
		setPlanRemindPending(inv, false)
	}

	reason, p := e.evaluate(scope)
	if reason == planBlockNone {
		return nil, nil
	}
	// 计划未执行完：必须拿到完整响应才能判定，故关闭流式。
	args.Request.GenerationConfig.Stream = false

	if !pending {
		return nil, nil
	}
	msg := e.nudgeMessage(reason, p, planNudgeCount(inv))
	if msg == "" {
		return nil, nil
	}
	args.Request.Messages = append(args.Request.Messages, model.NewUserMessage(msg))
	return nil, nil
}

// nudgeMessage 生成催办文案：把外置的 PLAN + PROGRESS + 下一步一起回灌。
func (e *PlanEnforcer) nudgeMessage(reason planBlockReason, p *planpkg.Plan, attempt int) string {
	var b strings.Builder
	b.WriteString("[系统·计划执行] 你的答复已被拦截，未送达用户。")
	switch reason {
	case planBlockNoPlan:
		b.WriteString("原因：本轮尚未建立执行计划。请先调用 " + ToolCreatePlan +
			" 把任务拆成有序步骤，再逐项执行。")
	case planBlockOpenPlan:
		c := p.Counts()
		fmt.Fprintf(&b, "原因：计划尚未执行完（还有 %d 个步骤未收敛），不允许给出最终答复。\n", c.Open())
		b.WriteString(p.Render())
		b.WriteString("\n")
		b.WriteString(p.RenderProgress())
		if next := p.Next(); next != nil {
			fmt.Fprintf(&b, "\n下一步：%s %s —— 现在就去做它，", next.ID, next.Title)
			b.WriteString("做完后调用 " + ToolUpdateStep + "(step_id=\"" + next.ID +
				"\", status=\"done\", note=\"...\") 更新进度。")
		}
		b.WriteString("\n若某步确实不必做或做不了，用 " + ToolUpdateStep +
			"(status=\"skipped\"/\"failed\", note=\"原因\") 如实标注，不要静默跳过。")
	default:
		return ""
	}
	if e.maxNudges > 0 {
		b.WriteString("（第 " + strconv.Itoa(attempt) + "/" + strconv.Itoa(e.maxNudges) + " 次提醒）")
	}
	return b.String()
}

// afterModel 拦截「计划未执行完时的最终答复」，令 llmflow 继续下一轮循环。
//
// 决策顺序（与 goal.go / 框架 todoenforcer 对齐，快路径优先）：
//  1. 无响应 / 出错 / 非最终响应（流式分片、工具调用）→ 放行；
//  2. 计划已全部收敛，或未开启 requirePlan 且无计划 → 放行；
//  3. 拦截预算耗尽 → 放行（fail-open）；
//  4. 否则返回一个 Done=false 且不含内容的控制响应，令循环继续。
func (e *PlanEnforcer) afterModel(
	ctx context.Context,
	args *model.AfterModelArgs,
) (*model.AfterModelResult, error) {
	if args == nil || args.Response == nil {
		return nil, nil
	}
	if args.Error != nil || args.Response.Error != nil {
		return nil, nil
	}
	if !shouldConsiderGoalResponse(args.Response) {
		return nil, nil
	}
	inv, _ := agent.InvocationFromContext(ctx)
	scope := goalScope(inv)

	reason, _ := e.evaluate(scope)
	if reason == planBlockNone {
		return nil, nil
	}
	if planNudgeCount(inv) >= e.maxNudges {
		// 预算耗尽：放行，宁可让用户看到一个可能不完整的答复，也不要把 Runner 卡死。
		setPlanRemindPending(inv, false)
		return nil, nil
	}
	incPlanNudgeCount(inv)
	setPlanRemindPending(inv, true)
	return &model.AfterModelResult{CustomResponse: goalBlockedResponse(args.Response)}, nil
}

// ---------------------------------------------------------------------------
// 工具
// ---------------------------------------------------------------------------

// planStepInput 是计划步骤的输入结构（create_plan / add_steps 共用）。
type planStepInput struct {
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

// createPlanInput 是 create_plan 入参。
type createPlanInput struct {
	Title string          `json:"title"`
	Steps []planStepInput `json:"steps"`
}

// addStepsInput 是 add_steps 入参。
type addStepsInput struct {
	Steps []planStepInput `json:"steps"`
}

// updateStepInput 是 update_step 入参。空字符串表示「不修改该字段」。
type updateStepInput struct {
	StepID string `json:"step_id"`
	Status string `json:"status"`
	Note   string `json:"note"`
}

// planToolOutput 是四个工具的统一返回体。
type planToolOutput struct {
	OK       bool           `json:"ok"`
	Plan     *planpkg.Plan  `json:"plan,omitempty"`
	Summary  string         `json:"summary"`
	Progress string         `json:"progress,omitempty"`
	NextStep *planpkg.Step  `json:"next_step,omitempty"`
	Counts   *planpkg.Counts `json:"counts,omitempty"`
	Hint     string         `json:"hint,omitempty"`
}

// toSpecs 把工具入参转换为领域层步骤规格。
func toSpecs(in []planStepInput) []planpkg.StepSpec {
	if len(in) == 0 {
		return nil
	}
	out := make([]planpkg.StepSpec, 0, len(in))
	for _, s := range in {
		out = append(out, planpkg.StepSpec{Title: s.Title, Detail: s.Detail})
	}
	return out
}

// renderPlanOutput 组装统一返回体（含 PLAN + PROGRESS + 下一步）。
func renderPlanOutput(p *planpkg.Plan) planToolOutput {
	c := p.Counts()
	out := planToolOutput{
		OK:       true,
		Plan:     p,
		Summary:  p.Render(),
		Progress: p.RenderProgress(),
		NextStep: p.Next(),
		Counts:   &c,
	}
	if p.IsOpen() {
		next := "继续推进下一步"
		if s := p.Next(); s != nil {
			next = "现在去做 " + s.ID + "：" + s.Title
		}
		out.Hint = "计划仍有未完成步骤，" + next + "；此时给出最终答复会被系统拦截。"
	} else {
		out.Hint = "计划已全部收敛，现在可以向用户汇报最终结果。"
	}
	return out
}

func (e *PlanEnforcer) createPlanTool() tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, in createPlanInput) (planToolOutput, error) {
			inv, _ := agent.InvocationFromContext(ctx)
			p, err := e.store.Create(goalScope(inv), in.Title, toSpecs(in.Steps))
			if err != nil {
				return planToolOutput{}, err
			}
			out := renderPlanOutput(p)
			out.Hint = "计划已建立。请从第一个步骤开始逐项执行，每完成一项就调用 " +
				ToolUpdateStep + " 更新状态；所有步骤收敛前不要给出最终答复。"
			return out, nil
		},
		function.WithName(ToolCreatePlan),
		function.WithDescription("为当前任务建立可执行的计划：title 是任务总目标，"+
			"steps 是有序步骤清单（每步 title 说明做什么、detail 可选补充细节）。"+
			"任务需要两步以上时必须先调用它；重复调用会覆盖旧计划（用于重新规划）。"+
			"建立后必须逐项执行，全部步骤收敛（done/skipped/failed）前无法结束本轮。"),
	)
}

func (e *PlanEnforcer) getPlanTool() tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, _ struct{}) (planToolOutput, error) {
			inv, _ := agent.InvocationFromContext(ctx)
			p, err := e.store.Get(goalScope(inv))
			if errors.Is(err, planpkg.ErrNotFound) {
				return planToolOutput{
					OK:      false,
					Summary: "当前没有执行计划。",
					Hint:    "任务需要多步时，请先调用 " + ToolCreatePlan + " 建立计划。",
				}, nil
			}
			if err != nil {
				return planToolOutput{}, err
			}
			return renderPlanOutput(p), nil
		},
		function.WithName(ToolGetPlan),
		function.WithDescription("回读当前执行计划与进展（步骤清单、各步状态与执行记录、下一步是什么）。"+
			"不确定还剩什么没做时调用它，不要凭记忆臆测。"),
	)
}

func (e *PlanEnforcer) updateStepTool() tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, in updateStepInput) (planToolOutput, error) {
			inv, _ := agent.InvocationFromContext(ctx)
			patch, err := buildStepPatch(in)
			if err != nil {
				return planToolOutput{}, err
			}
			p, err := e.store.UpdateStep(goalScope(inv), in.StepID, patch)
			if errors.Is(err, planpkg.ErrNotFound) {
				return planToolOutput{}, fmt.Errorf("%s: 尚未建立计划，请先调用 %s", ToolUpdateStep, ToolCreatePlan)
			}
			if err != nil {
				return planToolOutput{}, err
			}
			return renderPlanOutput(p), nil
		},
		function.WithName(ToolUpdateStep),
		function.WithDescription("更新计划中某个步骤的状态与执行记录："+
			"step_id 取自计划（如 s1）；status 取值 pending / in_progress / done / skipped / failed——"+
			"开工时填 in_progress、做完填 done；"+
			"skipped（无需执行）与 failed（执行失败）必须在 note 中写明理由。"+
			"note 用于记录「实际做了什么、结果如何」，会作为进展保留。留空的字段保持不变。"),
	)
}

func (e *PlanEnforcer) addStepsTool() tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, in addStepsInput) (planToolOutput, error) {
			inv, _ := agent.InvocationFromContext(ctx)
			p, err := e.store.AddSteps(goalScope(inv), toSpecs(in.Steps))
			if errors.Is(err, planpkg.ErrNotFound) {
				return planToolOutput{}, fmt.Errorf("%s: 尚未建立计划，请先调用 %s", ToolAddSteps, ToolCreatePlan)
			}
			if err != nil {
				return planToolOutput{}, err
			}
			return renderPlanOutput(p), nil
		},
		function.WithName(ToolAddSteps),
		function.WithDescription("向现有计划追加步骤（执行过程中发现遗漏的工作时使用）。"+
			"新步骤会排在末尾并处于 pending 状态，同样必须逐项执行完毕。"),
	)
}

// buildStepPatch 把工具入参转换为领域层 StepPatch（空串=不修改）。
func buildStepPatch(in updateStepInput) (planpkg.StepPatch, error) {
	var p planpkg.StepPatch
	if s := strings.TrimSpace(in.Status); s != "" {
		st, err := planpkg.ParseStepStatus(s)
		if err != nil {
			return p, err
		}
		p.Status = &st
	}
	if in.Note != "" {
		v := in.Note
		p.Note = &v
	}
	return p, nil
}
