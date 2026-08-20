// Package taskrun 封装「后台任务扇出」的接线（M2-04）：
//   - BuildAgentFactory：构造 worker 子代理工厂，按 child session 的 OwnerUserID
//     从闭包解析模型 + 工作目录，构建 Coder 子代理（与 M1-08 同款）。
//   - NewController：组装框架 inprocess.Service（自带 run 记录持久化 FileStore）+ 内部
//     worker Runner（挂持久化 session.Service，使 transcript 跨重启可读）。
//   - Tools：把框架 tool/taskrun 的六个控制工具挂到根 Agent（Orchestrator/单代理）。
//
// 本包只依赖框架与 internal/agent/internal/sessionstore，均为非 CGO；
// 真正的 DB/模型解析闭包由 cmd/server（CGO）注入，业务层不直连 DB。
package taskrun

import (
	"context"
	"fmt"
	"log"
	"strconv"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	taskrunruntime "trpc.group/trpc-go/trpc-agent-go/agent/taskrun"
	inprocess "trpc.group/trpc-go/trpc-agent-go/agent/taskrun/inprocess"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	taskruntool "trpc.group/trpc-go/trpc-agent-go/tool/taskrun"

	codeagent "github.com/ayanmw/multiagent2/server/internal/agent"
	"github.com/ayanmw/multiagent2/server/internal/executor"
	"github.com/ayanmw/multiagent2/server/internal/worktree"
)

// AppName 是后台任务子代理 Runner 与父 Orchestrator 共用的 app 命名空间。
// transcript 回查钥匙依赖父子 appName 一致（见框架 tool/taskrun 实现），
// 故必须与 engine.New 中 runner.NewRunner 的 appName 相同。
const AppName = "go-multi-agent-v2"

// WorkerResolver 按 OwnerUserID 解析 worker 子代理所需的模型与工作目录。
// 由 cmd/server（CGO）注入真实 DB + Provider 解析逻辑。
type WorkerResolver struct {
	// ResolveModel 返回指定用户的框架模型实例（含已解密的 APIKey + BaseURL）。
	ResolveModel func(ctx context.Context, userID string) (model.Model, error)
	// ResolveWorkdir 返回指定用户 worker 的「主仓库」执行工作目录（已确保存在，M1-07）。
	// 当开启 worktree 隔离时，子代理实际工作目录会被替换为该主仓库派生的独立 worktree。
	ResolveWorkdir func(ctx context.Context, userID string) (string, error)
	// Worktree 是可选的 worktree 隔离钩子（M2-05）。nil 表示不隔离，沿用主目录。
	Worktree *WorktreeHook
	// NewAuditor 按 OwnerUserID 解析本次 worker 命令执行的审计器（M3-01 执行审计落库）。
	// 返回审计器写入 audit_logs 表的对应 owner 名下，实现 taskrun 后台子任务命令的全量审计；
	// nil 时 worker 命令经日志审计（LogAuditor，不落库）。若未注入则 worker 使用 nil 审计器。
	NewAuditor func(ownerUserID uint) executor.Auditor
	// NewCheckpointer 按 OwnerUserID + 子任务会话解析「人工检查点」落库回调（M3-05）。
	// 后台任务是典型的无人值守场景：命中 ask 危险命令时不再直接 deny，而是生成 checkpoint
	// 并暂停该命令，待运营在前端审批（approve 执行 / reject 中止）。
	// nil 时回退为直接 deny（与 M3-05 之前行为一致）。
	NewCheckpointer func(ownerUserID uint, childSessionID string) executor.Checkpointer
}

// WorktreeHook 把 git worktree 隔离接入 taskrun 生命周期（M2-05）：
//   - BuildAgentFactory 中调用 Create 为子代理换上隔离工作目录（独立分支 taskrun/<id>）；
//   - 作为 inprocess.Observer 在子任务终态（completed/failed/canceled）时
//     merge 回主分支并清理 worktree（或冲突保留分支交人工）。
//
// 关联键（重要，v1.10.0 适配）：子任务唯一键取 run.ID（框架经
// RuntimeStateKeyRunID 注入 worker AgentFactory 的 ro.RuntimeState，Observer 侧
// 用 run.ID 可复现同一键），与 Create/OnRunUpdate 两侧统一；为兼容历史测试
// （直接以 childSessionID 为键调用），OnRunUpdate 在 run.ID 查不到时回退
// childSessionID 再查。
//
// Enabled=false 或 Manager=nil 时本钩子为空操作，taskrun 行为与 M2-04 一致。
type WorktreeHook struct {
	Enabled bool
	Manager *worktree.Manager
}

// Create 为指定 runID 创建隔离 worktree；返回实际工作目录（失败回退主目录时返回空串）。
func (h *WorktreeHook) Create(ctx context.Context, repoDir, runID string) (string, error) {
	if !h.Enabled || h.Manager == nil {
		return "", nil
	}
	return h.Manager.Create(ctx, repoDir, runID)
}

// OnRunUpdate 实现 taskrun.Observer：在子任务终态时 merge 回主分支并清理 worktree。
func (h *WorktreeHook) OnRunUpdate(ctx context.Context, run taskrunruntime.Run) {
	if !h.Enabled || h.Manager == nil {
		return
	}
	if !run.Status.IsTerminal() {
		return
	}
	// 键解析：优先 run.ID（v1.10.0 下 Create 侧用 runID 注册）；历史调用以
	// ChildSessionID 为键注册的 entry 也能命中（向后兼容）。
	key := run.ID
	matched := h.Manager.Lookup(key)
	if key == "" || !matched {
		if run.ChildSessionID != "" {
			key = run.ChildSessionID
			matched = h.Manager.Lookup(key)
		}
	}
	// M7.5-03 压测诊断：并发扇出时键不匹配会导致 Finalize 空跑（产物丢失），
	// 保留一行可观测日志便于排查。
	log.Printf("[taskrun] OnRunUpdate run=%s child=%s status=%s key=%s matched=%v",
		run.ID, run.ChildSessionID, run.Status, key, matched)
	h.Manager.Finalize(ctx, key, string(run.Status))
}

// BuildAgentFactory 构造框架 AgentFactory：每次 spawn 时从 invocation 上下文取出
// OwnerUserID，经闭包解析模型 + 工作目录后构建 Coder 子代理。
//
// 注意（v1.10.0 适配，重要）：worker Runner 的 AgentFactory 签名不含 UserID，且
// runner.Run 是先 selectAgent（调用本工厂）后 NewInvocation——即工厂被调用时 ctx 里
// 还没有 invocation（框架行为，见 LEARNINGS）。因此身份来源做多级回退：
//  1. agent.InvocationFromContext(ctx)（若框架调整顺序或个别路径已创建 invocation）；
//  2. ctx 显式注入（调用方用 WithWorkerIdentity 包装 Controller，经 SpawnRequest
//     RunContext 钩子把父会话用户身份透传到 worker 运行上下文）。
//
// 子任务唯一键（worktree / checkpoint 用）取 ro.RuntimeState["taskrun.run_id"]
// （框架 inprocess 注入的 run.ID，Observer 侧可用 run.ID 复现同一键），而非
// invocation.Session.ID——工厂阶段拿不到 child session，且 runID 保证每次派生唯一。
//
// executorMode 为 worker 子代理的执行器运行模式（M4-06）：后台任务本质上是无人值守场景，
// 调用方应传入 executor.ModeUnattended（ask 危险命令生成人工检查点排队），保证 24h 自主
// Loop 派生的子任务同样受护栏约束、命中 ask 时落检查点而非卡死或盲目放行。
//
// backend/dockerOpts 为 worker 子代理的代码执行后端（M8-02）：BackendHost（宿主机，
// 默认）或 BackendDocker（一次性容器沙箱，逃逸命令在容器内被拒）。注意 docker 后端下
// Git 工具要求容器镜像内置 git；worktree 钩子的 git 操作（add/merge 宿主仓库结构）仍走
// 宿主机执行，不受此参数影响（见 LEARNINGS M8-02）。
func BuildAgentFactory(guardrail codeagent.GuardrailConfig, res WorkerResolver, executorMode executor.Mode, backend executor.Backend, dockerOpts executor.DockerOptions) runner.AgentFactory {
	resolveModel := res.ResolveModel
	resolveWorkdir := res.ResolveWorkdir
	if resolveModel == nil {
		resolveModel = func(ctx context.Context, userID string) (model.Model, error) {
			return nil, fmt.Errorf("taskrun: 未配置模型解析器")
		}
	}
	if resolveWorkdir == nil {
		resolveWorkdir = func(ctx context.Context, userID string) (string, error) {
			return "", fmt.Errorf("taskrun: 未配置工作目录解析器")
		}
	}
	return func(ctx context.Context, ro agent.RunOptions) (agent.Agent, error) {
		// 身份解析（见函数头注释：v1.10.0 下工厂阶段无 invocation，多级回退）。
		uid := ""
		if inv, ok := agent.InvocationFromContext(ctx); ok && inv != nil && inv.Session != nil {
			uid = inv.Session.UserID
		}
		if uid == "" {
			uid = workerUserIDFromContext(ctx)
		}
		if uid == "" {
			return nil, fmt.Errorf("taskrun: 无法获取 worker 调用的用户身份（请用 WithWorkerIdentity 包装 Controller，或在含 invocation 的上下文派生）")
		}
		m, err := resolveModel(ctx, uid)
		if err != nil {
			return nil, fmt.Errorf("taskrun: 解析 worker 模型失败: %w", err)
		}
		wd, err := resolveWorkdir(ctx, uid)
		if err != nil {
			return nil, fmt.Errorf("taskrun: 解析 worker 工作目录失败: %w", err)
		}
		// 子任务唯一键：优先 ro.RuntimeState 的 run_id（框架注入），保证 Observer
		// 侧用 run.ID 可复现；兜底取注入的父会话 id（仅作 key，语义为「本次派生」）。
		childKey := workerParentSessionFromContext(ctx)
		if v, ok := ro.RuntimeState[taskrunruntime.RuntimeStateKeyRunID]; ok {
			if s, isStr := v.(string); isStr && s != "" {
				childKey = s
			}
		}
		// M7.5-03 压测诊断：记录 Create 侧注册键（供 OnRunUpdate 匹配核查）。
		if res.Worktree != nil && res.Worktree.Enabled {
			log.Printf("[taskrun] BuildAgentFactory uid=%s childKey=%s", uid, childKey)
		}
		// M3-01：构造落库审计器，使 worker 子代理执行的命令写入该 owner 名下的审计日志。
		// uid 为字符串形式的用户 id（与入口 uid 一致），解析失败则回落系统（0）。
		var auditor executor.Auditor
		// M3-05：后台任务无人值守，命中 ask 危险命令时生成人工检查点并暂停（而非直接 deny）。
		var checkpointer executor.Checkpointer
		if uidNum, perr := strconv.ParseUint(uid, 10, 64); perr == nil {
			if res.NewAuditor != nil {
				auditor = res.NewAuditor(uint(uidNum))
			}
			if res.NewCheckpointer != nil {
				checkpointer = res.NewCheckpointer(uint(uidNum), childKey)
			}
		}
		// M2-05：若开启 worktree 隔离，把子代理的执行目录切换到独立 worktree（独立分支），
		// 使其改动不污染主分支工作区；创建失败则回退主目录（不阻断任务）。
		if res.Worktree != nil {
			if wt, werr := res.Worktree.Create(ctx, wd, childKey); werr == nil && wt != "" {
				wd = wt
			}
		}
		return codeagent.NewCoder(codeagent.Deps{
			Model:        m,
			Workdir:      wd,
			Guardrail:    guardrail,
			Auditor:      auditor,
			Checkpointer: checkpointer,
			ExecutorMode: executorMode,
			Backend:      backend,     // M8-02：worker 执行后端（host/docker）
			Docker:       dockerOpts,  // M8-02：docker 后端容器配置
		})
	}
}

// NewController 组装后台任务控制器（框架 inprocess.Service）：
//   - worker Runner：NewRunnerWithAgentFactory(AppName, defaultAgentName, factory)，
//     并挂持久化 session.Service（transcript 落盘）。
//   - inprocess.Service：用 store 持久化 run 记录（跨重启保留）；observer 可选，
//     用于子任务终态钩子（M2-05 worktree merge/清理）；
//     Start 后才能在父 Agent 调用 start_task_run 时真正派生子任务。
func NewController(ctx context.Context, defaultAgentName string, factory runner.AgentFactory, store inprocess.Store, sessionSvc session.Service, observer taskrunruntime.Observer) (*inprocess.Service, error) {
	if factory == nil {
		return nil, fmt.Errorf("taskrun: 未提供 worker 代理工厂")
	}
	workerOpts := []runner.Option{}
	if sessionSvc != nil {
		workerOpts = append(workerOpts, runner.WithSessionService(sessionSvc))
	}
	workerRunner := runner.NewRunnerWithAgentFactory(AppName, defaultAgentName, factory, workerOpts...)
	svcOpts := []inprocess.Option{inprocess.WithStore(store)}
	if observer != nil {
		svcOpts = append(svcOpts, inprocess.WithObserver(observer))
	}
	svc, err := inprocess.NewService(workerRunner, svcOpts...)
	if err != nil {
		return nil, err
	}
	svc.Start(ctx)
	return svc, nil
}

// Tools 返回挂到根 Agent 的后台任务控制工具集（start/list/get/cancel/wait + transcript）。
// sessionSvc 非空时才会追加 read_task_run_transcript（M2-04 ① 持久化 transcript）；
// defaultAgentName 指定未显式指定 agent 时派生的默认 worker（建议 codeagent.RoleCoder）。
func Tools(controller taskrunruntime.Controller, sessionSvc session.Service, defaultAgentName string) []tool.Tool {
	opts := []taskruntool.Option{
		taskruntool.WithDefaultAgentName(defaultAgentName),
		taskruntool.WithParentAppNamePropagation(true),
	}
	if sessionSvc != nil {
		opts = append(opts, taskruntool.WithSessionService(sessionSvc))
	}
	tr := taskruntool.NewTools(controller, opts...)
	return tr.All()
}

// ---------------------------------------------------------------------------
// worker 身份透传（v1.10.0 适配，M7.5-02 真实模型 E2E 暴露）
// ---------------------------------------------------------------------------
//
// 背景：框架 v1.10.0 的 inprocess.Service 用 baseCtx（NewController 注入的 ctx）启动
// 后台 goroutine，worker Runner.Run 的 selectAgent 阶段先于 NewInvocation——即
// worker 的 AgentFactory 被调用时，ctx 里**没有**框架 invocation，拿不到 OwnerUserID。
// 因此 BuildAgentFactory 改为多级取身份（invocation → ctx 注入），调用方用
// WithWorkerIdentity 包装 Controller：在 spawn 工具调用（ctx 含父 Orchestrator 的
// invocation）时，把父会话的用户身份经 SpawnRequest.RunContext 钩子写入 worker 的
// 运行上下文，工厂即可经 workerUserIDFromContext 取到。

// workerCtxKey 是注入 worker 运行上下文的身份键。
type workerCtxKey int

const (
	workerCtxUserID workerCtxKey = iota
	workerCtxParentSession
)

// WithWorkerUserID 把后台子任务的归属用户注入运行上下文（供 BuildAgentFactory 读取；
// 由 WithWorkerIdentity 自动注入，一般无需手动调用）。
func WithWorkerUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, workerCtxUserID, userID)
}

// WithWorkerParentSession 把父会话 id 注入运行上下文（作为子任务唯一键的兜底来源）。
func WithWorkerParentSession(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, workerCtxParentSession, sessionID)
}

func workerUserIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(workerCtxUserID).(string); ok {
		return v
	}
	return ""
}

func workerParentSessionFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(workerCtxParentSession).(string); ok {
		return v
	}
	return ""
}

// WithWorkerIdentity 包装后台任务 Controller，在每次 spawn 时把父会话的用户身份
// 注入 worker 运行上下文（见上方背景说明）。
//
// 用法：taskRunController = taskrun.WithWorkerIdentity(taskRunController)
// 之后把包装后的 controller 同时传给 taskrun.Tools 与 engine.ModelConfig.TaskRunController，
// 使 worker 子代理（Coder）能解析到归属用户（模型/工作目录/审计/检查点）。
func WithWorkerIdentity(inner taskrunruntime.Controller) taskrunruntime.Controller {
	if inner == nil {
		return nil
	}
	return &workerIdentityController{inner: inner}
}

// workerIdentityController 实现 taskrunruntime.Controller 的包装器。
type workerIdentityController struct {
	inner taskrunruntime.Controller
}

func (c *workerIdentityController) Spawn(ctx context.Context, req taskrunruntime.SpawnRequest) (taskrunruntime.Run, error) {
	// spawn 工具执行 ctx 含父 Orchestrator 的 invocation（框架 currentContext 同源）。
	if inv, ok := agent.InvocationFromContext(ctx); ok && inv != nil && inv.Session != nil && inv.Session.UserID != "" {
		uid, sid := inv.Session.UserID, inv.Session.ID
		base := req.RunContext
		req.RunContext = func(runCtx context.Context) context.Context {
			runCtx = WithWorkerUserID(runCtx, uid)
			if sid != "" {
				runCtx = WithWorkerParentSession(runCtx, sid)
			}
			if base != nil {
				if enriched := base(runCtx); enriched != nil {
					runCtx = enriched
				}
			}
			return runCtx
		}
	}
	return c.inner.Spawn(ctx, req)
}

func (c *workerIdentityController) List(ctx context.Context, filter taskrunruntime.ListFilter) ([]taskrunruntime.Run, error) {
	return c.inner.List(ctx, filter)
}

func (c *workerIdentityController) Get(ctx context.Context, runID string) (*taskrunruntime.Run, error) {
	return c.inner.Get(ctx, runID)
}

func (c *workerIdentityController) Cancel(ctx context.Context, runID string) (*taskrunruntime.Run, bool, error) {
	return c.inner.Cancel(ctx, runID)
}

func (c *workerIdentityController) Wait(ctx context.Context, runID string) (*taskrunruntime.Run, error) {
	return c.inner.Wait(ctx, runID)
}
