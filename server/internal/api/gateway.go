package api

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	taskrunruntime "trpc.group/trpc-go/trpc-agent-go/agent/taskrun"

	"github.com/ayanmw/multiagent2/server/internal/artifact"
	"github.com/ayanmw/multiagent2/server/internal/crypto"
	"github.com/ayanmw/multiagent2/server/internal/engine"
	"github.com/ayanmw/multiagent2/server/internal/executor"
	"github.com/ayanmw/multiagent2/server/internal/metrics"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/notify"
	"github.com/ayanmw/multiagent2/server/internal/obslog"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	codectool "github.com/ayanmw/multiagent2/server/internal/tool"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Channel 是所有外部对话入口的抽象来源（M4-04 Channel 层）。
// 内置常量覆盖当前四种入口；未来 IM/邮件网关只需实现本接口并调用 Gateway.Run/Stream，
// 即可复用统一的会话串行锁与同一套 Runner，无需各自重写「解析模型→建引擎→跑 Loop」逻辑。
type Channel interface {
	Kind() string
}

// ChannelKind 是内置 Channel 的轻量实现（仅携带稳定标识字符串）。
type ChannelKind string

func (c ChannelKind) Kind() string { return string(c) }

const (
	ChannelWeb     ChannelKind = "web"     // 浏览器对话（/api/chat 与 SSE 端点）
	ChannelCLI     ChannelKind = "cli"     // 命令行（M5-01 预留）
	ChannelWebhook ChannelKind = "webhook" // 外部事件触发（M4-03）
	ChannelCron    ChannelKind = "cron"    // 定时调度触发（M4-02）
	ChannelIM      ChannelKind = "im"      // 预留：IM/邮件网关
	ChannelRecover ChannelKind = "recover" // 进程重启后的跨天恢复续跑（M4-05）
	ChannelA2A     ChannelKind = "a2a"     // 外部 Agent 经 A2A 协议发起的任务（M5-07）
)

// GatewayConfig 是 Gateway 运行所需的全部依赖（与 ChatHandler/StreamChatHandler/engineLoopRunner 对齐）。
// 构造一次后由 Web 对话、SSE、定时、Webhook 等所有 Channel 共享，从而保证「同一会话串行锁」跨 Channel 生效。
type GatewayConfig struct {
	DB                 *gorm.DB
	EncKey             []byte
	EngineTimeout      time.Duration
	WorkspaceRoot      string
	Team               engine.TeamConfig
	StateStore         artifact.Store
	EnableState        bool
	SkillRoot          string
	SkillDataDir       string
	SkillWarmStart     bool
	SkillMaxChars      int
	TaskRunController  taskrunruntime.Controller
	TaskRunSession     engine.SessionService
	ToolSearchEnabled  bool
	ToolSearchProvider engine.ToolSearchProvider
	CheckpointEnabled  bool
	// ExecutorMode 是执行器运行模式（M4-06）：默认跟随 RUN_MODE 配置（unattended）。
	// 自主化 Channel（cron/webhook/recover）由 Gateway 强制覆盖为无人值守，保证
	// 无人盯守下 ask 危险命令落到检查点队列、预算护栏全程生效。
	ExecutorMode executor.Mode
	// ExecutorBackend 是代码执行后端（M8-02）：executor.BackendHost（宿主机 cwd 约束，
	// 默认）或 executor.BackendDocker（一次性容器沙箱：无网络 + 只读根 + /workspace 挂载，
	// 逃逸命令在容器内被拒）。零值回落 host（向后兼容）。
	ExecutorBackend executor.Backend
	// Docker 是 ExecutorBackend=BackendDocker 时的容器配置（镜像/网络/只读/CLI/超时）。
	Docker executor.DockerOptions
	// Notifier 是运行结果/检查点通知出口（M4-07，可空：nil 时不发通知）。
	Notifier notify.Notifier
	// KnowledgeRetriever 是可选的「对话前知识检索注入」（M5-02，可空：nil 时不检索）。
	// 非空时在每次对话前检索该用户知识库的相关切片并前缀注入用户消息。
	KnowledgeRetriever engine.KnowledgeRetriever
}

// Gateway 是所有对话/自主 Loop 的统一入口（M4-04）。
// 职责：① 稳定 session_id 分配（空则生成）；② 每会话串行锁（同一 session 内请求串行，
// 防并发串话/状态混乱）；③ 统一构建引擎并跑同一 Runner（Web/SSE/定时/Webhook 收敛到同一代码路径）。
type Gateway struct {
	cfg   GatewayConfig
	mu    sync.Mutex
	locks map[string]*sessionLock
	// 预算耗尽通知按用户冷却（M6-05）：无人值守 Loop 重试期间，一次预算拦截可能频繁触发，
	// 故对同用户限频，避免通知中心被刷屏。
	budgetNotifyMu   sync.Mutex
	budgetNotifyLast map[uint]time.Time
}

// budgetNotifyCooldown 是同一用户两次预算耗尽通知的最小间隔（M6-05）。
const budgetNotifyCooldown = 15 * time.Minute

type sessionLock struct {
	mu  sync.Mutex
	ref int
}

// NewGateway 构造统一网关。
func NewGateway(cfg GatewayConfig) *Gateway {
	return &Gateway{cfg: cfg, locks: make(map[string]*sessionLock), budgetNotifyLast: make(map[uint]time.Time)}
}

// maybeNotifyBudget 在「平台级预算耗尽拦截」时经统一通知出口给用户发一条告警（M6-05），
// 并做按用户冷却以避免无人值守 Loop 重试期间通知风暴。nil notifier 或未触发拦截时静默跳过
// （best-effort，不阻断主流程）。
func (g *Gateway) maybeNotifyBudget(uid uint, ev repo.BudgetEvaluation) {
	if g.cfg.Notifier == nil || !ev.Blocked {
		return
	}
	// M7-04：平台级预算耗尽拦截发生时记录指标（供「预算耗尽」告警）。
	metrics.RecordBudgetExhausted(context.Background())
	now := time.Now()
	g.budgetNotifyMu.Lock()
	last, ok := g.budgetNotifyLast[uid]
	if ok && now.Sub(last) < budgetNotifyCooldown {
		g.budgetNotifyMu.Unlock()
		return
	}
	g.budgetNotifyLast[uid] = now
	g.budgetNotifyMu.Unlock()
	_ = g.cfg.Notifier.Notify(context.Background(), notify.NewBudgetExhausted(uid, string(ev.Scope), ev.Used, ev.Max))
}

// DB 暴露底层数据库句柄（供 Channel handler 做预算检查等前置逻辑）。
func (g *Gateway) DB() *gorm.DB { return g.cfg.DB }

// EncKey 暴露加密主密钥（供 Channel handler 按需解密）。
func (g *Gateway) EncKey() []byte { return g.cfg.EncKey }

// EvaluateBudget 在发起 LLM 调用前评估平台级预算护栏（M3-04），供各 Channel 复用同一检查逻辑。
func (g *Gateway) EvaluateBudget(uid uint, sessionKey string) (repo.BudgetEvaluation, error) {
	return repo.EvaluateBudgets(g.cfg.DB, uid, sessionKey, "")
}

// ErrBudgetExhausted 是「平台级预算耗尽拦截」的哨兵错误（M3-04 / M4-06）。
// 由 Gateway.prepareRun 在发起 LLM 调用前集中评估，使 Web/SSE/定时/Webhook/恢复
// 全部 Channel 共用同一护栏；自主化无人值守 Loop 也不会绕过护栏烧预算。
var ErrBudgetExhausted = errors.New("budget exhausted")

// BudgetExhaustedError 携带预算评估明细的错误类型，便于 Channel handler 构造
// 429 / SSE RUN_ERROR 响应（含 scope/used/max 信息）。
type BudgetExhaustedError struct {
	Eval repo.BudgetEvaluation
}

func (e *BudgetExhaustedError) Error() string   { return "预算耗尽，待恢复" }
func (e *BudgetExhaustedError) Unwrap() error    { return ErrBudgetExhausted }

// resolveExecutorMode 解析本次运行的执行器模式（M4-06）：自主化 Channel
// （cron/webhook/recover/im）强制无人值守 —— 这些入口无人实时值守，ask 危险命令必须
// 落到人工检查点队列排队，绝不允许交互确认（无人确认即卡死）；Web/CLI 等有人值守
// Channel 跟随 RUN_MODE 配置（默认 unattended，安全默认）。
func (g *Gateway) resolveExecutorMode(ch Channel) executor.Mode {
	if ch == nil {
		return g.cfg.ExecutorMode
	}
	switch ch.Kind() {
	case ChannelCron.Kind(), ChannelWebhook.Kind(), ChannelRecover.Kind(), ChannelIM.Kind():
		return executor.ModeUnattended
	default:
		return g.cfg.ExecutorMode
	}
}

// allocateSessionKey 稳定分配 session_id：已有则沿用，否则生成新的。
func (g *Gateway) allocateSessionKey(key string) string {
	if key == "" {
		return repo.NewSessionKey()
	}
	return key
}

// lockSession 取或建某 session 的串行锁并加锁；unlockSession 解锁并在引用归零时清理。
func (g *Gateway) lockSession(key string) {
	g.mu.Lock()
	sl, ok := g.locks[key]
	if !ok {
		sl = &sessionLock{}
		g.locks[key] = sl
	}
	sl.ref++
	g.mu.Unlock()
	sl.mu.Lock()
}

func (g *Gateway) unlockSession(key string) {
	g.mu.Lock()
	sl := g.locks[key]
	if sl != nil {
		sl.ref--
		if sl.ref <= 0 {
			delete(g.locks, key)
		}
	}
	g.mu.Unlock()
	if sl != nil {
		sl.mu.Unlock()
	}
}

// Request 是进入 Gateway 的统一对话请求（所有 Channel 共用）。
type Request struct {
	Channel      Channel // 来源标识（Web/CLI/Webhook/Cron/IM）
	UserID       uint
	SessionKey   string // 为空时 Gateway 分配稳定 session_id
	Message      string
	ModelID      uint   // 0 = 默认启用模型
	WorkspaceKey string
	TeamOverride *engine.TeamConfig // 非空时覆盖 Gateway 默认 Team（如自主化强制开启目标契约）
}

// Result 是 Gateway 处理后的统一结果。
type Result struct {
	SessionKey string
	Reply      string
	ModelID    uint
	ModelName  string
	Session    *model.Session
}

type preparedRun struct {
	sess       *model.Session
	sessionKey string
	m          *model.Model
	p          *model.Provider
	workdir    string
	eng        *engine.Engine
	history    []engine.ChatMessage
}

// prepareRun 统一完成「解析模型 → 解密 → 建会话/写 user 消息 → 解析工作目录 → 构建引擎 → 加载历史」
// 的共享前置流程，供 Run/Stream 复用（同一 Runner 落点）。sessionKey 必须已稳定分配。
func (g *Gateway) prepareRun(ctx context.Context, req Request, sessionKey string) (*preparedRun, error) {
	uid := req.UserID

	// 预算护栏（M3-04 / M4-06）：在发起 LLM 调用前集中评估平台级预算，超时直接拦截。
	// 集中在此处使 Web/SSE/定时/Webhook/恢复 Loop 全部 Channel 共用同一护栏，
	// 自主化无人值守 Loop 也不会绕过护栏烧预算。拦截时写审计便于管理员追溯。
	budgetEv, berr := repo.EvaluateBudgets(g.cfg.DB, uid, sessionKey, "")
	if berr != nil {
		return nil, fmt.Errorf("预算评估失败: %w", berr)
	}
	if budgetEv.Blocked {
		writeBudgetBlockAudit(g.cfg.DB, uid, budgetEv)
		// M6-05：预算耗尽拦截同时经统一通知出口给用户发一条告警（按用户冷却防风暴）。
		g.maybeNotifyBudget(uid, budgetEv)
		return nil, &BudgetExhaustedError{Eval: budgetEv}
	}

	// 本次运行的执行器模式（M4-06）：自主化 Channel 强制无人值守，其余跟随 RUN_MODE 配置。
	exMode := g.resolveExecutorMode(req.Channel)

	m, p, err := resolveChatModel(g.cfg.DB, uid, req.ModelID)
	if err != nil {
		return nil, err
	}
	apiKey := ""
	if p.APIKeyEnc != "" {
		dec, derr := crypto.Decrypt(p.APIKeyEnc, g.cfg.EncKey)
		if derr != nil {
			return nil, fmt.Errorf("解密 provider key 失败")
		}
		apiKey = dec
	}
	sess, serr := repo.GetOrCreateSession(g.cfg.DB, uid, sessionKey)
	if serr != nil {
		return nil, fmt.Errorf("创建会话失败")
	}
	if aerr := repo.AppendMessage(g.cfg.DB, sess.ID, "user", req.Message); aerr != nil {
		return nil, fmt.Errorf("写入用户消息失败")
	}
	wsLocalDir, werr := resolveWorkspaceLocalDir(g.cfg.DB, uid, req.WorkspaceKey, sess)
	if werr != nil {
		return nil, fmt.Errorf("workspace not found")
	}
	workdir, dErr := ensureWorkdir(g.cfg.WorkspaceRoot, uid, wsLocalDir)
	if dErr != nil {
		return nil, dErr
	}

	team := g.cfg.Team
	if req.TeamOverride != nil {
		team = *req.TeamOverride
	}

	// 人工检查点落库回调（M3-05）：无人值守命中 ask 危险命令时生成 checkpoint 并暂停。
	var checkpointer executor.Checkpointer
	if g.cfg.CheckpointEnabled {
		checkpointer = func(cpr executor.CheckpointRequest) (string, error) {
			cp := &model.Checkpoint{
				SessionID: sessionKey,
				UserID:    uid,
				Command:   cpr.Command,
				Workdir:   cpr.Workdir,
				Reason:    cpr.Reason,
				Context:   cpr.Context,
				Status:    model.CheckpointPending,
			}
			if cerr := repo.CreateCheckpoint(g.cfg.DB, cp); cerr != nil {
				return "", cerr
			}
			// M4-07：无人值守命中 ask 危险命令生成检查点后，向归属用户发一条「待审批」通知
			// （best-effort，不阻断主流程；nil notifier 时静默跳过）。
			if g.cfg.Notifier != nil {
				_ = g.cfg.Notifier.Notify(ctx, notify.NewCheckpoint(uid, 0, "(自主 Loop)", cp.DisplayID(), cpr.Command))
			}
			return cp.DisplayID(), nil
		}
	}
	var tools []tool.Tool
	if !team.EnableSubAgents {
		// M8-02：单代理模式的根 Agent 工具集按配置后端切换（host 宿主机 /
		// docker 容器沙箱）；docker 下 git 工具需镜像内置 git。
		t, tErr := codectool.NewCodeActWithGitBackend(workdir, repo.NewDBAuditor(g.cfg.DB, uid), checkpointer, exMode, g.cfg.ExecutorBackend, g.cfg.Docker)
		if tErr != nil {
			return nil, fmt.Errorf("构建代码执行工具失败: %w", tErr)
		}
		tools = t
	}
	// M5-06：读取该用户的可优化指令覆盖（默认名为 "default" 的单代理指令）。
	// 空字符串表示未配置 → 引擎回退内置 defaultInstruction，向后兼容。
	instrOverride, _ := repo.GetInstructionContent(g.cfg.DB, uid, model.DefaultInstructionName)
	eng, eErr := engine.New(engine.ModelConfig{
		ModelID:            m.ModelID,
		BaseURL:            p.BaseURL,
		APIKey:             apiKey,
		Protocol:           string(p.Protocol),
		Timeout:            g.cfg.EngineTimeout,
		Tools:              tools,
		Team:               team,
		Workdir:            workdir,
		EnableState:        g.cfg.EnableState,
		StateStore:         g.cfg.StateStore,
		SkillWarmStart:     g.cfg.SkillWarmStart,
		SkillRoots:         []string{g.cfg.SkillRoot, filepath.Join(g.cfg.SkillDataDir, strconv.FormatUint(uint64(uid), 10))},
		SkillKeywords:      nil,
		SkillMaxChars:      g.cfg.SkillMaxChars,
		TaskRunController:  g.cfg.TaskRunController,
		TaskRunSession:     g.cfg.TaskRunSession,
		ToolSearchEnabled:  g.cfg.ToolSearchEnabled,
		ToolSearchProvider: g.cfg.ToolSearchProvider,
		ToolSearchUserID:   uid,
		KnowledgeRetriever: g.cfg.KnowledgeRetriever,
		Auditor:            repo.NewDBAuditor(g.cfg.DB, uid),
		Checkpointer:       checkpointer,
		ExecutorMode:       exMode,
		Backend:            g.cfg.ExecutorBackend, // M8-02：team 模式 Coder 执行后端
		Docker:             g.cfg.Docker,          // M8-02：docker 后端容器配置
		InstructionOverride: instrOverride,
	})
	if eErr != nil {
		return nil, eErr
	}
	history := loadChatHistory(g.cfg.DB, sess.ID, 1)
	return &preparedRun{sess: sess, sessionKey: sessionKey, m: m, p: p, workdir: workdir, eng: eng, history: history}, nil
}

// Run 串行执行一次非流式对话（绑定 sessionKey 串行锁）。返回统一结果或引擎错误。
func (g *Gateway) Run(ctx context.Context, req Request) (*Result, error) {
	sessionKey := g.allocateSessionKey(req.SessionKey)
	g.lockSession(sessionKey)
	defer g.unlockSession(sessionKey)
	return g.run(ctx, req, sessionKey)
}

func (g *Gateway) run(ctx context.Context, req Request, sessionKey string) (res *Result, err error) {
	// M7-06：对话级 trace span——Gateway→Runner→工具调用共用同一 trace_id，
	// 使一次对话的日志可按 trace 串联（含跨 Channel 的自主 Loop）。
	ctx, end := obslog.StartSpan(ctx, "gateway.run",
		"channel", channelKind(req.Channel), "session_key", sessionKey, "user_id", req.UserID)
	defer func() { end(err) }()

	pr, err := g.prepareRun(ctx, req, sessionKey)
	if err != nil {
		return nil, err
	}
	defer pr.eng.Close()
	llmStart := time.Now()
	reply, err := pr.eng.Chat(engine.WithUserID(ctx, strconv.FormatUint(uint64(req.UserID), 10)), sessionKey, req.Message, pr.history)
	// M3-09：记录 LLM 调用数 / 时延 / 错误率（provider+model 维度）。
	metrics.RecordLLMCall(ctx, pr.p.Name, pr.m.Name, time.Since(llmStart), err)
	if err != nil {
		return nil, err
	}
	g.finalize(req, pr, reply)
	obslog.Ctx(ctx).Info("gateway.run.complete",
		"session_key", sessionKey, "model", pr.m.Name, "reply_chars", len(reply))
	return &Result{SessionKey: sessionKey, Reply: reply, ModelID: pr.m.ID, ModelName: pr.m.Name, Session: pr.sess}, nil
}

// Stream 串行执行一次流式对话，通过 emit 推送 AG-UI 事件（绑定 sessionKey 串行锁）。
// 流式初始化失败时返回非 nil err（调用方应补发 RUN_ERROR）；转换期错误已在 emit 内上报，err 为 nil。
func (g *Gateway) Stream(ctx context.Context, req Request, emit func(string, gin.H)) (*Result, error) {
	sessionKey := g.allocateSessionKey(req.SessionKey)
	g.lockSession(sessionKey)
	defer g.unlockSession(sessionKey)
	return g.stream(ctx, req, sessionKey, emit)
}

func (g *Gateway) stream(ctx context.Context, req Request, sessionKey string, emit func(string, gin.H)) (res *Result, err error) {
	// M7-06：流式对话同样以 gateway.stream span 贯通 trace（见 run 注释）。
	ctx, end := obslog.StartSpan(ctx, "gateway.stream",
		"channel", channelKind(req.Channel), "session_key", sessionKey, "user_id", req.UserID)
	defer func() { end(err) }()

	pr, err := g.prepareRun(ctx, req, sessionKey)
	if err != nil {
		return nil, err
	}
	defer pr.eng.Close()
	llmStart := time.Now()
	ch, rerr := pr.eng.Stream(engine.WithUserID(ctx, strconv.FormatUint(uint64(req.UserID), 10)), sessionKey, req.Message, pr.history)
	if rerr != nil {
		metrics.RecordLLMCall(ctx, pr.p.Name, pr.m.Name, time.Since(llmStart), rerr)
		return nil, rerr
	}
	conv := newAGUIConverter()
	text, convErr := conv.Convert(ch, emit)
	// 仅在正常结束或护栏熔断（partial 结果）时落库助手消息（M1-13 运行级兜底）。
	if convErr == nil || conv.circuitBroken {
		g.finalize(req, pr, text)
	}
	metrics.RecordLLMCall(ctx, pr.p.Name, pr.m.Name, time.Since(llmStart), convErr)
	if convErr != nil {
		obslog.Ctx(ctx).Error("gateway.stream.complete",
			"session_key", sessionKey, "model", pr.m.Name, "error", convErr.Error(), "partial_chars", len(text))
		return &Result{SessionKey: sessionKey, Reply: text, ModelID: pr.m.ID, ModelName: pr.m.Name, Session: pr.sess}, convErr
	}
	obslog.Ctx(ctx).Info("gateway.stream.complete",
		"session_key", sessionKey, "model", pr.m.Name, "reply_chars", len(text))
	return &Result{SessionKey: sessionKey, Reply: text, ModelID: pr.m.ID, ModelName: pr.m.Name, Session: pr.sess}, nil
}

// channelKind 取 Channel 的稳定标识；nil Channel 回退 "unknown"（兼容直接调用/测试）。
func channelKind(ch Channel) string {
	if ch == nil {
		return "unknown"
	}
	return ch.Kind()
}

// finalize 在对话正常结束或护栏熔断（partial）后记录 token 用量并落库助手消息。
func (g *Gateway) finalize(req Request, pr *preparedRun, reply string) {
	// M3-03：记录 token 用量（按 user / session / provider / model 归属）。
	recordEngineUsage(g.cfg.DB, pr.eng, req.UserID, pr.sess, pr.p, pr.m, buildPromptText(pr.history, req.Message), reply)
	// 仅在正常结束时落库助手消息（客户端中途断开不写脏数据，由调用方控制）。
	if perr := repo.AppendMessage(g.cfg.DB, pr.sess.ID, "assistant", reply); perr != nil {
		fmt.Printf("[GATEWAY] 写入助手消息失败: %v\n", perr)
	}
}
