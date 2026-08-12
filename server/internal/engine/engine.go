// Package engine 封装 trpc-agent-go 的 Runner/LLMAgent，作为 Agent 对话引擎层。
// 设计目标：把框架 API 收敛在此包内，框架版本升级时只需改动本层（见 LEARNINGS）。
package engine

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	taskrunruntime "trpc.group/trpc-go/trpc-agent-go/agent/taskrun"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	taskruntool "trpc.group/trpc-go/trpc-agent-go/tool/taskrun"

	codeagent "github.com/ayanmw/multiagent2/server/internal/agent"
	"github.com/ayanmw/multiagent2/server/internal/artifact"
	"github.com/ayanmw/multiagent2/server/internal/executor"
	"github.com/ayanmw/multiagent2/server/internal/skillrepo"
	"github.com/ayanmw/multiagent2/server/internal/toolsearch"
)

// defaultInstruction 是 Agent 的系统提示词（中文优先，编程助手定位）。
const defaultInstruction = "你是一个有用的编程助手，基于 trpc-agent-go 运行。" +
	"请用简洁、准确的中文回答用户的编程与技术问题；当需要使用工具时优先调用可用工具。"

// ModelConfig 描述一次对话所需的模型连接信息。
// BaseURL 应为 OpenAI 兼容端点（含 /v1，例如 http://localhost:8080/v1）。
type ModelConfig struct {
	ModelID  string        // 上游模型 id（如 gpt-4o、qwen2.5）
	BaseURL  string        // OpenAI 兼容 base URL（含 /v1）
	APIKey   string        // 上游 API Key（本地无鉴权代理可留空）
	Protocol string        // openai / anthropic / gemini（M0-10 仅实现 openai 兼容）
	Timeout  time.Duration // 单次对话（流式）超时；<=0 时取默认 90s（由配置注入，M0.5-05）
	// Tools 是可选的额外工具集（如 M1-06 CodeAct 工具），会被追加到基础工具之后。
	// 不传则仅使用 echo/get_time 基础工具。
	Tools []tool.Tool
	// Team 是多代理编排（CodeTeam）配置（M1-08/M1-09）：
	//   - EnableSubAgents=true 时根 Agent 换成 Orchestrator，代码落地能力收敛到
	//     Coder 子代理，由框架 agenttool 委托调用；此时 Workdir 必填，Tools 仅作为
	//     Orchestrator 的附加工具（CodeAct 工具集由 codeagent 工厂装配给 Coder）；
	//   - EnableReviewer=true 时额外加入只读 Reviewer 子代理，形成
	//     「实现→审阅→修复」回环，轮数上限由 MaxReviewRounds 控制。
	// 零值 = 单代理模式（与 M1-06/07 行为一致）。
	Team TeamConfig
	// Workdir 是子代理代码工具集的受限工作目录（Team.EnableSubAgents=true 时必填）。
	Workdir string
	// Guardrail 是护栏熔断预算（M1-13），经 config 注入；Team.EnableSubAgents=true 时
	// 由 codeagent.NewTeam 下发给 Orchestrator 与各子代理共用同一套约束。
	Guardrail codeagent.GuardrailConfig
	// EnableState 开启「工作状态外置」（M1-16）：根 Agent 装上 StateEnforcer，
	// 维护 PLAN.md/PROGRESS.md/LEARNINGS.md 并落盘，进程重启/中断后续跑能接上。
	// 仅当 StateStore 非空时生效，根 Agent（单代理或 Orchestrator）持有该扩展，
	// 子代理（Coder/Reviewer）不装。
	EnableState bool
	// StateStore 是状态文件的存储后端（M1-16），经 config 注入；
	// nil 时即使 EnableState=true 也不会安装扩展（避免落空）。
	StateStore artifact.Store
	// SkillWarmStart 开启「技能 warm-start」（M2-03）：会话开始时把相关 SKILL.md
	// 注入根 Agent 系统上下文，使新会话自动带着技能知识开工。
	SkillWarmStart bool
	// SkillRoots 是 warm-start 扫描的技能根目录（共享 + 用户私有），由 api 层按 uid 拼好传入。
	SkillRoots []string
	// SkillKeywords 是可选的检索关键词（来自 workspace 或首条消息）；为空则注入全部（受长度上限约束）。
	SkillKeywords []string
	// SkillMaxChars 是 warm-start 注入内容的长度上限（控长）；<=0 时取默认 6000。
	SkillMaxChars int
	// TaskRunController 是后台任务控制器（M2-04）：框架 inprocess.Service 实现，
	// 由 cmd 层装配（含 worker 子代理工厂 + run 记录持久化）。非空时把六个后台任务
	// 控制工具挂到根 Agent（Orchestrator/单代理），使 Agent 能派生子任务并行推进。
	TaskRunController taskrunruntime.Controller
	// TaskRunSession 是后台任务 child session 的持久化 session.Service（M2-04 ①）：
	// 落盘子任务事件/transcript，进程重启后仍能读回。为空则 read_task_run_transcript 不挂载。
	TaskRunSession session.Service
	// ToolSearchEnabled 开启「延迟工具箱」（M2-06）：把 MCP 服务器工具经一对控制工具
	// tool_search（检索）/call_tool（按需调用）暴露给 Agent，默认不把全部工具的声明
	// 一次性灌进模型上下文，避免 token 随工具数线性膨胀。
	ToolSearchEnabled bool
	// ToolSearchProvider 在每次对话时按需构建延迟工具箱（默认不暴露全部工具）。
	// 返回 nil 表示当前用户没有可用工具，引擎将不挂载 tool_search/call_tool 双工具。
	ToolSearchProvider ToolSearchProvider
	// ToolSearchUserID 是当前对话归属用户，供 provider 做 owner 隔离（M2-02 MCP 配置按用户隔离）。
	ToolSearchUserID uint
	// KnowledgeRetriever 是可选的「对话前知识检索注入」（M5-02）。nil 时不检索、不做任何注入；
	// 非空时引擎在发送用户消息前调用其 Retrieve 获取该用户全部知识库的相关内容，前缀注入用户消息，
	// 丰富模型上下文（长度由 retriever 内部控长）。检索失败或无相关内容时安全跳过。
	KnowledgeRetriever KnowledgeRetriever
	// Auditor 是命令执行审计器（M3-01 执行审计落库）。nil 时回落日志审计（LogAuditor）。
	// 经 codeagent.Deps 下传 Coder 子代理，使 team 模式下代码落地命令同样写入审计日志；
	// 单代理模式的工具由 api 层直接以 DBAuditor 构造（见 chat.go/sse.go）。
	Auditor executor.Auditor
	// Checkpointer 是无人值守下 ask 危险命令的「人工检查点」落库回调（M3-05）。nil 时
	// 命中 ask 的命令在无人值守下退化为直接 deny（与旧行为一致）。经 codeagent.Deps
	// 下传 Coder 子代理；单代理模式的工具由 api 层直接传入 NewCodeActWithGit。
	Checkpointer executor.Checkpointer
	// ExecutorMode 是执行器运行模式（M4-06）：无人值守（Unattended）下 ask 危险命令
	// 生成人工检查点排队、预算护栏全程生效、危险命令 deny 默认，使 24h 自主 Loop
	// 无需人盯；Interactive 下 ask 直接 deny（有人值守调试会话）。经 codeagent.Deps
	// 下传 Coder 子代理；单代理模式的工具由 api 层直接传入 NewCodeActWithGit。
	ExecutorMode executor.Mode
}

// KnowledgeRetriever 在对话前检索相关知识库内容并注入用户消息（M5-02）。
// 返回 "" 表示无相关内容（引擎不注入，保持原消息）。任何错误由实现方自行降级（引擎忽略 error 并跳过）。
type KnowledgeRetriever interface {
	Retrieve(ctx context.Context, userID, query string) (string, error)
}

// ToolSearchProvider 在每次对话时按需构建延迟工具箱的回调（M2-06）。
// 入参 userID 取自当前对话归属用户；返回 nil 表示无可暴露工具（引擎安全跳过）。
// 任何内部错误都应返回 error，引擎据此 fail-open（跳过工具箱而非阻断对话）。
type ToolSearchProvider func(ctx context.Context, userID uint) (*toolsearch.Toolbox, error)

// TeamConfig 是 CodeTeam 编排配置（M1-09），定义在 internal/agent 包内，
// 此处以类型别名再导出，使 api/cmd 层无需直连 codeagent 包即可配置团队。
type TeamConfig = codeagent.TeamConfig

// Engine 封装 trpc-agent-go 的 Runner/LLMAgent，负责连接 Provider+Model 并产出事件流。
type Engine struct {
	cfg    ModelConfig
	runner runner.Runner
	// toolbox 是延迟工具箱（M2-06），若启用则持有本轮对话的 MCP 连接，Close 时释放。
	toolbox *toolsearch.Toolbox
	// lastUsage 是本轮对话累计的 token 用量（M3-03 Token/费用计量），由 Stream 的
	// 事件桥接 goroutine 在读取事件时写回；Chat/Stream 消费完毕后经 LastUsage 读取落库。
	usageMu   sync.Mutex
	lastUsage model.Usage
}

// New 依据 ModelConfig 构造 Agent 引擎（openai 兼容协议）。
func New(cfg ModelConfig) (*Engine, error) {
	if cfg.ModelID == "" {
		return nil, errors.New("engine: 模型 id 不能为空")
	}
	if cfg.Protocol != "" && cfg.Protocol != "openai" {
		return nil, errors.New("engine: M0-10 暂仅支持 openai 兼容协议，其他协议将在后续里程碑实现")
	}
	if cfg.BaseURL == "" {
		return nil, errors.New("engine: BaseURL 不能为空（需为 OpenAI 兼容端点，含 /v1）")
	}

	// 框架模型抽象：OpenAI 兼容客户端。上游如需鉴权，api_key 透传。
	m := openai.New(cfg.ModelID, openai.WithAPIKey(cfg.APIKey), openai.WithBaseURL(cfg.BaseURL))

	// 基础工具集（echo/get_time）+ 可选额外工具（如 M1-06 CodeAct）。
	allTools := []tool.Tool{echoTool(), getTimeTool()}
	if len(cfg.Tools) > 0 {
		allTools = append(allTools, cfg.Tools...)
	}

	// 后台任务控制面（M2-04）：把六个控制工具挂到根 Agent（单代理或 Orchestrator 均可），
	// 使 Agent 能派生子任务后台并行推进。默认 worker 为 codeagent.RoleCoder（与 cmd 层
	// worker Runner 的默认代理名一致）；WithParentAppNamePropagation(true) 使父子
	// appName 一致，从而 transcript 回查钥匙可命中（见框架 tool/taskrun 实现）。
	if cfg.TaskRunController != nil {
		trTools := taskruntool.NewTools(
			cfg.TaskRunController,
			taskruntool.WithDefaultAgentName(codeagent.RoleCoder),
			taskruntool.WithParentAppNamePropagation(true),
			taskruntool.WithSessionService(cfg.TaskRunSession),
		)
		allTools = append(allTools, trTools.All()...)
	}

	// 延迟工具箱（M2-06）：把 MCP 服务器工具经 tool_search/call_tool 双控制工具按需暴露，
	// 默认不把全部工具注册进 Agent 上下文（避免 token 随工具数线性膨胀）。
	// 仅在启用、且有可用工具时才挂载双工具；provider 报错安全跳过（fail-open）。
	var box *toolsearch.Toolbox
	if cfg.ToolSearchEnabled && cfg.ToolSearchProvider != nil {
		if b, berr := cfg.ToolSearchProvider(context.Background(), cfg.ToolSearchUserID); berr != nil {
			// 构建失败不阻断对话：仅跳过工具箱（MCP 连接异常 / 用户无配置等）。
			box = nil
		} else if b != nil && b.Len() > 0 {
			box = b
			allTools = append(allTools, toolsearch.NewToolSearch(box), toolsearch.NewCallTool(box))
		}
	}

	// 技能 warm-start（M2-03）：会话开始时把相关 SKILL.md 渲染成系统上下文片段，
	// 注入根 Agent 指令。SkillRoots 由 api 层按 [共享根, 用户私有根] 拼好传入；
	// 长度由 SkillMaxChars 上限控制，避免技能膨胀撑爆上下文。
	skillCtx := ""
	if cfg.SkillWarmStart && len(cfg.SkillRoots) > 0 {
		max := cfg.SkillMaxChars
		if max <= 0 {
			max = skillrepo.DefaultWarmStartMaxChars
		}
		if b, serr := skillrepo.WarmStartBlockRoots(cfg.SkillRoots, cfg.SkillKeywords, max); serr == nil {
			skillCtx = b
		}
	}

	// 根 Agent：
	//   - 默认（单代理）：codeagent 直接持有全部工具，行为与 M1-07 一致；
	//   - Team.EnableSubAgents（M1-08）：换成 Orchestrator，代码落地委托给 Coder 子代理；
	//   - 再叠加 Team.EnableReviewer（M1-09）：加入只读 Reviewer，形成审阅回环。
	var root agent.Agent
	if cfg.Team.EnableSubAgents {
		orchestrator, oerr := codeagent.NewTeam(codeagent.Deps{
			Model:        m,
			Workdir:      cfg.Workdir,
			ExtraTools:   allTools,
			Guardrail:    cfg.Guardrail,
			StateStore:   cfg.StateStore,
			SkillContext: skillCtx,
			Auditor:      cfg.Auditor,
			Checkpointer: cfg.Checkpointer,
			ExecutorMode: cfg.ExecutorMode,
		}, cfg.Team)
		if oerr != nil {
			return nil, oerr
		}
		root = orchestrator
	} else {
		// 单代理模式（默认 AGENT_MODE=single，也是 24h 循环的实际运行模式）：
		// 同样必须挂载护栏熔断（M1-13）。否则无人值守循环在单代理模式下无任何
		// LLM 调用/工具迭代上限，模型一旦陷入死循环会卡死整轮 Run。
		singleOpts := []llmagent.Option{
			llmagent.WithModel(m),
			llmagent.WithInstruction(defaultInstruction + skillCtx),
			llmagent.WithTools(allTools),
		}
		if grdOpts := cfg.Guardrail.Options(); grdOpts != nil {
			singleOpts = append(singleOpts, grdOpts...)
		}
		// 工作状态外置（M1-16）：根 Agent 挂 StateEnforcer，把 PLAN/PROGRESS/LEARNINGS 落盘，
		// 进程重启 / 中断后续跑能接上。单代理模式（24h 循环实际模式）同样需要它。
		if cfg.EnableState && cfg.StateStore != nil {
			singleOpts = append(singleOpts, llmagent.WithExtensions(
				codeagent.NewStateEnforcer(codeagent.WithStateStore(cfg.StateStore))))
		}
		root = llmagent.New("codeagent", singleOpts...)
	}

	// Runner：未显式提供 session service 时框架会自动创建内存版会话服务。
	r := runner.NewRunner("go-multi-agent-v2", root)

	// 单次对话超时：<=0 时回退默认 90s（由配置 ENGINE_TIMEOUT_SECONDS 注入，M0.5-05）。
	if cfg.Timeout <= 0 {
		cfg.Timeout = 90 * time.Second
	}
	return &Engine{cfg: cfg, runner: r, toolbox: box}, nil
}

// Stream 发送一条用户消息并返回 Agent 事件流（channel）。
// 调用方负责消费 channel，并在结束后调用 Close 释放 Runner 资源；
// 当 ctx 被取消（如客户端断开）或 90s 超时后，底层 channel 会自动关闭。
//
// history 为可选的对话历史（按时间正序），用于多轮记忆回灌：框架无 SQLite
// 会话后端（v1.10.0 仅 inmemory/noop），这里把 DB 加载的历史作为初始消息
// seed 进本次 Run 的会话，使模型能看到前文（见 M0.5-01）。为空则单轮。
func (e *Engine) Stream(ctx context.Context, sessionID, userMessage string, history []model.Message) (<-chan *event.Event, error) {
	if sessionID == "" {
		sessionID = "default"
	}
	runCtx, cancel := context.WithTimeout(ctx, e.cfg.Timeout)
	// 显式开启流式：llmagent 默认是非流式（返回整块 Message.Content），
	// 不开启则上游被当作非流式 JSON 请求，无法产生 token 级增量。
	// M0 出口标准要求真正的流式对话，故始终以流式模式运行。
	runOpts := []agent.RunOption{agent.WithStream(true)}
	if len(history) > 0 {
		// 把 DB 历史作为初始消息 seed 进本次会话（fresh inmemory service），
		// runner 会先落库历史事件再追加本轮 user 消息，模型即拥有多轮上下文。
		runOpts = append(runOpts, agent.WithMessages(history))
	}
	// userID 透传真实用户标识（M2-04）：后台任务派生时 OwnerUserID 取自此值，
	// 管控 API 据此做 owner 隔离；未注入时回退 "user"（兼容历史/测试调用）。
	userID := userIDFromContext(ctx)

	// 知识库检索注入（M5-02）：若配置了 KnowledgeRetriever，则在发送用户消息前
	// 检索该用户全部知识库的相关内容，前缀注入用户消息（长度由 retriever 控长）。
	// 检索失败或无相关内容时安全跳过，不影响主对话。
	if e.cfg.KnowledgeRetriever != nil {
		if kbCtx, rerr := e.cfg.KnowledgeRetriever.Retrieve(runCtx, userID, userMessage); rerr == nil && kbCtx != "" {
			userMessage = kbCtx + "\n\n" + userMessage
		}
	}

	ch, err := e.runner.Run(runCtx, userID, sessionID, model.NewUserMessage(userMessage), runOpts...)
	if err != nil {
		cancel()
		return nil, err
	}

	// 桥接到独立输出 channel：源 channel 关闭或 ctx 取消时收尾并释放资源。
	out := make(chan *event.Event)
	go func() {
		defer close(out)
		defer cancel()
		for ev := range ch {
			// 累计 token 用量（M3-03）：上游在 Response.Usage 给出 prompt/completion/total，
			// 通常落在终帧；以「最新非零用量」覆盖，保证取最终累计值。
			if ev != nil && ev.Response != nil && ev.Response.Usage != nil && ev.Response.Usage.TotalTokens > 0 {
				e.usageMu.Lock()
				e.lastUsage = *ev.Response.Usage
				e.usageMu.Unlock()
			}
			select {
			case out <- ev:
			case <-runCtx.Done():
				return
			}
		}
	}()
	return out, nil
}

// LastUsage 返回本轮对话累计的 token 用量（M3-03）。
// 必须在 Chat/Stream 事件流被完全消费后读取（api 层在 eng.Chat 返回、
// 或 conv.Convert 结束后调用），此时桥接 goroutine 已写回最终值。
func (e *Engine) LastUsage() model.Usage {
	e.usageMu.Lock()
	defer e.usageMu.Unlock()
	return e.lastUsage
}

// Chat 发送一条用户消息并返回模型的最终文本回复（Stream 的累积版）。
// sessionID 用于多轮隔离（M0-11 起可配合 DB 持久化的 Session）；
// history 为可选的对话历史，用于多轮记忆回灌（见 M0.5-01）。
func (e *Engine) Chat(ctx context.Context, sessionID, userMessage string, history []model.Message) (string, error) {
	ch, err := e.Stream(ctx, sessionID, userMessage, history)
	if err != nil {
		return "", err
	}

	// 累加所有事件中的文本片段。
	// 通过 DeltaState 实现「优先 Delta 增量，未出现增量才回退 Message」的去重规则，
	// 与 AG-UI converter 共用同一逻辑（见 internal/engine/delta.go / M0.5-04）。
	var sb strings.Builder
	ds := NewDeltaState()
	// 运行级兜底（M1-13）：护栏熔断（LLM 调用/工具迭代预算耗尽）由框架以 IsError()
	// 事件表达，但本质是优雅终止。这里保留已产出的 partial 文本，并在末尾追加提示，
	// 而不是把它当成运行错误丢弃（见 IsCircuitBreakEvent）。
	circuitBroken := false
	for ev := range ch {
		if ev == nil || ev.Response == nil {
			continue
		}
		if IsCircuitBreakEvent(ev) {
			if !circuitBroken {
				sb.WriteString(CircuitBreakNotice())
				circuitBroken = true
			}
			continue
		}
		for i := range ev.Response.Choices {
			c := ev.Response.Choices[i]
			if t := ds.Text(c.Delta.Content, c.Message.Content); t != "" {
				sb.WriteString(t)
			}
		}
	}
	return sb.String(), nil
}

// Close 释放 Runner 持有的资源（内存 session 等）。
// 若本轮启用了延迟工具箱（M2-06），同时释放其持有的 MCP 连接，避免连接泄漏。
func (e *Engine) Close() error {
	if e.toolbox != nil {
		_ = e.toolbox.Close()
	}
	if e.runner != nil {
		return e.runner.Close()
	}
	return nil
}

// ctxKeyUserID 是注入真实用户标识的上下文键（M2-04 userID 透传）。
type ctxKeyUserID int

const ctxKeyUserIDVal ctxKeyUserID = iota

// WithUserID 把真实用户标识注入 ctx，供 engine.Stream 透传给框架 runner.Run，
// 使后台任务派生时的 OwnerUserID 正确隔离（管控 API 据此做 owner 过滤）。
// 未注入时 engine 回退为 "user"（兼容历史/测试调用）。
func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, ctxKeyUserIDVal, userID)
}

// userIDFromContext 从 ctx 取出注入的用户标识，未注入或为空时回退 "user"。
func userIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKeyUserIDVal).(string); ok && v != "" {
		return v
	}
	return "user"
}

// EstimateUsage 在 upstream 未返回 usage 时做粗估（M3-03 兜底）。
// 以「字符数/4」近似 token 数（中英文混合的保守下限估算），
// prompt+completion 相加得到 total；调用方据此标记 Estimated=true 落库。
func EstimateUsage(prompt, completion string) model.Usage {
	p := len([]rune(prompt)) / 4
	c := len([]rune(completion)) / 4
	return model.Usage{PromptTokens: p, CompletionTokens: c, TotalTokens: p + c}
}
