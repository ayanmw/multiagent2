package config

import (
	"crypto/sha256"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	codeagent "github.com/ayanmw/multiagent2/server/internal/agent"
	"github.com/ayanmw/multiagent2/server/internal/executor"
	"github.com/ayanmw/multiagent2/server/internal/skillrepo"
)

// DefaultEngineTimeout is the fallback streaming timeout when ENGINE_TIMEOUT_SECONDS
// is unset or invalid. A single LLM run is aborted after this duration.
const DefaultEngineTimeout = 90 * time.Second

// Agent 运行模式（env AGENT_MODE，M1-08）。
const (
	// AgentModeSingle：单代理模式，codeagent 直接持有 CodeAct 工具（M1-06/07 行为）。
	AgentModeSingle = "single"
	// AgentModeTeam：子代理委托模式，根 Agent 为 Orchestrator，代码落地委托 Coder 子代理。
	AgentModeTeam = "team"
)

// 运行时模式（env RUN_MODE，M4-06 无人值守 Loop 运行模式）。
const (
	// RunModeUnattended 无人值守：危险命令 deny 默认 + 命中 ask 生成人工检查点排队 +
	// 预算护栏全程生效，使 24h 自主 Loop 无需人盯。这是 24h 自主平台的安全默认。
	RunModeUnattended = "unattended"
	// RunModeAttended 有人值守（可选）：用于有人实时值守的调试会话，危险命令 ask 直接 deny
	// （无同步确认通道，回落 deny）。自主化 cron/webhook/恢复 Loop 不受此影响（强制无人值守）。
	RunModeAttended = "attended"
)

// Config holds the application configuration.
type Config struct {
	DBPath               string
	Port                 string
	JWTSecret            string
	EncryptionKey        []byte // 32-byte key for AES-256-GCM (provider API keys at rest)
	EngineTimeoutSeconds int    // timeout (s) for a single LLM run (env ENGINE_TIMEOUT_SECONDS)
	WorkspaceRoot        string // 用户工作区根目录（env WORKSPACE_ROOT，默认 data/workspaces），M1-06 CodeAct 工具的执行根
	artifactRoot         string // 工作状态文件根目录（env ARTIFACT_ROOT，默认 data/agent-state），M1-16
	stateEnabled         bool   // 是否开启「工作状态外置」（env STATE_ENABLED，默认 true），M1-16
	AgentMode            string // single / team（env AGENT_MODE，默认 single），M1-08 子代理委托开关
	TeamReviewer         bool   // team 模式下是否加入只读 Reviewer（env TEAM_REVIEWER，默认 true），M1-09
	TeamMaxReviewRounds  int    // 「实现→审阅→修复」回环轮数上限（env TEAM_MAX_REVIEW_ROUNDS，默认 2），M1-09
	GoalContract         bool   // team 模式下是否启用目标契约（env GOAL_CONTRACT，默认 true），M1-11
	GoalMaxNudges        int    // 目标未达成时最多拦截几次过早的最终答复（env GOAL_MAX_NUDGES，默认 3），M1-11
	PlanExecute          bool   // team 模式下是否启用 Plan-Execute 循环（env PLAN_EXECUTE，默认 true），M1-12
	PlanMaxNudges        int    // 计划未做完时最多拦截几次过早的最终答复（env PLAN_MAX_NUDGES，默认 3），M1-12
	// 护栏熔断预算（M1-13）：防止自主推进的 Agent 陷入死循环烧掉预算 / 卡死 24h 循环。
	// 默认按 codeagent 包默认预算启用；GUARDRAIL_DISABLED=true 可完全解除（仅本地调试）。
	MaxLLMCalls       int  // 单次 invocation LLM 调用次数上限（env MAX_LLM_CALLS，默认 32），M1-13
	MaxToolIterations int  // 单次 invocation 工具迭代轮数上限（env MAX_TOOL_ITERATIONS，默认 16），M1-13
	MaxToolRetries    int  // 单个工具失败后的重试次数（env MAX_TOOL_RETRIES，默认 2），M1-13
	GuardrailDisabled bool // 关闭护栏（env GUARDRAIL_DISABLED，默认 false）；生产/无人值守禁止开启，M1-13
	// Skills 仓库（M2-03）：共享技能根（内置/管理员，只读）+ 用户私有技能根（owner 隔离）。
	skillsRoot        string // 共享技能根目录（env SKILLS_ROOT，默认 <cwd>/skills）
	skillsDataDir     string // 用户私有技能根目录（env SKILLS_DATA_DIR，默认 <cwd>/data/skills）
	skillWarmStart    bool   // 是否开启「技能 warm-start」注入（env SKILL_WARM_START，默认 true），M2-03
	skillWarmMaxChars int    // warm-start 注入内容长度上限（控长，env SKILL_WARM_START_MAX_CHARS，默认 6000），M2-03
	// Worktree 隔离（M2-05）：后台 taskrun 子任务在独立 git worktree 内执行，完成后 merge 回主分支。
	worktreeIsolation bool // 是否开启 worktree 隔离（env WORKTREE_ISOLATION，默认 true），M2-05
	// 延迟工具箱（M2-06）：把 MCP 服务器工具经 tool_search/call_tool 双控制工具按需暴露给 Agent，
	// 默认不把全部工具声明一次性灌进模型上下文，避免 token 随工具数线性膨胀。
	toolSearchEnabled bool // 是否开启延迟工具箱（env TOOL_SEARCH_ENABLED，默认 true），M2-06
	// 知识库 RAG 检索注入（M5-02）：env KNOWLEDGE_ENABLED（默认 true）。
	// 开启后对话前检索用户知识库相关切片并前缀注入用户消息；关闭则不做检索注入。
	knowledgeEnabled bool // 是否开启知识库检索注入（env KNOWLEDGE_ENABLED，默认 true），M5-02
	// 平台级预算护栏总开关（M3-04）：env BUDGET_ENABLED（默认 true）。
	// 关闭后所有预算检查直接放行（仅本地调试 / 紧急恢复用）；具体阈值由 DB 中的 BudgetPolicy 配置。
	budgetEnabled bool // 是否开启预算护栏（env BUDGET_ENABLED，默认 true），M3-04
	// 人工检查点开关（M3-05）：env CHECKPOINT_ENABLED（默认 true）。
	// 关闭后无人值守命中 ask 危险命令直接 deny（与旧行为一致）；
	// 开启时则生成 checkpoint 记录并暂停，待前端审批（approve 执行 / reject 中止）。
	checkpointEnabled bool // 是否开启人工检查点（env CHECKPOINT_ENABLED，默认 true），M3-05
	// DB 结构迁移（M3-08）：默认走版本化 migration（repo.RunMigrations）。
	// DB_AUTO_MIGRATE=true 仅作**开发期 fallback**，在 migration 之后再跑一次
	// GORM AutoMigrate，便于本地改模型时免写迁移；生产必须保持关闭。
	dbAutoMigrate bool // 是否启用 AutoMigrate 开发 fallback（env DB_AUTO_MIGRATE，默认 false），M3-08
	// 可观测性（M3-09）：默认开启；初始化 OpenTelemetry MeterProvider 并开放 /metrics
	// （Prometheus 文本格式）。METRICS_ENABLED=false 可关闭（纯调试 / 不暴露指标端点）。
	metricsEnabled bool // 是否启用可观测性指标（env METRICS_ENABLED，默认 true），M3-09
	// 结构化日志（M7-06）：LOG_FORMAT 为 json（默认，生产友好）/ text（本地调试）；
	// LOG_LEVEL 为 debug/info/warn/error（默认 info）。初始化全局 slog 时使用，
	// 并使标准 log 包输出一并 JSON 化（集中采集友好）。
	logFormat string // 日志输出格式（env LOG_FORMAT，默认 json），M7-06
	logLevel  string // 日志级别（env LOG_LEVEL，默认 info），M7-06
	// Webhook 速率限制（M4-03）：外部事件入口防刷。按 token 维度、窗口内最多触发 limit 次。
	webhookRateLimit  int           // 单个 webhook token 在窗口内的触发上限（env WEBHOOK_RATE_LIMIT，默认 10）
	webhookRateWindow time.Duration // 速率限制窗口（env WEBHOOK_RATE_WINDOW_SECONDS，默认 60s）
	// 跨天恢复重试上限（M4-05）：单次进程重启对同一「未收敛运行」最多尝试续跑的次数，
	// 超过后标记 failed 不再续跑，避免永远无法收敛的 Loop 在每次重启时无限续跑。
	recoveryMaxAttempts int // 恢复重试上限（env RECOVERY_MAX_ATTEMPTS，默认 3）
	// 出站 webhook 通知回调地址（M4-07）：自主化 Loop 结果除站内信外，额外 POST 一份 JSON
	// 事件到该地址（占位，可对接外部系统）。为空则跳过（仅站内信）。
	webhookNotifyURL string // 出站通知回调地址（env WEBHOOK_NOTIFY_URL，默认空=关闭）
	// Webhook 入站签名密钥（M6-05）：非空时启用 HMAC-SHA256 校验（X-Webhook-Signature），
	// 外部系统在调用 /api/webhooks/:token 时需携带按此密钥对 body 计算的签名；空=关闭校验（向后兼容）。
	webhookSignSecret string // 入站 webhook 签名密钥（env WEBHOOK_SIGN_SECRET，默认空=关闭）
	// 告警接收端点共享密钥（M7-04）：非空时 /api/alerts 要求携带此密钥（Authorization: Bearer
	// 或 X-Alert-Token）才接受 Alertmanager 推送；空=关闭校验（向后兼容，生产建议配置）。
	alertWebhookToken string // 告警接收端点共享密钥（env ALERT_WEBHOOK_TOKEN，默认空=关闭）
	// 告警通知目标用户 ID 列表（M7-04）：Prometheus/Alertmanager 平台告警经统一通知出口
	// 投递到这些管理员用户（逗号分隔，env ALERT_NOTIFY_USER_IDS，默认 "1"）。
	alertNotifyUserIDs []uint // 告警通知目标用户 ID 列表（env ALERT_NOTIFY_USER_IDS，默认 [1]）
	// 安全加固（MX-07）：CORS 精细化允许的前端源列表（env CORS_ALLOWED_ORIGINS，逗号分隔），
	// 仅放行白名单内的跨域源；缺省为本地 Vite 开发源。特殊值 "*" 放开全部（仅公开非凭证接口用）。
	corsAllowedOrigins []string
	// 登录/注册防刷（MX-07）：按客户端 IP 滑动窗口限流；关闭则 limit 回落 0（中间件直接放行）。
	rateLimitLoginEnabled bool
	rateLimitLoginLimit   int
	rateLimitLoginWindow  time.Duration
	// 对话防刷（MX-07）：按认证用户滑动窗口限流（缺省回落客户端 IP）；关闭则 limit 回落 0。
	rateLimitChatEnabled bool
	rateLimitChatLimit   int
	rateLimitChatWindow  time.Duration
	// 运行时模式（M4-06）：env RUN_MODE，默认 unattended。无人值守下危险命令 deny 默认、
	// 命中 ask 生成人工检查点排队、预算护栏全程生效，使 24h 自主 Loop 无需人盯；
	// attended 仅用于有人实时值守的调试会话（危险命令 ask 直接 deny）。
	runMode string
	// 技能进化飞轮（M5-03）：env EVOLUTION_ENABLED（默认 true）控制是否启动后台扫描
	// goroutine（遍历会话 → LLM 提取候选技能 → 质量门控 → 落库待审批）。
	// EVOLUTION_INTERVAL_SECONDS（默认 3600）为扫描周期。
	evolutionEnabled  bool
	evolutionInterval time.Duration
	// 执行后端（M8-02）：env EXECUTOR_BACKEND = host | docker（默认 host）。
	// host：宿主机受限工作目录执行（cwd 约束 + 危险命令策略）；
	// docker：一次性容器沙箱（--network none 无网络 + --read-only 只读根 +
	// 工作目录挂载 /workspace），逃逸命令（写系统目录/出网/读宿主文件）在容器内被拒。
	// docker 后端需宿主机安装 docker CLI，且容器镜像需包含所需工具链（git 等）。
	executorBackend executor.Backend
	// Docker 执行后端细节（M8-02）：DOCKER_IMAGE（默认 alpine:3.20）/
	// DOCKER_NETWORK（默认 none）／DOCKER_READ_ONLY（默认 true）／
	// DOCKER_BIN（默认 docker）／DOCKER_TIMEOUT_SECONDS（默认 60）。
	dockerImage    string
	dockerNetwork  string
	dockerReadOnly bool
	dockerBin      string
	dockerTimeout  time.Duration
}

// DefaultMaxReviewRounds 是 CodeTeam「实现→审阅→修复」默认回环轮数上限（M1-09）。
const DefaultMaxReviewRounds = 2

// DefaultGoalMaxNudges 是目标契约默认的最大拦截次数（M1-11）。
// 超出后 fail-open 放行，避免模型不配合时把整轮 Run 卡死。
const DefaultGoalMaxNudges = 3

// DefaultMaxPlanNudges 是 Plan-Execute 循环默认的最大拦截次数（M1-12）。
// 超出后 fail-open 放行，避免模型不配合时把整轮 Run 卡死。
const DefaultMaxPlanNudges = 3

// DefaultRecoveryMaxAttempts 是单次跨重启恢复对同一「未收敛运行」的最大续跑尝试次数
// （M4-05）。超过后标记 failed 不再续跑，避免永远无法收敛的 Loop 在每次重启时无限续跑。
// 此处定义一份本地默认，避免 config 包反向依赖 api 包（api 已另有同名常量，二者互不冲突）。
const DefaultRecoveryMaxAttempts = 3

// 护栏熔断默认预算（M1-13）直接复用 codeagent 包定义，避免两处漂移：
// 业务层默认以 codeagent.Default* 为准，config 仅做 env 注入入口。
const (
	// DefaultMaxLLMCalls 是单次 invocation 默认的 LLM 调用次数上限。
	DefaultMaxLLMCalls = codeagent.DefaultMaxLLMCalls
	// DefaultMaxToolIterations 是单次 invocation 默认的工具迭代轮数上限。
	DefaultMaxToolIterations = codeagent.DefaultMaxToolIterations
	// DefaultMaxToolRetries 是单个工具调用失败后默认的重试次数。
	DefaultMaxToolRetries = codeagent.DefaultMaxToolRetries
)

// Load reads configuration from environment variables with sensible defaults.
func Load() *Config {
	cfg := &Config{
		Port: envOrDefault("PORT", "8080"),
	}

	// Streaming timeout for a single LLM run (env ENGINE_TIMEOUT_SECONDS).
	// Defaults to DefaultEngineTimeout when unset or non-positive.
	cfg.EngineTimeoutSeconds = envOrDefaultInt("ENGINE_TIMEOUT_SECONDS", int(DefaultEngineTimeout/time.Second))

	// JWT signing secret (must be set via env in production).
	const defaultJWTSecret = "dev-insecure-secret-change-me"
	jwtSecret := envOrDefault("JWT_SECRET", defaultJWTSecret)
	if jwtSecret == defaultJWTSecret {
		log.Println("[WARN] JWT_SECRET not set; using insecure default secret. Set JWT_SECRET in production.")
	}
	cfg.JWTSecret = jwtSecret

	// 32-byte AES-256 key for encrypting provider secrets at rest.
	// Use a dedicated PROVIDER_ENC_KEY in production; fall back to JWT_SECRET
	// for local development so a single env var still works end-to-end.
	encSrc := envOrDefault("PROVIDER_ENC_KEY", jwtSecret)
	sum := sha256.Sum256([]byte(encSrc))
	cfg.EncryptionKey = sum[:]

	// Default DB path: data/codeagent.db relative to project root
	dbPath := envOrDefault("DB_PATH", "")
	if dbPath == "" {
		// Resolve relative to working directory (which should be the project root)
		execPath, _ := os.Getwd()
		dbPath = filepath.Join(execPath, "data", "codeagent.db")
	}

	// Ensure parent directory exists
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		panic("failed to create data directory: " + err.Error())
	}

	cfg.DBPath = dbPath

	// 用户工作区根目录（M1-06 CodeAct 工具的受限执行根）。
	// 每个用户的工作区为该目录下的 <uid> 子目录，由 api 层在请求时自动创建。
	workspaceRoot := envOrDefault("WORKSPACE_ROOT", "")
	if workspaceRoot == "" {
		execPath, _ := os.Getwd()
		workspaceRoot = filepath.Join(execPath, "data", "workspaces")
	}
	if err := os.MkdirAll(workspaceRoot, 0755); err != nil {
		panic("failed to create workspace root directory: " + err.Error())
	}
	cfg.WorkspaceRoot = workspaceRoot

	// 工作状态文件根目录（M1-16「工作状态外置」）：按 <root>/<sessionKey>/ 落盘
	// PLAN.md/PROGRESS.md/LEARNINGS.md，使进程重启/中断后续跑能接上。
	// 默认 data/agent-state（与 workspace 同级的运行时目录，gitignore 已忽略 data/）。
	artifactRoot := envOrDefault("ARTIFACT_ROOT", "")
	if artifactRoot == "" {
		execPath, _ := os.Getwd()
		artifactRoot = filepath.Join(execPath, "data", "agent-state")
	}
	if err := os.MkdirAll(artifactRoot, 0755); err != nil {
		panic("failed to create artifact root directory: " + err.Error())
	}
	cfg.artifactRoot = artifactRoot

	// 工作状态外置开关（M1-16）：默认开启。仅在需要关闭状态落盘（如纯单轮问答调试）时设
	// STATE_ENABLED=false；24h 自主推进循环应保持开启以具备「中断续跑」能力。
	cfg.stateEnabled = envOrDefaultBool("STATE_ENABLED", true)

	// Agent 运行模式（M1-08）：team 时启用 Orchestrator→Coder 子代理委托。
	// 非法取值退回 single，避免误配把线上对话切到未预期的编排链路。
	mode := envOrDefault("AGENT_MODE", AgentModeSingle)
	if mode != AgentModeSingle && mode != AgentModeTeam {
		log.Printf("[WARN] AGENT_MODE=%q is invalid; using %q", mode, AgentModeSingle)
		mode = AgentModeSingle
	}
	cfg.AgentMode = mode

	// CodeTeam 编排配置（M1-09）：team 模式下默认加入只读 Reviewer，形成审阅回环；
	// 可用 TEAM_REVIEWER=false 关闭（退回 M1-08 的 Orchestrator+Coder 二人组）。
	cfg.TeamReviewer = envOrDefaultBool("TEAM_REVIEWER", true)
	cfg.TeamMaxReviewRounds = envOrDefaultInt("TEAM_MAX_REVIEW_ROUNDS", DefaultMaxReviewRounds)
	if cfg.TeamMaxReviewRounds <= 0 {
		log.Printf("[WARN] TEAM_MAX_REVIEW_ROUNDS must be positive; using default %d", DefaultMaxReviewRounds)
		cfg.TeamMaxReviewRounds = DefaultMaxReviewRounds
	}

	// 目标契约配置（M1-11）：team 模式下默认开启，Orchestrator 必须把目标推进到
	// complete/blocked 才允许结束；GOAL_CONTRACT=false 可关闭（退回 M1-09 行为）。
	cfg.GoalContract = envOrDefaultBool("GOAL_CONTRACT", true)
	cfg.GoalMaxNudges = envOrDefaultInt("GOAL_MAX_NUDGES", DefaultGoalMaxNudges)
	if cfg.GoalMaxNudges <= 0 {
		log.Printf("[WARN] GOAL_MAX_NUDGES must be positive; using default %d", DefaultGoalMaxNudges)
		cfg.GoalMaxNudges = DefaultGoalMaxNudges
	}

	// Plan-Execute 循环配置（M1-12）：team 模式下默认开启，Orchestrator 必须先建计划、
	// 逐项执行完毕才允许结束（requirePlan 默认 false，一句话能答完的请求不强制建计划）；
	// PLAN_EXECUTE=false 可关闭（退回 M1-11 行为）。
	cfg.PlanExecute = envOrDefaultBool("PLAN_EXECUTE", true)
	cfg.PlanMaxNudges = envOrDefaultInt("PLAN_MAX_NUDGES", DefaultMaxPlanNudges)
	if cfg.PlanMaxNudges <= 0 {
		log.Printf("[WARN] PLAN_MAX_NUDGES must be positive; using default %d", DefaultMaxPlanNudges)
		cfg.PlanMaxNudges = DefaultMaxPlanNudges
	}

	// 护栏熔断预算（M1-13）：默认按 codeagent 默认预算启用，无人值守必须有兜底；
	// 取值非法时回落默认并打印告警。GUARDRAIL_DISABLED=true 完全解除限制（仅本地调试）。
	cfg.MaxLLMCalls = envOrDefaultInt("MAX_LLM_CALLS", DefaultMaxLLMCalls)
	if cfg.MaxLLMCalls <= 0 {
		log.Printf("[WARN] MAX_LLM_CALLS must be positive; using default %d", DefaultMaxLLMCalls)
		cfg.MaxLLMCalls = DefaultMaxLLMCalls
	}
	cfg.MaxToolIterations = envOrDefaultInt("MAX_TOOL_ITERATIONS", DefaultMaxToolIterations)
	if cfg.MaxToolIterations <= 0 {
		log.Printf("[WARN] MAX_TOOL_ITERATIONS must be positive; using default %d", DefaultMaxToolIterations)
		cfg.MaxToolIterations = DefaultMaxToolIterations
	}
	cfg.MaxToolRetries = envOrDefaultInt("MAX_TOOL_RETRIES", DefaultMaxToolRetries)
	if cfg.MaxToolRetries < 0 {
		log.Printf("[WARN] MAX_TOOL_RETRIES must be non-negative; using default %d", DefaultMaxToolRetries)
		cfg.MaxToolRetries = DefaultMaxToolRetries
	}
	cfg.GuardrailDisabled = envOrDefaultBool("GUARDRAIL_DISABLED", false)
	if cfg.GuardrailDisabled {
		log.Println("[WARN] GUARDRAIL_DISABLED=true: circuit-breaker limit lifted; only for local debugging, NOT for unattended runs.")
	}

	// Skills 仓库（M2-03）：共享技能根（内置/管理员，只读）+ 用户私有技能根（owner 隔离）。
	// 两目录均在 Load 时确保存在，使 warm-start 扫描与私有技能写入不会因目录缺失失败。
	skillsRoot := envOrDefault("SKILLS_ROOT", "")
	if skillsRoot == "" {
		execPath, _ := os.Getwd()
		skillsRoot = filepath.Join(execPath, "skills")
	}
	if err := os.MkdirAll(skillsRoot, 0o755); err != nil {
		panic("failed to create skills root directory: " + err.Error())
	}
	cfg.skillsRoot = skillsRoot

	skillsDataDir := envOrDefault("SKILLS_DATA_DIR", "")
	if skillsDataDir == "" {
		execPath, _ := os.Getwd()
		skillsDataDir = filepath.Join(execPath, "data", "skills")
	}
	if err := os.MkdirAll(skillsDataDir, 0o755); err != nil {
		panic("failed to create skills data directory: " + err.Error())
	}
	cfg.skillsDataDir = skillsDataDir

	// 技能 warm-start（M2-03）：默认开启；把相关 SKILL.md 注入根 Agent 系统上下文，
	// 使新会话自动「带着技能知识」开工。SKILL_WARM_START=false 可关闭（纯调试）。
	cfg.skillWarmStart = envOrDefaultBool("SKILL_WARM_START", true)
	cfg.skillWarmMaxChars = envOrDefaultInt("SKILL_WARM_START_MAX_CHARS", skillrepo.DefaultWarmStartMaxChars)
	if cfg.skillWarmMaxChars <= 0 {
		log.Printf("[WARN] SKILL_WARM_START_MAX_CHARS must be positive; using default %d", skillrepo.DefaultWarmStartMaxChars)
		cfg.skillWarmMaxChars = skillrepo.DefaultWarmStartMaxChars
	}

	// Worktree 隔离（M2-05）：后台 taskrun 子任务在独立 git worktree（独立分支 taskrun/<id>）内执行，
	// 完成后 merge 回主分支并清理，绝不 push 远程。默认开启；WORKTREE_ISOLATION=false 可关闭（退化为 M2-04 直接在主目录执行）。
	cfg.worktreeIsolation = envOrDefaultBool("WORKTREE_ISOLATION", true)

	// 延迟工具箱（M2-06）：默认开启；把 MCP 服务器工具经 tool_search/call_tool 按需暴露，
	// 避免上下文随工具数线性膨胀。TOOL_SEARCH_ENABLED=false 可关闭（纯调试 / 无 MCP 配置场景）。
	cfg.toolSearchEnabled = envOrDefaultBool("TOOL_SEARCH_ENABLED", true)

	// 知识库 RAG 检索注入（M5-02）：默认开启；对话前检索用户知识库相关切片前缀注入。
	// KNOWLEDGE_ENABLED=false 可关闭（纯调试 / 无需知识库场景）。
	cfg.knowledgeEnabled = envOrDefaultBool("KNOWLEDGE_ENABLED", true)

	// 平台级预算护栏（M3-04）：默认开启；具体阈值由 DB 中的 BudgetPolicy 配置。
	// BUDGET_ENABLED=false 可整体关闭拦截（紧急恢复 / 纯调试，非生产环境不建议）。
	cfg.budgetEnabled = envOrDefaultBool("BUDGET_ENABLED", true)

	// 人工检查点（M3-05）：默认开启；无人值守命中 ask 危险命令转人工审批。
	// CHECKPOINT_ENABLED=false 可整体关闭（退化回直接 deny，紧急恢复 / 纯调试用）。
	cfg.checkpointEnabled = envOrDefaultBool("CHECKPOINT_ENABLED", true)

	// DB 结构迁移（M3-08）：默认 false —— 启动只执行版本化 migration，
	// 结构变更必须以 migration 落盘，避免各环境「靠 AutoMigrate 补齐」而漂移。
	cfg.dbAutoMigrate = envOrDefaultBool("DB_AUTO_MIGRATE", false)
	if cfg.dbAutoMigrate {
		log.Println("[WARN] DB_AUTO_MIGRATE=true: 开发期 AutoMigrate 兜底已开启；生产环境严禁开启——" +
			"此开关会让各环境表结构靠 AutoMigrate 补齐而漂移，唯一生产 schema 真相源是 schema_migrations 版本表。")
	}

	// 可观测性（M3-09）：默认开启；初始化 OpenTelemetry MeterProvider 并暴露 /metrics
	// （Prometheus 文本格式）。METRICS_ENABLED=false 可整体关闭指标端点（纯调试）。
	cfg.metricsEnabled = envOrDefaultBool("METRICS_ENABLED", true)

	// 结构化日志（M7-06）：LOG_FORMAT 默认 json（生产友好，可被 Loki/ELK 集中采集）；
	// LOG_LEVEL 默认 info。取值非法时回落默认并打印告警。
	cfg.logFormat = envOrDefault("LOG_FORMAT", "json")
	if cfg.logFormat != "json" && cfg.logFormat != "text" {
		log.Printf("[WARN] LOG_FORMAT=%q 非法，使用默认 json", cfg.logFormat)
		cfg.logFormat = "json"
	}
	cfg.logLevel = envOrDefault("LOG_LEVEL", "info")
	switch cfg.logLevel {
	case "debug", "info", "warn", "warning", "error":
	default:
		log.Printf("[WARN] LOG_LEVEL=%q 非法，使用默认 info", cfg.logLevel)
		cfg.logLevel = "info"
	}

	// Webhook 速率限制（M4-03）：外部事件入口防刷。单个 token 在窗口内最多触发 limit 次，
	// 超出 handler 返回 429。取值非法时回落默认并打印告警。
	cfg.webhookRateLimit = envOrDefaultInt("WEBHOOK_RATE_LIMIT", 10)
	if cfg.webhookRateLimit <= 0 {
		log.Printf("[WARN] WEBHOOK_RATE_LIMIT must be positive; using default 10")
		cfg.webhookRateLimit = 10
	}
	wrs := envOrDefaultInt("WEBHOOK_RATE_WINDOW_SECONDS", 60)
	if wrs <= 0 {
		log.Printf("[WARN] WEBHOOK_RATE_WINDOW_SECONDS must be positive; using default 60")
		wrs = 60
	}
	cfg.webhookRateWindow = time.Duration(wrs) * time.Second

	// 跨天恢复重试上限（M4-05）：默认 3；取值非法时回落默认并打印告警。
	cfg.recoveryMaxAttempts = envOrDefaultInt("RECOVERY_MAX_ATTEMPTS", DefaultRecoveryMaxAttempts)
	if cfg.recoveryMaxAttempts <= 0 {
		log.Printf("[WARN] RECOVERY_MAX_ATTEMPTS must be positive; using default %d", DefaultRecoveryMaxAttempts)
		cfg.recoveryMaxAttempts = DefaultRecoveryMaxAttempts
	}

	// 出站 webhook 通知回调地址（M4-07）：为空表示只发站内信，不发外部回调。
	cfg.webhookNotifyURL = envOrDefault("WEBHOOK_NOTIFY_URL", "")
	cfg.webhookSignSecret = envOrDefault("WEBHOOK_SIGN_SECRET", "")

	// 告警接收端点共享密钥（M7-04）：空=关闭校验。
	cfg.alertWebhookToken = envOrDefault("ALERT_WEBHOOK_TOKEN", "")
	// 告警通知目标用户 ID 列表（M7-04）：默认投递到 ID=1（首个管理员）。
	cfg.alertNotifyUserIDs = parseUintList(envOrDefault("ALERT_NOTIFY_USER_IDS", "1"))

	// 安全加固（MX-07）：CORS 精细化允许源。缺省本地 Vite 开发源；生产用
	// CORS_ALLOWED_ORIGINS 显式配置前端域名（逗号分隔）。特殊值 "*" 放开全部
	//（仅公开非凭证接口用，此时 AllowCredentials 不生效）。
	corsOrigins := envOrDefault("CORS_ALLOWED_ORIGINS", "http://localhost:5173,http://127.0.0.1:5173")
	cfg.corsAllowedOrigins = splitAndTrim(corsOrigins, ",")

	// 登录/注册防刷（MX-07）：缺省开启，单 IP 每 60s 最多 10 次，抵御爆破。
	cfg.rateLimitLoginEnabled = envOrDefaultBool("RATE_LIMIT_LOGIN_ENABLED", true)
	cfg.rateLimitLoginLimit = envOrDefaultInt("RATE_LIMIT_LOGIN_LIMIT", 10)
	if cfg.rateLimitLoginLimit <= 0 {
		log.Printf("[WARN] RATE_LIMIT_LOGIN_LIMIT must be positive; using default 10")
		cfg.rateLimitLoginLimit = 10
	}
	rlw := envOrDefaultInt("RATE_LIMIT_LOGIN_WINDOW_SECONDS", 60)
	if rlw <= 0 {
		log.Printf("[WARN] RATE_LIMIT_LOGIN_WINDOW_SECONDS must be positive; using default 60")
		rlw = 60
	}
	cfg.rateLimitLoginWindow = time.Duration(rlw) * time.Second

	// 对话防刷（MX-07）：缺省开启，单用户每 60s 最多 30 次（缺省回落 IP），抵御滥用。
	cfg.rateLimitChatEnabled = envOrDefaultBool("RATE_LIMIT_CHAT_ENABLED", true)
	cfg.rateLimitChatLimit = envOrDefaultInt("RATE_LIMIT_CHAT_LIMIT", 30)
	if cfg.rateLimitChatLimit <= 0 {
		log.Printf("[WARN] RATE_LIMIT_CHAT_LIMIT must be positive; using default 30")
		cfg.rateLimitChatLimit = 30
	}
	rcw := envOrDefaultInt("RATE_LIMIT_CHAT_WINDOW_SECONDS", 60)
	if rcw <= 0 {
		log.Printf("[WARN] RATE_LIMIT_CHAT_WINDOW_SECONDS must be positive; using default 60")
		rcw = 60
	}
	cfg.rateLimitChatWindow = time.Duration(rcw) * time.Second

	// 运行时模式（M4-06）：默认 unattended（24h 自主平台的安全默认）。
	cfg.runMode = envOrDefault("RUN_MODE", RunModeUnattended)
	if cfg.runMode != RunModeUnattended && cfg.runMode != RunModeAttended {
		log.Printf("[WARN] RUN_MODE=%q 非法，使用默认 %q", cfg.runMode, RunModeUnattended)
		cfg.runMode = RunModeUnattended
	}

	// 技能进化飞轮（M5-03）：默认开启后台扫描；EVOLUTION_ENABLED=false 关闭（纯调试 /
	// 不希望后台消耗 LLM 配额时）。扫描周期默认 3600s，非法取值回落默认。
	cfg.evolutionEnabled = envOrDefaultBool("EVOLUTION_ENABLED", true)
	evoInterval := envOrDefaultInt("EVOLUTION_INTERVAL_SECONDS", 3600)
	if evoInterval <= 0 {
		log.Printf("[WARN] EVOLUTION_INTERVAL_SECONDS must be positive; using default 3600")
		evoInterval = 3600
	}
	cfg.evolutionInterval = time.Duration(evoInterval) * time.Second

	// 执行后端（M8-02）：默认 host（向后兼容——旧部署不设 EXECUTOR_BACKEND 行为不变）。
	// docker 需宿主机安装 docker CLI；启动时若探测不可用会打 [WARN]（见 main.go）。
	backend := executor.Backend(envOrDefault("EXECUTOR_BACKEND", string(executor.BackendHost)))
	if backend != executor.BackendHost && backend != executor.BackendDocker {
		log.Printf("[WARN] EXECUTOR_BACKEND=%q 非法，使用默认 %q", backend, executor.BackendHost)
		backend = executor.BackendHost
	}
	cfg.executorBackend = backend
	// Docker 后端细节：默认与 executor.DockerOptions 零值回落一致（见 executor 包常量），
	// 此处显式给默认便于运维按 env 覆盖；非法数值回落默认并告警。
	cfg.dockerImage = envOrDefault("DOCKER_IMAGE", executor.DefaultDockerImage)
	cfg.dockerNetwork = envOrDefault("DOCKER_NETWORK", executor.DefaultDockerNetwork)
	cfg.dockerReadOnly = envOrDefaultBool("DOCKER_READ_ONLY", true)
	cfg.dockerBin = envOrDefault("DOCKER_BIN", executor.DefaultDockerBin)
	dts := envOrDefaultInt("DOCKER_TIMEOUT_SECONDS", 60)
	if dts <= 0 {
		log.Printf("[WARN] DOCKER_TIMEOUT_SECONDS must be positive; using default 60")
		dts = 60
	}
	cfg.dockerTimeout = time.Duration(dts) * time.Second

	return cfg
}

// GuardrailConfig returns the circuit-breaker budget as a codeagent.GuardrailConfig
// (M1-13). Zero-valued config fields are normalized inside codeagent.GuardrailConfig,
// so callers can use the result directly with Options() to build llmagent options.
func (c *Config) GuardrailConfig() codeagent.GuardrailConfig {
	if c == nil {
		return codeagent.GuardrailConfig{}
	}
	return codeagent.GuardrailConfig{
		Disabled:          c.GuardrailDisabled,
		MaxLLMCalls:       c.MaxLLMCalls,
		MaxToolIterations: c.MaxToolIterations,
		MaxToolRetries:    c.MaxToolRetries,
	}
}

// SubAgentsEnabled reports whether the orchestrator/sub-agent delegation mode is on.
func (c *Config) SubAgentsEnabled() bool {
	return c != nil && c.AgentMode == AgentModeTeam
}

// ReviewerEnabled reports whether the read-only Reviewer joins the team (M1-09).
// It requires the team (sub-agent) mode to be enabled.
func (c *Config) ReviewerEnabled() bool {
	return c.SubAgentsEnabled() && c.TeamReviewer
}

// MaxReviewRounds returns the configured implement→review→fix loop budget,
// falling back to DefaultMaxReviewRounds when unset or invalid.
func (c *Config) MaxReviewRounds() int {
	if c == nil || c.TeamMaxReviewRounds <= 0 {
		return DefaultMaxReviewRounds
	}
	return c.TeamMaxReviewRounds
}

// GoalEnabled reports whether the goal contract is installed on the
// orchestrator (M1-11). It requires the team (sub-agent) mode to be enabled:
// the contract is a root-agent-only capability.
func (c *Config) GoalEnabled() bool {
	return c.SubAgentsEnabled() && c.GoalContract
}

// MaxGoalNudges returns the configured premature-final block budget,
// falling back to DefaultGoalMaxNudges when unset or invalid.
func (c *Config) MaxGoalNudges() int {
	if c == nil || c.GoalMaxNudges <= 0 {
		return DefaultGoalMaxNudges
	}
	return c.GoalMaxNudges
}

// PlanEnabled reports whether the Plan-Execute loop is installed on the
// orchestrator (M1-12). It requires the team (sub-agent) mode to be enabled:
// the loop is a root-agent-only capability.
func (c *Config) PlanEnabled() bool {
	return c.SubAgentsEnabled() && c.PlanExecute
}

// MaxPlanNudges returns the configured premature-final block budget for the
// plan loop, falling back to DefaultMaxPlanNudges when unset or invalid.
func (c *Config) MaxPlanNudges() int {
	if c == nil || c.PlanMaxNudges <= 0 {
		return DefaultMaxPlanNudges
	}
	return c.PlanMaxNudges
}

// EngineTimeout returns the duration used to bound a single LLM streaming run.
// It falls back to DefaultEngineTimeout when the configured value is invalid.
func (c *Config) EngineTimeout() time.Duration {
	if c == nil || c.EngineTimeoutSeconds <= 0 {
		return DefaultEngineTimeout
	}
	return time.Duration(c.EngineTimeoutSeconds) * time.Second
}

// ArtifactRoot returns the on-disk root directory for the working-state
// artifacts (PLAN/PROGRESS/LEARNINGS, M1-16). It is created during Load, so an
// empty value should never be returned for a loaded config.
func (c *Config) ArtifactRoot() string {
	if c == nil {
		return ""
	}
	return c.artifactRoot
}

// StateEnabled reports whether the working-state externalization
// (StateEnforcer, M1-16) is turned on. When false, the engine does not install
// the enforcer and no PLAN/PROGRESS/LEARNINGS files are written.
func (c *Config) StateEnabled() bool {
	return c != nil && c.stateEnabled
}

// SkillsRoot returns the shared (read-only) skills repository root (M2-03).
func (c *Config) SkillsRoot() string {
	if c == nil {
		return ""
	}
	return c.skillsRoot
}

// SkillsDataDir returns the per-user private skills repository root (M2-03).
func (c *Config) SkillsDataDir() string {
	if c == nil {
		return ""
	}
	return c.skillsDataDir
}

// SkillWarmStart reports whether skill warm-start injection is enabled (M2-03).
func (c *Config) SkillWarmStart() bool {
	return c != nil && c.skillWarmStart
}

// SkillWarmStartMaxChars returns the length cap for the warm-start context block (M2-03).
func (c *Config) SkillWarmStartMaxChars() int {
	if c == nil || c.skillWarmMaxChars <= 0 {
		return skillrepo.DefaultWarmStartMaxChars
	}
	return c.skillWarmMaxChars
}

// WorktreeIsolation reports whether background taskrun sub-tasks should execute
// inside an isolated git worktree and merge back on completion (M2-05).
func (c *Config) WorktreeIsolation() bool {
	return c != nil && c.worktreeIsolation
}

// ToolSearchEnabled reports whether the lazy toolbox (M2-06) is turned on.
// When on, the engine exposes tool_search/call_tool control tools so the model
// can discover and invoke MCP tools on demand instead of loading every tool
// declaration into the context upfront.
func (c *Config) ToolSearchEnabled() bool {
	return c != nil && c.toolSearchEnabled
}

// KnowledgeEnabled reports whether the knowledge-base RAG injection (M5-02) is
// turned on. When on, the engine retrieves related knowledge slices from the
// user's knowledge bases before each chat turn and prepends them to the user
// message. When off, no retrieval is performed.
func (c *Config) KnowledgeEnabled() bool {
	return c != nil && c.knowledgeEnabled
}

// BudgetEnabled reports whether the platform-level budget guardrail (M3-04) is
// turned on. When off, all budget checks pass through (emergency recovery /
// local debugging only). Thresholds are configured via DB BudgetPolicy rows.
func (c *Config) BudgetEnabled() bool {
	return c != nil && c.budgetEnabled
}

// CheckpointEnabled reports whether the human-in-the-loop checkpoint (M3-05) is
// turned on. When off, ask-level dangerous commands in unattended mode are denied
// directly (the pre-M3-05 behavior); when on, they generate a pending checkpoint
// that an operator must approve/reject via the UI.
func (c *Config) CheckpointEnabled() bool {
	return c != nil && c.checkpointEnabled
}

// DBAutoMigrate reports whether the GORM AutoMigrate development fallback is
// enabled (M3-08). Schema is normally managed by the versioned migrations in
// internal/repo; this switch only exists for local model iteration and must be
// left off in production.
func (c *Config) DBAutoMigrate() bool {
	return c != nil && c.dbAutoMigrate
}

// MetricsEnabled reports whether the observability metrics subsystem (M3-09) is
// turned on. When on, the server initializes an OpenTelemetry MeterProvider and
// exposes a /metrics endpoint (Prometheus text format). When off, Record*
// calls are no-ops and /metrics returns 404.
func (c *Config) MetricsEnabled() bool {
	return c != nil && c.metricsEnabled
}

// LogFormat returns the structured log output format ("json" or "text", M7-06).
// Falls back to "json" when unset or invalid.
func (c *Config) LogFormat() string {
	if c == nil || c.logFormat == "" {
		return "json"
	}
	return c.logFormat
}

// LogLevel returns the structured log level string ("debug"/"info"/"warn"/"error", M7-06).
// Falls back to "info" when unset or invalid.
func (c *Config) LogLevel() string {
	if c == nil || c.logLevel == "" {
		return "info"
	}
	return c.logLevel
}

// WebhookRateLimit returns the per-token webhook trigger cap within the rate
// window (M4-03). It falls back to 10 when unset or invalid.
func (c *Config) WebhookRateLimit() int {
	if c == nil || c.webhookRateLimit <= 0 {
		return 10
	}
	return c.webhookRateLimit
}

// WebhookRateWindow returns the sliding-window duration for the webhook rate
// limiter (M4-03). It falls back to 60 seconds when unset or invalid.
func (c *Config) WebhookRateWindow() time.Duration {
	if c == nil || c.webhookRateWindow <= 0 {
		return 60 * time.Second
	}
	return c.webhookRateWindow
}

// RecoveryMaxAttempts returns the per-restart retry budget for resuming an
// unfinished automation run during cross-day recovery (M4-05). It falls back to
// 3 when unset or invalid. Runs that keep failing past this budget are marked
// failed so a never-converging loop is not resumed on every restart.
func (c *Config) RecoveryMaxAttempts() int {
	if c == nil || c.recoveryMaxAttempts <= 0 {
		return 3
	}
	return c.recoveryMaxAttempts
}

// RunModeString returns the configured runtime mode string (M4-06).
// It falls back to RunModeUnattended when the config is nil.
func (c *Config) RunModeString() string {
	if c == nil {
		return RunModeUnattended
	}
	return c.runMode
}

// WebhookNotifyURL returns the outbound webhook notification callback URL (M4-07).
// Empty means the outbound callback is disabled and only the in-app notification
// (notifications table) is written.
func (c *Config) WebhookNotifyURL() string {
	if c == nil {
		return ""
	}
	return c.webhookNotifyURL
}

// WebhookSignSecret returns the inbound webhook HMAC-SHA256 signing secret (M6-05).
// Empty means signature verification is disabled (backward compatible).
func (c *Config) WebhookSignSecret() string {
	if c == nil {
		return ""
	}
	return c.webhookSignSecret
}

// AlertWebhookToken returns the shared secret for the /api/alerts receiver (M7-04).
// Empty means token verification is disabled (backward compatible).
func (c *Config) AlertWebhookToken() string {
	if c == nil {
		return ""
	}
	return c.alertWebhookToken
}

// AlertNotifyUserIDs returns the admin user IDs that platform alerts are delivered to (M7-04).
// Falls back to [1] when unset or invalid.
func (c *Config) AlertNotifyUserIDs() []uint {
	if c == nil || len(c.alertNotifyUserIDs) == 0 {
		return []uint{1}
	}
	return c.alertNotifyUserIDs
}

// parseUintList splits a comma-separated string into a slice of uints, skipping
// any non-numeric tokens (used for ALERT_NOTIFY_USER_IDS).
func parseUintList(s string) []uint {
	parts := splitAndTrim(s, ",")
	out := make([]uint, 0, len(parts))
	for _, p := range parts {
		if n, err := strconv.ParseUint(p, 10, 64); err == nil && n > 0 {
			out = append(out, uint(n))
		}
	}
	if len(out) == 0 {
		return []uint{1}
	}
	return out
}

// EvolutionEnabled reports whether the background skill-evolution scanner (M5-03)
// is turned on. When on, the server periodically scans finished sessions, extracts
// candidate skills via LLM, applies the quality gate, and persists them as pending
// candidates for human approval. When off, no background scan runs (you can still
// trigger a scan manually via POST /api/skill-candidates/scan).
func (c *Config) EvolutionEnabled() bool {
	return c != nil && c.evolutionEnabled
}

// EvolutionInterval returns the period between background evolution scans (M5-03).
// It falls back to one hour when unset or invalid.
func (c *Config) EvolutionInterval() time.Duration {
	if c == nil || c.evolutionInterval <= 0 {
		return time.Hour
	}
	return c.evolutionInterval
}

// ExecutorBackend returns the configured execution backend (M8-02):
// executor.BackendHost (default) or executor.BackendDocker. When nil or unset,
// host is returned (backward compatible: old deployments behave unchanged).
func (c *Config) ExecutorBackend() executor.Backend {
	if c == nil || c.executorBackend == "" {
		return executor.BackendHost
	}
	return c.executorBackend
}

// DockerOptions returns the Docker execution backend configuration (M8-02),
// mirroring the env overrides (DOCKER_IMAGE/DOCKER_NETWORK/DOCKER_READ_ONLY/
// DOCKER_BIN/DOCKER_TIMEOUT_SECONDS). Only meaningful when ExecutorBackend() ==
// BackendDocker; fields left at executor zero values fall back to the safe
// defaults inside the executor package (alpine + none + read-only).
func (c *Config) DockerOptions() executor.DockerOptions {
	if c == nil {
		return executor.DockerOptions{}
	}
	ro := c.dockerReadOnly
	timeout := c.dockerTimeout
	return executor.DockerOptions{
		Image:    c.dockerImage,
		Network:  c.dockerNetwork,
		ReadOnly: &ro,
		Bin:      c.dockerBin,
		Timeout:  timeout,
	}
}

// IsUnattended reports whether the server runs in unattended mode (M4-06).
// This is the default (and the safe default for a 24h autonomous platform):
// dangerous commands default to deny, ask-level commands become queued
// checkpoints, and budget guardrails stay active. Only an explicit
// RUN_MODE=attended switches this off (for attended debugging sessions).
func (c *Config) IsUnattended() bool {
	return c == nil || c.runMode != RunModeAttended
}

// ExecutorMode maps the runtime mode to an executor.Mode (M4-06): unattended
// → ModeUnattended (ask → checkpoint/deny), attended → ModeInteractive (ask →
// deny, since there is no synchronous confirmation channel in the API flow).
func (c *Config) ExecutorMode() executor.Mode {
	if c.IsUnattended() {
		return executor.ModeUnattended
	}
	return executor.ModeInteractive
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// splitAndTrim splits s by sep and returns non-empty trimmed parts.
func splitAndTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// CORSAllowedOrigins returns the allowlisted frontend origins for CORS (MX-07).
// An empty slice means no cross-origin requests are permitted.
func (c *Config) CORSAllowedOrigins() []string {
	if c == nil {
		return nil
	}
	return c.corsAllowedOrigins
}

// RateLimitLogin returns the login/register anti-bruteforce settings (MX-07):
// enabled flag, per-IP limit within the window, and the window duration.
func (c *Config) RateLimitLogin() (enabled bool, limit int, window time.Duration) {
	if c == nil {
		return true, 10, 60 * time.Second
	}
	return c.rateLimitLoginEnabled, c.rateLimitLoginLimit, c.rateLimitLoginWindow
}

// RateLimitChat returns the chat anti-abuse settings (MX-07): enabled flag,
// per-user limit within the window, and the window duration.
func (c *Config) RateLimitChat() (enabled bool, limit int, window time.Duration) {
	if c == nil {
		return true, 30, 60 * time.Second
	}
	return c.rateLimitChatEnabled, c.rateLimitChatLimit, c.rateLimitChatWindow
}

// envOrDefaultBool parses a boolean env var (1/t/true/0/f/false, case-insensitive).
// Invalid values fall back to the provided default with a warning.
func envOrDefaultBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		log.Printf("[WARN] %s is not a valid boolean; using default %v", key, fallback)
		return fallback
	}
	return b
}

func envOrDefaultInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
		log.Printf("[WARN] %s is not a valid integer; using default %d", key, fallback)
	}
	return fallback
}
