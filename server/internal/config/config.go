package config

import (
	"crypto/sha256"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	codeagent "github.com/ayanmw/multiagent2/server/internal/agent"
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
}

// DefaultMaxReviewRounds 是 CodeTeam「实现→审阅→修复」默认回环轮数上限（M1-09）。
const DefaultMaxReviewRounds = 2

// DefaultGoalMaxNudges 是目标契约默认的最大拦截次数（M1-11）。
// 超出后 fail-open 放行，避免模型不配合时把整轮 Run 卡死。
const DefaultGoalMaxNudges = 3

// DefaultMaxPlanNudges 是 Plan-Execute 循环默认的最大拦截次数（M1-12）。
// 超出后 fail-open 放行，避免模型不配合时把整轮 Run 卡死。
const DefaultMaxPlanNudges = 3

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

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
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
