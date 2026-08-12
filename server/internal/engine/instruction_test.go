package engine

import "testing"

// TestSingleInstruction_OverridePriority 验证 InstructionOverride 优先于内置默认，
// 且空覆盖回落「默认指令 + skillCtx」（M5-06）。
func TestSingleInstruction_OverridePriority(t *testing.T) {
	base := defaultInstruction + "[skill]"

	if got := singleInstruction(ModelConfig{}, "[skill]"); got != base {
		t.Fatalf("空覆盖应返回 默认+skillCtx，实际 %q", got)
	}
	if got := singleInstruction(ModelConfig{InstructionOverride: "custom"}, "[skill]"); got != "custom" {
		t.Fatalf("非空覆盖应返回 custom，实际 %q", got)
	}
	// 覆盖不叠加 skillCtx。
	if got := singleInstruction(ModelConfig{InstructionOverride: "custom"}, "[skill]"); got == base {
		t.Fatalf("覆盖不应叠加 skillCtx")
	}
}
