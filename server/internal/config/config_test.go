package config

import (
	"os"
	"testing"
	"time"
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
