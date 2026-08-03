// Package codeagent 提供 CodeAgent 的「子代理工厂」（M1-08）。
//
// 目录名 internal/agent 与包名 codeagent 故意不同：框架已有 `agent` 包
// （trpc.group/trpc-go/trpc-agent-go/agent），本包若同名会与之冲突，
// 故沿用 internal/tool → package codectool 的同款约定，导入时写：
//
//	codeagent "github.com/ayanmw/multiagent2/server/internal/agent"
//
// 职责：
//   - 定义各角色子代理（Coder / 后续 Reviewer）的构造工厂，统一注入指令与工具集；
//   - 用框架 tool/agent（agenttool）把子代理包装成「可被父代理委托调用的工具」；
//   - 构造 Orchestrator 根代理：自身不带写工具，任何代码落地都必须委托给 Coder。
//
// 安全约束（见 LEARNINGS）：
//   - Coder 的代码工具集来自 codectool.NewCodeAct，内部已用 executor.SafeExecutor
//     包装危险命令策略（无人值守默认 deny），禁止绕过；
//   - 文件类工具的路径被约束在 Workdir 内（resolveSafePath），越界一律拒绝。
//
// 框架 API 收敛：本包与 internal/engine 是仅有的两处直连 trpc-agent-go 的地方。
package codeagent

import (
	"errors"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	agenttool "trpc.group/trpc-go/trpc-agent-go/tool/agent"

	codectool "github.com/ayanmw/multiagent2/server/internal/tool"
	"github.com/ayanmw/multiagent2/server/internal/artifact"
)

// 角色名称。agenttool 会把子代理名字直接作为工具名下发给 LLM，
// 故必须满足 ^[a-zA-Z0-9_-]+$（部分模型 API 对工具名有严格校验）。
const (
	// RoleOrchestrator 是编排者（根代理）的名称。
	RoleOrchestrator = "orchestrator"
	// RoleCoder 是负责实际落地代码改动的子代理名称。
	RoleCoder = "coder"
)

// CoderInstruction 是 Coder 子代理的系统提示词：强调「必须真正调用工具」。
const CoderInstruction = "你是 Coder 子代理，负责在受限的工作目录内真正落地代码改动。" +
	"你可以使用 shell_exec（执行命令）、file_read（读文件）、file_write（写文件）、file_edit（改文件）工具。" +
	"工作区已自动 git init（若尚未提交过），你还可以使用 git_status/git_diff/git_commit/git_log/git_branch " +
	"工具对改动进行版本管理：完成一批改动后调用 git_commit 提交（message 写明本次改了什么），" +
	"提交前可用 git_status/git_diff 确认改动范围。" +
	"收到任务后必须实际调用工具完成改动，不要只给出计划或代码片段而不执行。" +
	"完成后用一段简短中文说明你做了什么（改动了哪些文件、执行了哪些命令、结果如何）。" +
	"若命令被安全策略拒绝，请如实说明并给出更安全的替代方案。"

// OrchestratorInstruction 是 Orchestrator 根代理的系统提示词：强调「委托而非自己动手」。
const OrchestratorInstruction = "你是 Orchestrator（编排者），负责理解用户目标、拆解任务，" +
	"并把「需要真正修改文件或执行命令」的工作委托给 " + RoleCoder + " 子代理工具。" +
	"你自己没有写文件与执行命令的能力：任何代码落地都必须调用 " + RoleCoder + " 工具，" +
	"并在 request 参数中给出清晰、自足的任务描述（包含目标文件路径与期望内容/行为）。" +
	"子代理返回后，请核对其结果，必要时再次委托修正；最后用简洁中文向用户汇报最终结果。"

// coderToolDescription 是 Coder 作为工具暴露给 Orchestrator 时的描述。
const coderToolDescription = "把需要实际改动代码的任务委托给 Coder 子代理执行。" +
	"Coder 拥有 shell 执行与文件读写编辑能力，会在受限工作目录内真正落地改动。" +
	"request 参数应为完整、自足的任务描述（包含文件路径与期望内容）。"

// ErrNilModel 表示未注入框架模型实例。
var ErrNilModel = errors.New("codeagent: Model 不能为空")

// ErrEmptyWorkdir 表示未指定子代理的受限工作目录。
var ErrEmptyWorkdir = errors.New("codeagent: Workdir 不能为空")

// Deps 描述构造子代理所需的依赖，由 engine 层注入（业务层不直连框架）。
type Deps struct {
	// Model 是框架模型实例（如 openai.New(...) 的返回值）。
	Model model.Model
	// Workdir 是代码工具集的受限工作目录（通常为 workspace 的本地目录）。
	Workdir string
	// ExtraTools 是追加给 Orchestrator 的公共工具（如 echo/get_time）。
	// 不会下发给 Coder —— Coder 的工具集由本包固定装配，避免越权。
	ExtraTools []tool.Tool
	// Guardrail 是护栏熔断预算（M1-13）：LLM 调用数 / 工具迭代轮数 / 工具重试。
	// 零值 = 按默认预算启用（无人值守必须有兜底）；由 NewTeam 统一从 TeamConfig 下发，
	// 使 Orchestrator 与各子代理共用同一套预算。
	Guardrail GuardrailConfig
	// StateStore 是「工作状态外置」的存储后端（M1-16）。仅 Orchestrator 使用，
	// 不下发给 Coder/Reviewer——维护 run 级状态文件是编排者的职责，避免子代理并发写冲突。
	// 为空时 NewTeam 不安装 StateEnforcer（状态外置功能关闭）。
	StateStore artifact.Store
}

// validate 校验依赖完整性。
func (d Deps) validate() error {
	if d.Model == nil {
		return ErrNilModel
	}
	if d.Workdir == "" {
		return ErrEmptyWorkdir
	}
	return nil
}

// NewCoder 构造 Coder 子代理：带完整 CodeAct 工具集（shell_exec/file_read/file_write/file_edit）。
// 工具内部已经过危险命令策略与路径边界约束（见 codectool.NewCodeAct）。
func NewCoder(d Deps) (agent.Agent, error) {
	if err := d.validate(); err != nil {
		return nil, err
	}
	tools, err := codectool.NewCodeAct(d.Workdir)
	if err != nil {
		return nil, err
	}
	// M2-01：Coder 同时持有 Git 工具集（git_status/git_diff/git_commit/git_log/git_branch），
	// 使其在完成代码改动后能显式提交到 workspace 的 git 仓库。
	gitTools, gerr := codectool.NewGitTools(d.Workdir)
	if gerr != nil {
		return nil, gerr
	}
	tools = append(tools, gitTools...)
	opts := []llmagent.Option{
		llmagent.WithModel(d.Model),
		llmagent.WithDescription("在受限工作目录内实际落地代码改动的子代理（可执行命令、读写与编辑文件，并提交 git）"),
		llmagent.WithInstruction(CoderInstruction),
		llmagent.WithTools(tools),
	}
	// 护栏熔断（M1-13）：Coder 子代理同样受 LLM 调用数 / 工具迭代数约束，
	// 避免陷入死循环把无人工值守的 24h 循环卡死。
	opts = append(opts, d.Guardrail.Options()...)
	return llmagent.New(RoleCoder, opts...), nil
}

// AsTool 把任意子代理包装成可被父代理调用的工具（框架 agenttool）。
// 工具名取子代理的 Info().Name，故子代理命名必须符合 ^[a-zA-Z0-9_-]+$。
// description 为空时沿用子代理自身的 Description。
func AsTool(sub agent.Agent, description string) tool.Tool {
	if description == "" {
		return agenttool.NewTool(sub)
	}
	return agenttool.NewTool(sub, agenttool.WithDescription(description))
}

// NewCoderTool 构造「可被委托的 Coder」：先建 Coder 子代理，再包成 agenttool。
func NewCoderTool(d Deps) (tool.Tool, error) {
	coder, err := NewCoder(d)
	if err != nil {
		return nil, err
	}
	return AsTool(coder, coderToolDescription), nil
}

// NewOrchestrator 构造 Orchestrator 根代理（M1-08，不含 Reviewer）。
//
// 关键设计：Orchestrator **不直接持有** shell/文件写工具，只持有
//   - Deps.ExtraTools（公共只读/无副作用工具，如 echo/get_time）
//   - coder 委托工具（agenttool 包装的 Coder 子代理）
//
// 这样「产出代码」的权限被收敛到子代理内。M1-09 起团队构成由 TeamConfig
// 决定（见 team.go 的 NewTeam）；本函数等价于「只有 Coder 的团队」，保留
// 供向后兼容与单元测试使用。
func NewOrchestrator(d Deps) (agent.Agent, error) {
	return NewTeam(d, TeamConfig{EnableSubAgents: true})
}
