package engine

import (
	"testing"
	"time"
)

// TestEngineTimeoutDefaultAndOverride 验证单次对话超时可由 ModelConfig.Timeout 注入，
// 不传（<=0）时回退默认 90s（M0.5-05：超时提为配置项，不再硬编码 90s）。
func TestEngineTimeoutDefaultAndOverride(t *testing.T) {
	e, err := New(ModelConfig{ModelID: "m", BaseURL: "http://x/v1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if e.cfg.Timeout != 90*time.Second {
		t.Fatalf("未指定超时期望默认 90s，实际 %v", e.cfg.Timeout)
	}

	e2, err := New(ModelConfig{ModelID: "m", BaseURL: "http://x/v1", Timeout: 30 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if e2.cfg.Timeout != 30*time.Second {
		t.Fatalf("显式指定 30s 应被保留，实际 %v", e2.cfg.Timeout)
	}
}
