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
