// goal.go 实现 M1-11「目标契约」在框架侧的落地：
//
//	create_goal / get_goal / update_goal 三个工具 + GoalEnforcer 扩展。
//
// 设计要点：
//   - 契约化：Orchestrator 开工前必须用 create_goal 写下目标与验收标准，
//     过程中用 update_goal 汇报进展，只有目标变为 complete（达成）或 blocked
//     （客观受阻，且必须给出原因）时，才允许输出最终答复；
//   - 硬约束而非软提示：仅靠 prompt 约束 LLM 会「说完就跑」，因此用 AfterModel
//     回调拦截「过早的 final 响应」——把 Done 置 false 并清空 Choices，
//     让 llmflow 继续下一轮，同时避免把这条错误答复泄漏给前端与会话历史；
//   - 有界失败开放（fail-open）：连续拦截达到 MaxNudges 后放行，
//     防止模型不配合时把 Runner 卡死（与框架 todoenforcer 同策略）；
//   - 只装 Orchestrator：goal 扩展只在根编排者上安装，且该 Agent 不开启
//     EnableParallelTools（见 docs/03 §2.1「goal 扩展与并行工具冲突」）。
//
// 参考实现：trpc-agent-go v1.10.0 agent/extension/todoenforcer（框架内置的同类扩展）。
// 框架 v1.10.0 未提供 goal 包，故本文件自行实现。
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

	goalpkg "github.com/ayanmw/multiagent2/server/internal/goal"
)

// 目标契约工具名（须匹配 ^[a-zA-Z0-9_-]+$）。
const (
	ToolCreateGoal = "create_goal"
	ToolGetGoal    = "get_goal"
	ToolUpdateGoal = "update_goal"
)

// GoalExtensionName 是扩展在框架中的唯一名称（用于去重与错误包装）。
const GoalExtensionName = "goal-contract"

// DefaultMaxGoalNudges 是默认「最多拦截几次过早 final」。
// 超出后放行（fail-open），避免模型不配合时把整轮 Run 卡死。
const DefaultMaxGoalNudges = 3

// invocation 状态键（随 Invocation 生命周期消亡，天然按次请求隔离）。
const (
	stateKeyGoalNudgeCount    = "goal:nudge_count"
	stateKeyGoalRemindPending = "goal:remind_pending"
)

// GoalInstruction 是注入 Orchestrator 的目标契约作业规程。
var GoalInstruction = "\n\n【目标契约（强制，必须遵守）】" +
	"\n0. 收到任务后的第一步永远是调用 " + ToolCreateGoal +
	"，把「要达成什么」与「怎么算达成（验收标准）」写清楚；未立目标就直接答复会被系统拦截。" +
	"\n1. 每完成一个关键步骤，调用 " + ToolUpdateGoal + " 更新 progress，说明已做什么、还差什么；" +
	"\n2. 忘记当前目标时调用 " + ToolGetGoal + " 重新读取，不要凭记忆臆测；" +
	"\n3. 只有下面两种情况才允许输出最终答复：" +
	"\n   a) 全部验收标准都已满足 → 先 " + ToolUpdateGoal + "(status=\"complete\")；" +
	"\n   b) 遇到你自己无法解决的客观阻塞（缺权限/缺凭据/需求有歧义需用户澄清）→ " +
	"先 " + ToolUpdateGoal + "(status=\"blocked\", blocker=\"具体原因\")，再向用户说明缺什么。" +
	"\n4. 严禁在目标仍为 pending/in_progress 时给出最终答复；" +
	"也严禁为了收工而谎报 complete —— 没做完就如实标 blocked。"

// GoalEnforcer 是目标契约扩展：贡献三个 goal 工具，并用模型回调强制「未达成不结束」。
type GoalEnforcer struct {
	store       *goalpkg.Store
	maxNudges   int
	requireGoal bool
	tools       []tool.Tool
}

// 编译期接口断言。
var _ extension.Extension = (*GoalEnforcer)(nil)

// GoalOption 配置 GoalEnforcer。
type GoalOption func(*GoalEnforcer)

// WithGoalMaxNudges 设置最多拦截次数；<=0 时取 DefaultMaxGoalNudges。
func WithGoalMaxNudges(n int) GoalOption {
	return func(e *GoalEnforcer) {
		if n > 0 {
			e.maxNudges = n
		}
	}
}

// WithGoalStore 注入自定义 Store（测试或跨 Agent 共享目标时使用）。
func WithGoalStore(s *goalpkg.Store) GoalOption {
	return func(e *GoalEnforcer) {
		if s != nil {
			e.store = s
		}
	}
}

// WithRequireGoal 设置「未立目标是否也拦截」，默认 true。
// 置 false 时只在存在未收敛目标的情况下拦截。
func WithRequireGoal(v bool) GoalOption {
	return func(e *GoalEnforcer) { e.requireGoal = v }
}

// NewGoalEnforcer 构造目标契约扩展。
func NewGoalEnforcer(opts ...GoalOption) *GoalEnforcer {
	e := &GoalEnforcer{
		store:       goalpkg.NewStore(0),
		maxNudges:   DefaultMaxGoalNudges,
		requireGoal: true,
	}
	for _, o := range opts {
		if o != nil {
			o(e)
		}
	}
	e.tools = []tool.Tool{e.createGoalTool(), e.getGoalTool(), e.updateGoalTool()}
	return e
}

// Store 暴露内部存储，供上层（测试 / 可观测 / 未来的 artifact 落盘）读取。
func (e *GoalEnforcer) Store() *goalpkg.Store { return e.store }

// Tools 返回扩展贡献的三个目标工具（供不走 extension 装配路径的调用方复用）。
func (e *GoalEnforcer) Tools() []tool.Tool {
	if e == nil {
		return nil
	}
	return append([]tool.Tool(nil), e.tools...)
}

// Name 实现 extension.Extension。
func (e *GoalEnforcer) Name() string { return GoalExtensionName }

// Register 实现 extension.Extension：贡献工具 + 注册 BeforeModel/AfterModel 回调。
func (e *GoalEnforcer) Register(r *extension.Registry) {
	if r == nil {
		return
	}
	r.Tools(e.tools...)
	r.BeforeModel(e.beforeModel)
	r.AfterModel(e.afterModel)
}

// ---------------------------------------------------------------------------
// 作用域与状态
// ---------------------------------------------------------------------------

// goalScope 计算目标的作用域键：优先 session id（跨轮次保持同一目标），
// 退化为 invocation id（无会话时按次请求隔离）。
func goalScope(inv *agent.Invocation) string {
	if inv == nil {
		return "default"
	}
	if inv.Session != nil && inv.Session.ID != "" {
		return "sess:" + inv.Session.ID
	}
	if inv.InvocationID != "" {
		return "inv:" + inv.InvocationID
	}
	return "default"
}

func goalNudgeCount(inv *agent.Invocation) int {
	if inv == nil {
		return 0
	}
	v, _ := agent.GetStateValue[int](inv, stateKeyGoalNudgeCount)
	return v
}

func incGoalNudgeCount(inv *agent.Invocation) int {
	if inv == nil {
		return 0
	}
	n := goalNudgeCount(inv) + 1
	inv.SetState(stateKeyGoalNudgeCount, n)
	return n
}

func goalRemindPending(inv *agent.Invocation) bool {
	if inv == nil {
		return false
	}
	v, _ := agent.GetStateValue[bool](inv, stateKeyGoalRemindPending)
	return v
}

func setGoalRemindPending(inv *agent.Invocation, pending bool) {
	if inv == nil {
		return
	}
	if pending {
		inv.SetState(stateKeyGoalRemindPending, true)
		return
	}
	inv.DeleteState(stateKeyGoalRemindPending)
}

// ---------------------------------------------------------------------------
// 模型回调
// ---------------------------------------------------------------------------

// blockReason 说明本次为何要拦截（"" 表示放行）。
type blockReason string

const (
	blockNone     blockReason = ""
	blockNoGoal   blockReason = "no_goal"
	blockOpenGoal blockReason = "open_goal"
)

// evaluate 判定当前作用域是否应当阻止「最终答复」。
func (e *GoalEnforcer) evaluate(scope string) (blockReason, *goalpkg.Goal) {
	g, err := e.store.Get(scope)
	if errors.Is(err, goalpkg.ErrNotFound) {
		if e.requireGoal {
			return blockNoGoal, nil
		}
		return blockNone, nil
	}
	if err != nil || g == nil {
		return blockNone, nil
	}
	if g.IsOpen() {
		return blockOpenGoal, g
	}
	return blockNone, g
}

// beforeModel 在存在未收敛目标时关闭流式（否则半截答复会先于拦截决策抵达前端），
// 并在上一轮被拦截后注入一条催办消息。
func (e *GoalEnforcer) beforeModel(
	ctx context.Context,
	args *model.BeforeModelArgs,
) (*model.BeforeModelResult, error) {
	if args == nil || args.Request == nil {
		return nil, nil
	}
	inv, _ := agent.InvocationFromContext(ctx)
	scope := goalScope(inv)

	pending := goalRemindPending(inv)
	if pending {
		// 无条件消费标记，避免格式化结果为空时无限重复注入。
		setGoalRemindPending(inv, false)
	}

	reason, g := e.evaluate(scope)
	if reason == blockNone {
		return nil, nil
	}
	// 目标未收敛：必须拿到完整响应才能判定，故关闭流式。
	args.Request.GenerationConfig.Stream = false

	if !pending {
		return nil, nil
	}
	msg := e.nudgeMessage(reason, g, goalNudgeCount(inv))
	if msg == "" {
		return nil, nil
	}
	args.Request.Messages = append(args.Request.Messages, model.NewUserMessage(msg))
	return nil, nil
}

// nudgeMessage 生成催办文案。
func (e *GoalEnforcer) nudgeMessage(reason blockReason, g *goalpkg.Goal, attempt int) string {
	var b strings.Builder
	b.WriteString("[系统·目标契约] 你的答复已被拦截，未送达用户。")
	switch reason {
	case blockNoGoal:
		b.WriteString("原因：本轮尚未建立目标契约。请先调用 " + ToolCreateGoal +
			" 写清楚目标与验收标准，再继续干活。")
	case blockOpenGoal:
		b.WriteString("原因：目标尚未收敛（当前状态 " + string(g.Status) + "），不允许给出最终答复。\n")
		b.WriteString(g.Render())
		b.WriteString("\n请继续推进：还有工作就调用工具去做；")
		b.WriteString("确认全部验收标准已满足再 " + ToolUpdateGoal + "(status=\"complete\")；")
		b.WriteString("确实被客观因素卡住则 " + ToolUpdateGoal + "(status=\"blocked\", blocker=\"具体原因\")。")
	default:
		return ""
	}
	if e.maxNudges > 0 {
		b.WriteString("（第 " + strconv.Itoa(attempt) + "/" + strconv.Itoa(e.maxNudges) + " 次提醒）")
	}
	return b.String()
}

// afterModel 拦截「目标未收敛时的最终答复」。
//
// 决策顺序（与框架 todoenforcer 对齐，快路径优先）：
//  1. 无响应 / 出错 / 非最终响应（流式分片、工具调用）→ 放行；
//  2. 目标已 complete/blocked，或未开启 requireGoal 且无目标 → 放行；
//  3. 拦截预算耗尽 → 放行（fail-open）；
//  4. 否则返回一个 Done=false 且不含内容的控制响应，令 llmflow 继续循环。
func (e *GoalEnforcer) afterModel(
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
	if reason == blockNone {
		return nil, nil
	}
	if goalNudgeCount(inv) >= e.maxNudges {
		// 预算耗尽：放行，宁可让用户看到一个可能不完整的答复，也不要把 Runner 卡死。
		setGoalRemindPending(inv, false)
		return nil, nil
	}
	incGoalNudgeCount(inv)
	setGoalRemindPending(inv, true)
	return &model.AfterModelResult{CustomResponse: goalBlockedResponse(args.Response)}, nil
}

// goalBlockedResponse 构造「非内容控制响应」：Done=false 让循环继续，
// 清空 Choices 避免把过早的答复泄漏给前端与会话历史。
func goalBlockedResponse(src *model.Response) *model.Response {
	if src == nil {
		return &model.Response{Done: false}
	}
	rsp := src.Clone()
	rsp.Done = false
	rsp.IsPartial = false
	rsp.Choices = nil
	rsp.Error = nil
	return rsp
}

// shouldConsiderGoalResponse 只关心「成功的最终文本响应」：
// 工具调用响应是继续干活的信号，流式分片与错误响应必须原样透出。
func shouldConsiderGoalResponse(rsp *model.Response) bool {
	if rsp == nil {
		return false
	}
	if rsp.IsPartial || rsp.Error != nil {
		return false
	}
	if rsp.IsToolCallResponse() {
		return false
	}
	return rsp.IsFinalResponse()
}

// ---------------------------------------------------------------------------
// 工具
// ---------------------------------------------------------------------------

// createGoalInput 是 create_goal 入参。
type createGoalInput struct {
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
}

// updateGoalInput 是 update_goal 入参。空字符串表示「不修改该字段」。
type updateGoalInput struct {
	Status   string `json:"status"`
	Progress string `json:"progress"`
	Blocker  string `json:"blocker"`
}

// goalToolOutput 是三个工具的统一返回体。
type goalToolOutput struct {
	OK      bool          `json:"ok"`
	Goal    *goalpkg.Goal `json:"goal,omitempty"`
	Summary string        `json:"summary"`
	Hint    string        `json:"hint,omitempty"`
}

func (e *GoalEnforcer) createGoalTool() tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, in createGoalInput) (goalToolOutput, error) {
			inv, _ := agent.InvocationFromContext(ctx)
			g, err := e.store.Create(goalScope(inv), in.Title, in.Description, in.AcceptanceCriteria)
			if err != nil {
				return goalToolOutput{}, err
			}
			return goalToolOutput{
				OK:      true,
				Goal:    g,
				Summary: g.Render(),
				Hint: "目标已建立，状态 in_progress。现在开始干活；" +
					"完成后必须调用 " + ToolUpdateGoal + " 把状态改为 complete 或 blocked，否则无法结束本轮。",
			}, nil
		},
		function.WithName(ToolCreateGoal),
		function.WithDescription("建立本轮的目标契约：写下要达成什么（title）、背景说明（description）"+
			"以及怎么算达成（acceptance_criteria 验收标准清单）。"+
			"必须在开始干活之前调用；重复调用会覆盖旧目标（用于开启新任务）。"+
			"建立后目标状态为 in_progress，只有改为 complete 或 blocked 才允许给出最终答复。"),
	)
}

func (e *GoalEnforcer) getGoalTool() tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, _ struct{}) (goalToolOutput, error) {
			inv, _ := agent.InvocationFromContext(ctx)
			g, err := e.store.Get(goalScope(inv))
			if errors.Is(err, goalpkg.ErrNotFound) {
				return goalToolOutput{
					OK:      false,
					Summary: "当前没有目标契约。",
					Hint:    "请先调用 " + ToolCreateGoal + " 建立目标。",
				}, nil
			}
			if err != nil {
				return goalToolOutput{}, err
			}
			return goalToolOutput{OK: true, Goal: g, Summary: g.Render()}, nil
		},
		function.WithName(ToolGetGoal),
		function.WithDescription("读取当前目标契约（标题、验收标准、状态、进展、阻塞原因）。"+
			"不确定还剩什么没做时调用它，不要凭记忆臆测。"),
	)
}

func (e *GoalEnforcer) updateGoalTool() tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, in updateGoalInput) (goalToolOutput, error) {
			inv, _ := agent.InvocationFromContext(ctx)
			patch, err := buildGoalPatch(in)
			if err != nil {
				return goalToolOutput{}, err
			}
			g, err := e.store.Update(goalScope(inv), patch)
			if errors.Is(err, goalpkg.ErrNotFound) {
				return goalToolOutput{}, fmt.Errorf("%s: 尚未建立目标，请先调用 %s", ToolUpdateGoal, ToolCreateGoal)
			}
			if err != nil {
				return goalToolOutput{}, err
			}
			out := goalToolOutput{OK: true, Goal: g, Summary: g.Render()}
			if g.IsOpen() {
				out.Hint = "目标仍未收敛，请继续推进；此时给出最终答复会被系统拦截。"
			} else {
				out.Hint = "目标已收敛（" + string(g.Status) + "），现在可以向用户给出最终答复。"
			}
			return out, nil
		},
		function.WithName(ToolUpdateGoal),
		function.WithDescription("更新目标契约：progress 汇报进展；"+
			"status 取值 pending / in_progress / complete / blocked——"+
			"全部验收标准满足时填 complete；被自己无法解决的客观因素卡住时填 blocked "+
			"并在 blocker 中写明缺什么。留空的字段保持不变。"),
	)
}

// buildGoalPatch 把工具入参转换为领域层 Patch（空串=不修改）。
func buildGoalPatch(in updateGoalInput) (goalpkg.Patch, error) {
	var p goalpkg.Patch
	if s := strings.TrimSpace(in.Status); s != "" {
		st, err := goalpkg.ParseStatus(s)
		if err != nil {
			return p, err
		}
		p.Status = &st
	}
	if in.Progress != "" {
		v := in.Progress
		p.Progress = &v
	}
	if in.Blocker != "" {
		v := in.Blocker
		p.Blocker = &v
	}
	return p, nil
}
