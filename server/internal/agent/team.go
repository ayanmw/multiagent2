// team.go 实现 CodeTeam 编排（M1-09）：Orchestrator → Coder（写）→ Reviewer（只读）→ 回环。
//
// 设计要点：
//   - 团队构成「配置化」：由 TeamConfig 决定是否启用子代理委托、是否加入 Reviewer、
//     以及「实现→审阅→修复」最多回环几轮，配置从 config（env）经 engine 注入，
//     不在代码里写死；
//   - 权限分层：Orchestrator 无写工具（只能委托）、Coder 持完整 CodeAct 工具集、
//     Reviewer 只持只读工具（独立视角挑错，不能改动代码）；
//   - 串行驱动：M1 阶段 Coder/Reviewer 由 Orchestrator 串行委托（同一 workspace），
//     并行/worktree 隔离留待 M2（见 docs/03 §2.1 决策 3）。
package codeagent

import (
	"errors"
	"fmt"
	"strconv"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	goalpkg "github.com/ayanmw/multiagent2/server/internal/goal"
	planpkg "github.com/ayanmw/multiagent2/server/internal/plan"
	codectool "github.com/ayanmw/multiagent2/server/internal/tool"
)

// RoleReviewer 是只读审阅子代理的名称（同样会成为工具名，须匹配 ^[a-zA-Z0-9_-]+$）。
const RoleReviewer = "reviewer"

// DefaultMaxReviewRounds 是「实现 → 审阅 → 修复」默认最多回环轮数。
// 仅作为写进 Orchestrator 指令的软约束；硬性熔断（LLM 调用数/工具迭代数）见 M1-13。
const DefaultMaxReviewRounds = 2

// ErrNoReadOnlyTools 表示未能为 Reviewer 装配任何只读工具（工具名约定被改动时会触发）。
var ErrNoReadOnlyTools = errors.New("codeagent: 未能装配任何只读工具，Reviewer 无法工作")

// ReviewerInstruction 是 Reviewer 子代理的系统提示词：只读审阅、独立视角、必须给结论。
const ReviewerInstruction = "你是 Reviewer（代码审阅者）子代理，只拥有只读工具：" +
	"file_read（读取文件）与 grep（按正则检索代码）。" +
	"你没有写文件与执行命令的能力——file_write / file_edit / shell_exec 不会下发给你，" +
	"强行调用会被直接拒绝；需要改动时只能把问题写进结论，交由 Coder 落地。" +
	"你的任务是以独立视角审阅 Coder 的改动并挑出问题：先用 grep 定位相关代码、" +
	"用 file_read 读取具体文件，再依据事实给出意见，不要臆测文件内容。" +
	"输出格式：第一行给出结论「通过」或「需修改」；若为「需修改」，随后逐条列出具体问题" +
	"（问题所在文件与位置、为什么是问题、建议怎么改）。发现任何问题都必须明确指出，不要客套。"

// reviewerToolDescription 是 Reviewer 作为工具暴露给 Orchestrator 时的描述。
const reviewerToolDescription = "把「审阅代码改动」的任务委托给 Reviewer 子代理。" +
	"Reviewer 只有只读能力（file_read 读取文件、grep 检索代码），无法修改文件或执行命令，" +
	"会独立检查改动是否正确、完整、符合要求，并返回「通过」或「需修改 + 问题清单」。" +
	"request 参数应说明要审阅什么（文件路径与验收要求）。"

// TeamConfig 描述 CodeTeam 的可配置项（M1-09「team 配置化」）。
// 零值表示单代理模式（与 M1-06/07 行为一致）。
type TeamConfig struct {
	// EnableSubAgents 开启子代理委托模式：根 Agent 为 Orchestrator，代码落地委托 Coder。
	EnableSubAgents bool
	// EnableReviewer 在团队中加入 Reviewer（只读审阅者），形成「实现→审阅→修复」回环。
	// 仅在 EnableSubAgents=true 时生效。
	EnableReviewer bool
	// MaxReviewRounds 是回环轮数上限；<=0 时取 DefaultMaxReviewRounds。
	MaxReviewRounds int
	// EnableGoal 开启目标契约（M1-11）：为 Orchestrator 注入
	// create_goal/get_goal/update_goal 三个工具，并强制「目标未达成不许结束」。
	// 仅在 EnableSubAgents=true 时生效——契约只装根编排者，子代理不装。
	EnableGoal bool
	// MaxGoalNudges 是「最多拦截几次过早的最终答复」；<=0 时取 DefaultMaxGoalNudges。
	// 超出后放行（fail-open），避免模型不配合时把整轮 Run 卡死。
	MaxGoalNudges int
	// GoalStore 可选注入目标存储（测试与上层可观测用）；为空时内部自建。
	GoalStore *goalpkg.Store
	// EnablePlan 开启 Plan-Execute 循环（M1-12）：为 Orchestrator 注入
	// create_plan/get_plan/update_step/add_steps 四个工具，并强制「计划未做完不许结束」。
	// 仅在 EnableSubAgents=true 时生效——契约只装根编排者，子代理不装。
	// 与 M1-11 目标契约分工：Goal 管「要不要收工」，Plan 管「怎么一步步干」，二者可叠加。
	EnablePlan bool
	// MaxPlanNudges 是「最多拦截几次计划未做完就收工的答复」；<=0 时取 DefaultMaxPlanNudges。
	// 超出后放行（fail-open），避免模型不配合时把整轮 Run 卡死。
	MaxPlanNudges int
	// PlanStore 可选注入计划存储（测试与上层可观测用）；为空时内部自建。
	PlanStore *planpkg.Store
}

// normalized 返回补齐默认值后的配置副本。
func (c TeamConfig) normalized() TeamConfig {
	if c.MaxReviewRounds <= 0 {
		c.MaxReviewRounds = DefaultMaxReviewRounds
	}
	if c.MaxGoalNudges <= 0 {
		c.MaxGoalNudges = DefaultMaxGoalNudges
	}
	if c.MaxPlanNudges <= 0 {
		c.MaxPlanNudges = DefaultMaxPlanNudges
	}
	return c
}

// reviewerEnabled 报告是否应当装配 Reviewer（依赖子代理模式已开启）。
func (c TeamConfig) reviewerEnabled() bool {
	return c.EnableSubAgents && c.EnableReviewer
}

// goalEnabled 报告是否应当装配目标契约（同样依赖子代理模式已开启）。
func (c TeamConfig) goalEnabled() bool {
	return c.EnableSubAgents && c.EnableGoal
}

// planEnabled 报告是否应当装配 Plan-Execute 循环（同样依赖子代理模式已开启）。
func (c TeamConfig) planEnabled() bool {
	return c.EnableSubAgents && c.EnablePlan
}

// ReadOnlyTools 返回 Reviewer 可用的只读工具集（file_read + grep，M1-10）。
//
// M1-09 时的实现是「从 CodeAct 工具集按白名单过滤」；M1-10 起改为直接调用
// codectool.ReadOnlyTools **独立构造**——该路径下根本不会创建 Executor，
// 从结构上杜绝执行能力，也不存在「过滤漏网」的可能。返回前再用
// codectool.EnsureReadOnly 做一次兜底断言（fail fast）。
func ReadOnlyTools(workdir string) ([]tool.Tool, error) {
	if workdir == "" {
		return nil, ErrEmptyWorkdir
	}
	ro, err := codectool.ReadOnlyTools(workdir)
	if err != nil {
		return nil, err
	}
	if len(ro) == 0 {
		return nil, ErrNoReadOnlyTools
	}
	if err := codectool.EnsureReadOnly(ro); err != nil {
		return nil, err
	}
	return ro, nil
}

// NewReviewer 构造 Reviewer 子代理：仅持有只读工具，无 file_write/file_edit/shell_exec。
//
// 注意 Deps.ExtraTools **不会**下发给 Reviewer：额外工具由调用方（engine）注入，
// 无法保证其只读性，若混入写/执行工具会破坏 M1-10 的只读约束。
func NewReviewer(d Deps) (agent.Agent, error) {
	if err := d.validate(); err != nil {
		return nil, err
	}
	tools, err := ReadOnlyTools(d.Workdir)
	if err != nil {
		return nil, err
	}
	return llmagent.New(RoleReviewer,
		llmagent.WithModel(d.Model),
		llmagent.WithDescription("以独立视角审阅代码改动的只读子代理（只能读取与检索文件，不能修改或执行）"),
		llmagent.WithInstruction(ReviewerInstruction),
		llmagent.WithTools(tools),
	), nil
}

// NewReviewerTool 构造「可被委托的 Reviewer」：先建 Reviewer 子代理，再包成 agenttool。
func NewReviewerTool(d Deps) (tool.Tool, error) {
	reviewer, err := NewReviewer(d)
	if err != nil {
		return nil, err
	}
	return AsTool(reviewer, reviewerToolDescription), nil
}

// teamInstruction 依据团队配置生成 Orchestrator 的系统提示词。
// 未启用 Reviewer 时退回 M1-08 的纯委托指令，保证行为向后兼容；
// 启用目标契约（M1-11）时再追加目标契约作业规程。
func teamInstruction(cfg TeamConfig) string {
	base := OrchestratorInstruction
	if cfg.reviewerEnabled() {
		base += reviewLoopInstruction(cfg)
	}
	if cfg.goalEnabled() {
		base += GoalInstruction
	}
	if cfg.planEnabled() {
		base += PlanInstruction
	}
	return base
}

// reviewLoopInstruction 生成「实现→审阅→修复」回环的作业规程（M1-09）。
func reviewLoopInstruction(cfg TeamConfig) string {
	return "\n\n【团队协作流程（必须遵守）】" +
		"\n1. 需求落地：先调用 " + RoleCoder + " 工具，让它真正修改文件或执行命令；" +
		"\n2. 独立审阅：Coder 返回后，必须调用 " + RoleReviewer + " 工具审阅其改动" +
		"（在 request 中写明改了哪些文件、验收要求是什么）；" +
		"\n3. 回环修复：若 Reviewer 给出「需修改」，把它列出的问题原样转交给 " + RoleCoder + " 修复，" +
		"修复后再次调用 " + RoleReviewer + " 复审；" +
		"\n4. 收敛：直到 Reviewer 判定「通过」，或回环达到 " + strconv.Itoa(cfg.MaxReviewRounds) +
		" 轮为止；达到上限仍未通过时，如实向用户说明剩余问题，不要假装完成。" +
		"\n注意：Reviewer 只有只读能力，绝不要让它去改文件；改动一律交给 " + RoleCoder + "。"
}

// NewTeam 依据 TeamConfig 构造 CodeTeam 的根代理（M1-09）。
//
// 配置语义：
//   - EnableSubAgents=false：调用方不应使用本函数（应走单代理路径），这里直接报错，
//     避免静默产出一个「没有委托能力」的编排者；
//   - EnableReviewer=false：等价于 M1-08 的 Orchestrator + Coder；
//   - EnableReviewer=true：Orchestrator 同时持有 coder / reviewer 两个委托工具，
//     并在指令中约定「实现→审阅→修复」的回环及轮数上限；
//   - EnableGoal=true（M1-11）：为 Orchestrator 装配目标契约扩展
//     （create_goal/get_goal/update_goal + 未达成不许结束的硬拦截）。
//     契约只装根编排者：子代理各自只对「被委托的子任务」负责，
//     若也装契约会互相拦截，且与并行工具冲突（见 docs/03 §2.1）。
func NewTeam(d Deps, cfg TeamConfig) (agent.Agent, error) {
	if err := d.validate(); err != nil {
		return nil, err
	}
	cfg = cfg.normalized()
	if !cfg.EnableSubAgents {
		return nil, fmt.Errorf("codeagent: NewTeam 需要 TeamConfig.EnableSubAgents=true")
	}

	coderTool, err := NewCoderTool(d)
	if err != nil {
		return nil, err
	}

	tools := make([]tool.Tool, 0, len(d.ExtraTools)+2)
	tools = append(tools, d.ExtraTools...)
	tools = append(tools, coderTool)

	if cfg.reviewerEnabled() {
		reviewerTool, rerr := NewReviewerTool(d)
		if rerr != nil {
			return nil, rerr
		}
		tools = append(tools, reviewerTool)
	}

	opts := []llmagent.Option{
		llmagent.WithModel(d.Model),
		llmagent.WithDescription("负责目标拆解、子代理委托与「实现→审阅→修复」回环的编排者"),
		llmagent.WithInstruction(teamInstruction(cfg)),
		llmagent.WithTools(tools),
	}
	if cfg.goalEnabled() {
		// 注意：装了 goal 契约的 Agent 不能开启 EnableParallelTools——
		// 并行工具会让「工具调用响应 / 最终响应」的时序变得不可判定，
		// AfterModel 无法可靠区分「还在干活」与「过早收工」（见 docs/03 §2.1）。
		opts = append(opts, llmagent.WithExtensions(NewGoalEnforcer(
			WithGoalMaxNudges(cfg.MaxGoalNudges),
			WithGoalStore(cfg.GoalStore),
		)))
	}
	if cfg.planEnabled() {
		// Plan-Execute 与目标契约同款约束：不开并行工具，保证 AfterModel 能可靠判定
		// 「还在执行计划」与「过早收工」。二者都装根编排者，回调用安装顺序短路，
		// 可叠加：先要求立目标，再要求把计划做完（见 plan.go 顶部说明）。
		opts = append(opts, llmagent.WithExtensions(NewPlanEnforcer(
			WithPlanMaxNudges(cfg.MaxPlanNudges),
			WithPlanStore(cfg.PlanStore),
		)))
	}
	return llmagent.New(RoleOrchestrator, opts...), nil
}
