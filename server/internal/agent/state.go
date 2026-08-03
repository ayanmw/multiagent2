// state.go 实现 M1-16「工作状态外置」在框架侧的落地：
//
//	read_state / update_plan / update_progress / append_learning 四个工具 + StateEnforcer 扩展。
//
// 设计要点：
//   - 这是 Loop Engineering 的「状态记忆」：长任务（Goal 循环 / Plan-Execute / 24h 自主
//     Loop）把 PLAN.md / PROGRESS.md / LEARNINGS.md 维护为 artifact，存到可跨进程存活的
//     存储（默认落盘 FileStore），使「中断 / 重启后续跑」能接上（M1-16 验收标准）；
//   - StateEnforcer 装在根 Agent（单代理或 Orchestrator）上，子代理（Coder/Reviewer）
//     不装——维护 run 级状态是编排者的职责，避免多个子代理并发写同一份状态文件互相踩踏；
//   - beforeModel 在「本轮第一次模型调用」时读取该 session 的既有状态文件，若存在则把
//     其摘要作为一条用户消息回灌，让模型「先读再续跑」，而不是凭空重来；
//   - 工具的写入经 artifact.Store 落盘，与 docs/loop/ 控制文件严格隔离（见 LEARNINGS）。
//
// 参考实现：与 goal.go / plan.go 同款（对齐框架内置 agent/extension/todoenforcer）。
package codeagent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/extension"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"

	"github.com/ayanmw/multiagent2/server/internal/artifact"
)

// 状态外置工具名（须匹配 ^[a-zA-Z0-9_-]+$）。
const (
	ToolReadState      = "read_state"
	ToolUpdatePlan     = "update_plan"
	ToolUpdateProgress = "update_progress"
	ToolAppendLearning = "append_learning"
)

// StateExtensionName 是扩展在框架中的唯一名称（去重与错误包装用）。
const StateExtensionName = "state-externalization"

// invocation 状态键：本扩展只在「每轮 run 的第一次模型调用」时回灌一次续跑上下文。
const stateKeyStateChecked = "state:checked"

// StateInstruction 是注入根 Agent 的状态外置作业规程（短提示，工具描述自解释）。
var StateInstruction = "\n\n【工作状态外置（续跑用）】" +
	"\n处理跨多轮 / 可能被中断的长任务时，用以下工具维护可恢复的工作状态：" +
	"\n- " + ToolUpdatePlan + "：把整体计划 / 目标写入 PLAN.md（覆盖写）；" +
	"\n- " + ToolUpdateProgress + "：每完成一步就把进展追加到 PROGRESS.md；" +
	"\n- " + ToolAppendLearning + "：把踩到的坑 / 约定沉淀进 LEARNINGS.md；" +
	"\n- " + ToolReadState + "：需要回顾当前进度或续跑前调用它读回全部状态。" +
	"\n这些状态会存盘，下次 run（含进程重启）会自动回灌给你，使你能接着上次的位置继续，而不是从头开始。"

// StateEnforcer 是「工作状态外置」扩展：贡献四个状态工具，并在 run 开头回灌既有状态。
type StateEnforcer struct {
	store artifact.Store
	tools []tool.Tool
}

// 编译期接口断言。
var _ extension.Extension = (*StateEnforcer)(nil)

// StateOption 配置 StateEnforcer。
type StateOption func(*StateEnforcer)

// WithStateStore 注入 artifact 存储（落盘，跨重启续跑）；为空时退化为内存存储（不持久）。
func WithStateStore(s artifact.Store) StateOption {
	return func(e *StateEnforcer) {
		if s != nil {
			e.store = s
		}
	}
}

// NewStateEnforcer 构造「工作状态外置」扩展。
func NewStateEnforcer(opts ...StateOption) *StateEnforcer {
	e := &StateEnforcer{store: artifact.NewMemoryStore()}
	for _, o := range opts {
		if o != nil {
			o(e)
		}
	}
	e.tools = []tool.Tool{
		e.readStateTool(),
		e.updatePlanTool(),
		e.updateProgressTool(),
		e.appendLearningTool(),
	}
	return e
}

// Store 暴露内部存储，供上层（测试 / 可观测）读取。
func (e *StateEnforcer) Store() artifact.Store { return e.store }

// Tools 返回扩展贡献的四个状态工具（供不走 extension 装配路径的调用方复用）。
func (e *StateEnforcer) Tools() []tool.Tool {
	if e == nil {
		return nil
	}
	return append([]tool.Tool(nil), e.tools...)
}

// Name 实现 extension.Extension。
func (e *StateEnforcer) Name() string { return StateExtensionName }

// Register 实现 extension.Extension：贡献工具 + 注册 BeforeModel 回灌回调（无需 AfterModel）。
func (e *StateEnforcer) Register(r *extension.Registry) {
	if r == nil {
		return
	}
	r.Tools(e.tools...)
	r.BeforeModel(e.beforeModel)
}

// invocationScope 从上下文取出状态作用域键（与 goal/plan 同款：session 优先）。
func (e *StateEnforcer) invocationScope(ctx context.Context) string {
	inv, _ := agent.InvocationFromContext(ctx)
	return goalScope(inv)
}

// beforeModel 在「本轮第一次模型调用」读取该 session 的既有状态文件，
// 若存在则把摘要作为用户消息回灌，让模型先读再续跑。
func (e *StateEnforcer) beforeModel(
	ctx context.Context,
	args *model.BeforeModelArgs,
) (*model.BeforeModelResult, error) {
	if args == nil || args.Request == nil {
		return nil, nil
	}
	inv, _ := agent.InvocationFromContext(ctx)
	if inv != nil {
		// 每轮 run 只回灌一次；用 invocation 状态标记避免后续模型调用重复注入。
		if v, _ := agent.GetStateValue[bool](inv, stateKeyStateChecked); v {
			return nil, nil
		}
		inv.SetState(stateKeyStateChecked, true)
	}

	scope := goalScope(inv)
	snap, err := e.store.Snapshot(scope)
	if err != nil || !snap.Any {
		return nil, nil
	}
	msg := buildResumePrompt(snap)
	if msg == "" {
		return nil, nil
	}
	args.Request.Messages = append(args.Request.Messages, model.NewUserMessage(msg))
	return nil, nil
}

// buildResumePrompt 把既有状态文件摘要成「续跑上下文」消息。
func buildResumePrompt(snap artifact.Snapshot) string {
	var b strings.Builder
	b.WriteString("[系统·续跑上下文] 检测到上次 run 留下的工作状态，请基于它接着推进，不要从头开始。\n")
	if strings.TrimSpace(snap.Plan) != "" {
		b.WriteString("\n## PLAN.md\n")
		b.WriteString(snap.Plan)
	}
	if strings.TrimSpace(snap.Progress) != "" {
		b.WriteString("\n## PROGRESS.md\n")
		b.WriteString(snap.Progress)
	}
	if strings.TrimSpace(snap.Learnings) != "" {
		b.WriteString("\n## LEARNINGS.md\n")
		b.WriteString(snap.Learnings)
	}
	b.WriteString("\n（如需回顾完整内容，可调用 " + ToolReadState + "。）")
	return b.String()
}

// ---------------------------------------------------------------------------
// 工具
// ---------------------------------------------------------------------------

// stateProgressInput 是 update_progress 的入参。
type stateProgressInput struct {
	Text string `json:"text"`
}

// statePlanInput 是 update_plan 的入参。
type statePlanInput struct {
	Text string `json:"text"`
}

// stateLearningInput 是 append_learning 的入参。
type stateLearningInput struct {
	Text string `json:"text"`
}

// stateToolOutput 是写类工具的返回体。
type stateToolOutput struct {
	OK       bool   `json:"ok"`
	Artifact string `json:"artifact,omitempty"`
	Content  string `json:"content,omitempty"`
	Summary  string `json:"summary"`
}

// stateReadOutput 是 read_state 的返回体。
type stateReadOutput struct {
	Exists    bool   `json:"exists"`
	Plan      string `json:"plan,omitempty"`
	Progress  string `json:"progress,omitempty"`
	Learnings string `json:"learnings,omitempty"`
	Summary   string `json:"summary"`
}

func (e *StateEnforcer) readStateTool() tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, _ struct{}) (stateReadOutput, error) {
			scope := e.invocationScope(ctx)
			snap, err := e.store.Snapshot(scope)
			if err != nil {
				return stateReadOutput{}, err
			}
			if !snap.Any {
				return stateReadOutput{
					Exists:  false,
					Summary: "当前没有可续跑的状态文件（PLAN/PROGRESS/LEARNINGS）。",
				}, nil
			}
			return stateReadOutput{
				Exists:    true,
				Plan:      snap.Plan,
				Progress:  snap.Progress,
				Learnings: snap.Learnings,
				Summary:   "已读取上次工作状态，可据此续跑。",
			}, nil
		},
		function.WithName(ToolReadState),
		function.WithDescription("读取当前 run 的工作状态文件：PLAN.md（计划/目标）、"+
			"PROGRESS.md（进展日志）、LEARNINGS.md（踩坑与约定）。"+
			"被中断后续跑、或忘记做到哪了时调用它，不要凭记忆臆测。"),
	)
}

func (e *StateEnforcer) updatePlanTool() tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, in statePlanInput) (stateToolOutput, error) {
			scope := e.invocationScope(ctx)
			content := strings.TrimSpace(in.Text)
			if content == "" {
				return stateToolOutput{}, fmt.Errorf("%s: text 不能为空", ToolUpdatePlan)
			}
			if !strings.HasPrefix(content, "#") {
				content = "# 计划\n\n" + content
			}
			if err := e.store.Write(scope, artifact.PlanArtifact, content); err != nil {
				return stateToolOutput{}, err
			}
			return stateToolOutput{
				OK:       true,
				Artifact: artifact.PlanArtifact,
				Content:  content,
				Summary:  "计划已写入 PLAN.md。",
			}, nil
		},
		function.WithName(ToolUpdatePlan),
		function.WithDescription("把整体计划 / 目标写入 PLAN.md（覆盖写）。"+
			"长任务开工时建立计划，目标有调整时重新调用覆盖。"),
	)
}

func (e *StateEnforcer) updateProgressTool() tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, in stateProgressInput) (stateToolOutput, error) {
			scope := e.invocationScope(ctx)
			text := strings.TrimSpace(in.Text)
			if text == "" {
				return stateToolOutput{}, fmt.Errorf("%s: text 不能为空", ToolUpdateProgress)
			}
			entry := fmt.Sprintf("- %s %s\n", time.Now().Format("2006-01-02 15:04:05"), text)
			existing, ok, err := e.store.Read(scope, artifact.ProgressArtifact)
			if err != nil {
				return stateToolOutput{}, err
			}
			var content string
			if ok {
				content = existing + entry
			} else {
				content = "# 进展日志\n\n" + entry
			}
			if err := e.store.Write(scope, artifact.ProgressArtifact, content); err != nil {
				return stateToolOutput{}, err
			}
			return stateToolOutput{
				OK:       true,
				Artifact: artifact.ProgressArtifact,
				Content:  content,
				Summary:  "进展已追加到 PROGRESS.md。",
			}, nil
		},
		function.WithName(ToolUpdateProgress),
		function.WithDescription("把一条进展记录追加到 PROGRESS.md（工作状态文件）。"+
			"长任务每完成一步或遇到关键节点都调用它，使进度在进程重启 / 中断后仍能续跑。"+
			"text 是这次做了什么、结果如何的简短说明。"),
	)
}

func (e *StateEnforcer) appendLearningTool() tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, in stateLearningInput) (stateToolOutput, error) {
			scope := e.invocationScope(ctx)
			text := strings.TrimSpace(in.Text)
			if text == "" {
				return stateToolOutput{}, fmt.Errorf("%s: text 不能为空", ToolAppendLearning)
			}
			entry := fmt.Sprintf("- %s\n", text)
			existing, ok, err := e.store.Read(scope, artifact.LearningsArtifact)
			if err != nil {
				return stateToolOutput{}, err
			}
			var content string
			if ok {
				content = existing + entry
			} else {
				content = "# 踩坑与约定\n\n" + entry
			}
			if err := e.store.Write(scope, artifact.LearningsArtifact, content); err != nil {
				return stateToolOutput{}, err
			}
			return stateToolOutput{
				OK:       true,
				Artifact: artifact.LearningsArtifact,
				Content:  content,
				Summary:  "约定已沉淀到 LEARNINGS.md。",
			}, nil
		},
		function.WithName(ToolAppendLearning),
		function.WithDescription("把一条踩坑 / 约定沉淀进 LEARNINGS.md（工作状态文件）。"+
			"遇到有价值的环境坑、接口约定、用户偏好时记下，供后续 run 复用。"+
			"text 是这条经验 / 约定的简短说明。"),
	)
}

// StateInstructionText 暴露状态外置作业规程文本，便于在团队指令中追加。
func StateInstructionText() string { return StateInstruction }
