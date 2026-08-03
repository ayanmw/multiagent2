package engine

import (
	"os"
	"path/filepath"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// writeTempSkill 在临时目录写一个 SKILL.md 供 warm-start 扫描。
func writeTempSkill(t *testing.T, root, name, body string) {
	t.Helper()
	d := filepath.Join(root, name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestEngine_SkillWarmStart_WiresConfig 验证启用 warm-start 时引擎能正常构造
// （warm-start 在 New 内部扫描 roots 并把技能注入系统上下文）。用 dummy 模型参数，
// 不真正调用 LLM；仅验证接线不报错、warm-start 路径可执行。
func TestEngine_SkillWarmStart_WiresConfig(t *testing.T) {
	root := t.TempDir()
	writeTempSkill(t, root, "demo", "---\nname: demo\ndescription: 演示\n---\n演示技能正文")

	eng, err := New(ModelConfig{
		ModelID:        "dummy",
		BaseURL:        "http://localhost/v1",
		Protocol:       "openai",
		Timeout:        0,
		Tools:          []tool.Tool{},
		SkillWarmStart: true,
		SkillRoots:     []string{root},
		SkillMaxChars:  6000,
	})
	if err != nil {
		t.Fatalf("New 应在 warm-start 配置下成功: %v", err)
	}
	if eng == nil {
		t.Fatalf("引擎不应为 nil")
	}
	_ = eng.Close()
}

// TestEngine_SkillWarmStart_DisabledSkips 验证关闭 warm-start 或 roots 为空时跳过扫描。
func TestEngine_SkillWarmStart_DisabledSkips(t *testing.T) {
	// 关闭开关：即使 roots 非空也不扫描。
	eng, err := New(ModelConfig{
		ModelID:        "dummy",
		BaseURL:        "http://localhost/v1",
		Protocol:       "openai",
		SkillWarmStart: false,
		SkillRoots:     []string{t.TempDir()},
	})
	if err != nil {
		t.Fatalf("New 应成功: %v", err)
	}
	_ = eng.Close()

	// roots 为空：跳过扫描（构造成功）。
	eng2, err := New(ModelConfig{
		ModelID:        "dummy",
		BaseURL:        "http://localhost/v1",
		Protocol:       "openai",
		SkillWarmStart: true,
		SkillRoots:     nil,
	})
	if err != nil {
		t.Fatalf("roots 为空时应跳过扫描并成功: %v", err)
	}
	_ = eng2.Close()
}
