package config

import (
	"crypto/sha256"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"
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
	AgentMode            string // single / team（env AGENT_MODE，默认 single），M1-08 子代理委托开关
}

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

	// Agent 运行模式（M1-08）：team 时启用 Orchestrator→Coder 子代理委托。
	// 非法取值退回 single，避免误配把线上对话切到未预期的编排链路。
	mode := envOrDefault("AGENT_MODE", AgentModeSingle)
	if mode != AgentModeSingle && mode != AgentModeTeam {
		log.Printf("[WARN] AGENT_MODE=%q is invalid; using %q", mode, AgentModeSingle)
		mode = AgentModeSingle
	}
	cfg.AgentMode = mode

	return cfg
}

// SubAgentsEnabled reports whether the orchestrator/sub-agent delegation mode is on.
func (c *Config) SubAgentsEnabled() bool {
	return c != nil && c.AgentMode == AgentModeTeam
}

// EngineTimeout returns the duration used to bound a single LLM streaming run.
// It falls back to DefaultEngineTimeout when the configured value is invalid.
func (c *Config) EngineTimeout() time.Duration {
	if c == nil || c.EngineTimeoutSeconds <= 0 {
		return DefaultEngineTimeout
	}
	return time.Duration(c.EngineTimeoutSeconds) * time.Second
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
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
