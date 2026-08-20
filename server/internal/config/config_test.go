package config

import (
	"os"
	"testing"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/executor"
)

func TestEngineTimeoutDefault(t *testing.T) {
	c := &Config{}
	if c.EngineTimeout() != DefaultEngineTimeout {
		t.Fatalf("期望默认 %v，实际 %v", DefaultEngineTimeout, c.EngineTimeout())
	}
	if c.EngineTimeoutSeconds != 0 {
		t.Fatalf("未配置时 EngineTimeoutSeconds 应为 0，实际 %d", c.EngineTimeoutSeconds)
	}
}

func TestEngineTimeoutFromEnv(t *testing.T) {
	os.Setenv("ENGINE_TIMEOUT_SECONDS", "120")
	defer os.Unsetenv("ENGINE_TIMEOUT_SECONDS")
	c := &Config{EngineTimeoutSeconds: envOrDefaultInt("ENGINE_TIMEOUT_SECONDS", int(DefaultEngineTimeout/time.Second))}
	if c.EngineTimeoutSeconds != 120 {
		t.Fatalf("期望 120，实际 %d", c.EngineTimeoutSeconds)
	}
	if c.EngineTimeout() != 120*time.Second {
		t.Fatalf("期望 120s，实际 %v", c.EngineTimeout())
	}
}

func TestEngineTimeoutInvalidEnvFallsBack(t *testing.T) {
	os.Setenv("ENGINE_TIMEOUT_SECONDS", "not-a-number")
	defer os.Unsetenv("ENGINE_TIMEOUT_SECONDS")
	c := &Config{EngineTimeoutSeconds: envOrDefaultInt("ENGINE_TIMEOUT_SECONDS", int(DefaultEngineTimeout/time.Second))}
	if c.EngineTimeoutSeconds != int(DefaultEngineTimeout/time.Second) {
		t.Fatalf("非法值时期望回退到 %d，实际 %d", int(DefaultEngineTimeout/time.Second), c.EngineTimeoutSeconds)
	}
}

// TestTeamConfigDefaults 验证 M1-09「team 配置化」的默认值与依赖关系：
// Reviewer 只在 team 模式下生效，回环轮数缺省/非法时回退默认值。
func TestTeamConfigDefaults(t *testing.T) {
	// single 模式：即便 TeamReviewer=true 也不启用 Reviewer。
	single := &Config{AgentMode: AgentModeSingle, TeamReviewer: true}
	if single.ReviewerEnabled() {
		t.Fatal("single 模式下不应启用 Reviewer")
	}

	// team 模式 + 默认开关：Reviewer 生效，轮数回退默认值。
	team := &Config{AgentMode: AgentModeTeam, TeamReviewer: true}
	if !team.ReviewerEnabled() {
		t.Fatal("team 模式下应启用 Reviewer")
	}
	if got := team.MaxReviewRounds(); got != DefaultMaxReviewRounds {
		t.Fatalf("未配置轮数时期望 %d，实际 %d", DefaultMaxReviewRounds, got)
	}

	// 显式关闭 Reviewer：退回 Orchestrator+Coder 二人组。
	off := &Config{AgentMode: AgentModeTeam, TeamReviewer: false}
	if off.ReviewerEnabled() {
		t.Fatal("TEAM_REVIEWER=false 时不应启用 Reviewer")
	}

	// 自定义轮数生效。
	custom := &Config{AgentMode: AgentModeTeam, TeamReviewer: true, TeamMaxReviewRounds: 5}
	if got := custom.MaxReviewRounds(); got != 5 {
		t.Fatalf("期望轮数 5，实际 %d", got)
	}
}

// TestEnvOrDefaultBool 覆盖布尔环境变量解析（合法/非法/缺省）。
func TestEnvOrDefaultBool(t *testing.T) {
	if got := envOrDefaultBool("TEAM_REVIEWER_TEST_UNSET", true); !got {
		t.Fatal("未设置时应返回默认值 true")
	}
	os.Setenv("TEAM_REVIEWER_TEST", "false")
	defer os.Unsetenv("TEAM_REVIEWER_TEST")
	if got := envOrDefaultBool("TEAM_REVIEWER_TEST", true); got {
		t.Fatal("显式 false 应覆盖默认值")
	}
	os.Setenv("TEAM_REVIEWER_TEST", "not-a-bool")
	if got := envOrDefaultBool("TEAM_REVIEWER_TEST", true); !got {
		t.Fatal("非法值应回退默认值 true")
	}
}

// TestRunModeDefaults 验证 M4-06 默认运行模式为无人值守（24h 自主平台的安全默认）：
// nil 配置与未显式切换时，RunModeString=unattended、IsUnattended=true、ExecutorMode=Unattended。
func TestRunModeDefaults(t *testing.T) {
	// nil 配置：全部回落无人值守安全默认。
	var nilCfg *Config
	if nilCfg.RunModeString() != RunModeUnattended {
		t.Fatalf("nil 配置 RunModeString 期望 %q，实际 %q", RunModeUnattended, nilCfg.RunModeString())
	}
	if !nilCfg.IsUnattended() {
		t.Fatal("nil 配置应为无人值守")
	}
	if nilCfg.ExecutorMode() != executor.ModeUnattended {
		t.Fatalf("nil 配置 ExecutorMode 期望 %v，实际 %v", executor.ModeUnattended, nilCfg.ExecutorMode())
	}

	// 显式为空 runMode（模拟 Load 未赋值前的零值）：RunModeString 返回原始空串，
	// 但 IsUnattended/ExecutorMode 归一化到无人值守安全默认（Load 也会把空值补为 unattended）。
	empty := &Config{}
	if empty.RunModeString() != "" {
		t.Fatalf("空 Config 的 RunModeString 应返回原始空串，实际 %q", empty.RunModeString())
	}
	if !empty.IsUnattended() {
		t.Fatal("空 runMode 应归一化为无人值守")
	}
	if empty.ExecutorMode() != executor.ModeUnattended {
		t.Fatalf("空 runMode ExecutorMode 期望 %v，实际 %v", executor.ModeUnattended, empty.ExecutorMode())
	}
}

// TestRunModeAttended 验证显式 RUN_MODE=attended 时切换为有人值守模式。
func TestRunModeAttended(t *testing.T) {
	attended := &Config{runMode: RunModeAttended}
	if attended.IsUnattended() {
		t.Fatal("RUN_MODE=attended 时应为有人值守")
	}
	if attended.ExecutorMode() != executor.ModeInteractive {
		t.Fatalf("attended ExecutorMode 期望 %v，实际 %v", executor.ModeInteractive, attended.ExecutorMode())
	}
}

// TestRunModeInvalidFallsBack 验证非法 RUN_MODE 回落无人值守（绝不因误配把线上
// 24h 自主循环切到非预期的 attended 行为）。
func TestRunModeInvalidFallsBack(t *testing.T) {
	bad := &Config{runMode: "bogus-mode"}
	if !bad.IsUnattended() {
		t.Fatal("非法 RUN_MODE 应回落无人值守")
	}
	if bad.ExecutorMode() != executor.ModeUnattended {
		t.Fatalf("非法 RUN_MODE ExecutorMode 期望 %v，实际 %v", executor.ModeUnattended, bad.ExecutorMode())
	}
}

// TestDBAutoMigrate_DefaultOff 验收 M6-03：「生产默认关闭」的运营约束。
// 零值 Config 的 DBAutoMigrate() 必须为 false，绝不默認开启 AutoMigrate 兜底。
func TestDBAutoMigrate_DefaultOff(t *testing.T) {
	var c Config
	if c.DBAutoMigrate() {
		t.Fatal("默认 dbAutoMigrate 应为 false（生产默认关闭）")
	}
}

// TestDBAutoMigrate_EnvDriven 验收 M6-03：DB_AUTO_MIGRATE 由环境变量驱动，
// 默认值 false，显式 true 才开启（仅本地开发用）。
func TestDBAutoMigrate_EnvDriven(t *testing.T) {
	t.Run("unset_defaults_off", func(t *testing.T) {
		t.Setenv("DB_AUTO_MIGRATE", "")
		if Load().DBAutoMigrate() {
			t.Fatal("DB_AUTO_MIGRATE 未设置时应默认 false")
		}
	})
	t.Run("explicit_false", func(t *testing.T) {
		t.Setenv("DB_AUTO_MIGRATE", "false")
		if Load().DBAutoMigrate() {
			t.Fatal("DB_AUTO_MIGRATE=false 应使 DBAutoMigrate()=false")
		}
	})
	t.Run("explicit_true", func(t *testing.T) {
		t.Setenv("DB_AUTO_MIGRATE", "true")
		if !Load().DBAutoMigrate() {
			t.Fatal("DB_AUTO_MIGRATE=true 应使 DBAutoMigrate()=true")
		}
	})
}

// TestLogFormatLevel_Defaults 验收 M7-06：结构化日志默认 json/info，
// LOG_FORMAT/LOG_LEVEL 非法取值回落默认。
func TestLogFormatLevel_Defaults(t *testing.T) {
	var c Config
	if got := c.LogFormat(); got != "json" {
		t.Errorf("零值 LogFormat() = %q, want json", got)
	}
	if got := c.LogLevel(); got != "info" {
		t.Errorf("零值 LogLevel() = %q, want info", got)
	}
}

func TestLogFormatLevel_EnvDriven(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		t.Setenv("LOG_FORMAT", "")
		t.Setenv("LOG_LEVEL", "")
		c := Load()
		if c.LogFormat() != "json" || c.LogLevel() != "info" {
			t.Errorf("默认应为 json/info, got %s/%s", c.LogFormat(), c.LogLevel())
		}
	})
	t.Run("explicit", func(t *testing.T) {
		t.Setenv("LOG_FORMAT", "text")
		t.Setenv("LOG_LEVEL", "debug")
		c := Load()
		if c.LogFormat() != "text" || c.LogLevel() != "debug" {
			t.Errorf("应解析为 text/debug, got %s/%s", c.LogFormat(), c.LogLevel())
		}
	})
	t.Run("invalid_falls_back", func(t *testing.T) {
		t.Setenv("LOG_FORMAT", "yaml")
		t.Setenv("LOG_LEVEL", "verbose")
		c := Load()
		if c.LogFormat() != "json" || c.LogLevel() != "info" {
			t.Errorf("非法值应回落 json/info, got %s/%s", c.LogFormat(), c.LogLevel())
		}
	})
}

// TestExecutorBackend_Default 验收 M8-02：EXECUTOR_BACKEND 默认 host（向后兼容），
// 非法值回落 host。
func TestExecutorBackend_Default(t *testing.T) {
	var c Config
	if got := c.ExecutorBackend(); got != executor.BackendHost {
		t.Errorf("零值 ExecutorBackend() = %q, want host", got)
	}
}

func TestExecutorBackend_EnvDriven(t *testing.T) {
	t.Run("default_host", func(t *testing.T) {
		t.Setenv("EXECUTOR_BACKEND", "")
		if Load().ExecutorBackend() != executor.BackendHost {
			t.Fatal("EXECUTOR_BACKEND 未设置时应默认 host")
		}
	})
	t.Run("docker", func(t *testing.T) {
		t.Setenv("EXECUTOR_BACKEND", "docker")
		if Load().ExecutorBackend() != executor.BackendDocker {
			t.Fatal("EXECUTOR_BACKEND=docker 应返回 BackendDocker")
		}
	})
	t.Run("invalid_falls_back_host", func(t *testing.T) {
		t.Setenv("EXECUTOR_BACKEND", "vm")
		if Load().ExecutorBackend() != executor.BackendHost {
			t.Fatal("非法 EXECUTOR_BACKEND 应回落 host")
		}
	})
}

// TestDockerOptions_Defaults 验收 M8-02：DockerOptions 默认安全配置——
// 无网络(none) + 只读根(true) + alpine 镜像 + docker CLI + 60s 超时。
func TestDockerOptions_Defaults(t *testing.T) {
	var c Config
	opts := c.DockerOptions()
	if opts.Image != "" || opts.Network != "" || opts.Bin != "" || opts.Timeout != 0 {
		t.Errorf("零值 Config 的 DockerOptions 应留空交由 executor 回落默认, got %+v", opts)
	}
	// Load 后（未设 env）：config 显式给默认，与 executor 包常量一致。
	t.Setenv("EXECUTOR_BACKEND", "")
	cfg := Load()
	opts = cfg.DockerOptions()
	if opts.Image != executor.DefaultDockerImage || opts.Network != executor.DefaultDockerNetwork || opts.Bin != executor.DefaultDockerBin {
		t.Errorf("默认 DockerOptions 与 executor 常量不一致: %+v", opts)
	}
	if opts.ReadOnly == nil || !*opts.ReadOnly {
		t.Error("默认 ReadOnly 应为 true（只读根）")
	}
	if opts.Timeout != 60*time.Second {
		t.Errorf("默认 Timeout = %v, want 60s", opts.Timeout)
	}
}

func TestDockerOptions_EnvDriven(t *testing.T) {
	t.Setenv("EXECUTOR_BACKEND", "docker")
	t.Setenv("DOCKER_IMAGE", "ubuntu:24.04")
	t.Setenv("DOCKER_NETWORK", "bridge")
	t.Setenv("DOCKER_READ_ONLY", "false")
	t.Setenv("DOCKER_BIN", "podman")
	t.Setenv("DOCKER_TIMEOUT_SECONDS", "120")
	opts := Load().DockerOptions()
	if opts.Image != "ubuntu:24.04" || opts.Network != "bridge" || opts.Bin != "podman" {
		t.Errorf("env 覆盖未生效: %+v", opts)
	}
	if opts.ReadOnly == nil || *opts.ReadOnly {
		t.Error("DOCKER_READ_ONLY=false 应使 ReadOnly=false")
	}
	if opts.Timeout != 120*time.Second {
		t.Errorf("Timeout = %v, want 120s", opts.Timeout)
	}
}

// TestTaskRunBackend_Default 验收 M8-03：TASKRUN_BACKEND 默认 inprocess（向后兼容），
// queue 可切换，非法值回落 inprocess。
func TestTaskRunBackend_Default(t *testing.T) {
	var c Config
	if got := c.TaskRunBackend(); got != TaskRunBackendInprocess {
		t.Errorf("零值 TaskRunBackend() = %q, want %q", got, TaskRunBackendInprocess)
	}
}

func TestTaskRunBackend_EnvDriven(t *testing.T) {
	t.Run("default_inprocess", func(t *testing.T) {
		t.Setenv("TASKRUN_BACKEND", "")
		if got := Load().TaskRunBackend(); got != TaskRunBackendInprocess {
			t.Fatalf("TASKRUN_BACKEND 未设置时应默认 inprocess, got %q", got)
		}
	})
	t.Run("queue", func(t *testing.T) {
		t.Setenv("TASKRUN_BACKEND", "queue")
		if got := Load().TaskRunBackend(); got != TaskRunBackendQueue {
			t.Fatalf("TASKRUN_BACKEND=queue 应返回 %q, got %q", TaskRunBackendQueue, got)
		}
	})
	t.Run("invalid_falls_back_inprocess", func(t *testing.T) {
		t.Setenv("TASKRUN_BACKEND", "redis")
		if got := Load().TaskRunBackend(); got != TaskRunBackendInprocess {
			t.Fatalf("非法 TASKRUN_BACKEND 应回落 inprocess, got %q", got)
		}
	})
}

// TestTaskRunQueueOptions_Defaults 验收 M8-03：外部队列参数默认值
//（poll=1s / lease=30s / maxAttempts=3）。
func TestTaskRunQueueOptions_Defaults(t *testing.T) {
	var c Config
	if got := c.TaskRunQueuePollInterval(); got != DefaultTaskRunQueuePollInterval {
		t.Errorf("TaskRunQueuePollInterval() = %v, want %v", got, DefaultTaskRunQueuePollInterval)
	}
	if got := c.TaskRunQueueLease(); got != DefaultTaskRunQueueLease {
		t.Errorf("TaskRunQueueLease() = %v, want %v", got, DefaultTaskRunQueueLease)
	}
	if got := c.TaskRunQueueMaxAttempts(); got != DefaultTaskRunQueueMaxAttempts {
		t.Errorf("TaskRunQueueMaxAttempts() = %d, want %d", got, DefaultTaskRunQueueMaxAttempts)
	}
}

func TestTaskRunQueueOptions_EnvDriven(t *testing.T) {
	t.Setenv("TASKRUN_QUEUE_POLL_INTERVAL_MS", "250")
	t.Setenv("TASKRUN_QUEUE_LEASE_SECONDS", "5")
	t.Setenv("TASKRUN_QUEUE_MAX_ATTEMPTS", "7")
	cfg := Load()
	if got := cfg.TaskRunQueuePollInterval(); got != 250*time.Millisecond {
		t.Errorf("TaskRunQueuePollInterval() = %v, want 250ms", got)
	}
	if got := cfg.TaskRunQueueLease(); got != 5*time.Second {
		t.Errorf("TaskRunQueueLease() = %v, want 5s", got)
	}
	if got := cfg.TaskRunQueueMaxAttempts(); got != 7 {
		t.Errorf("TaskRunQueueMaxAttempts() = %d, want 7", got)
	}
}
