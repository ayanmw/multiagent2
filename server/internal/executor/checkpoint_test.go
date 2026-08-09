package executor

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestSafeExecutor_AskCreatesCheckpointInUnattended 验证 M3-05 核心行为：
// 无人值守模式下命中 ask 策略的危险命令，若挂有 Checkpointer，则生成人工检查点并暂停，
// 而不是像旧行为那样直接 deny；命令本身不得被执行。
func TestSafeExecutor_AskCreatesCheckpointInUnattended(t *testing.T) {
	h, _ := NewHostExecutor(t.TempDir())
	aud := NewMemoryAuditor()
	var got CheckpointRequest
	calls := 0
	cp := func(req CheckpointRequest) (string, error) {
		calls++
		got = req
		return "CP-7", nil
	}
	se := NewSafeExecutor(h, NewDangerousCommandPolicy(ModeUnattended), aud, nil, cp)

	res, err := se.Run(context.Background(), "rm -rf ./build")
	if err == nil {
		t.Fatal("挂载 Checkpointer 后 ask 命令应返回「已生成检查点」错误")
	}
	if res != nil {
		t.Fatalf("暂停的命令不应返回 Result，实际 %+v", res)
	}
	if !errors.Is(err, ErrCheckpointCreated) {
		t.Fatalf("应可识别为 ErrCheckpointCreated，实际 %v", err)
	}
	// 关键语义：检查点 ≠ 策略拒绝，工具层需据此区分「等待审批」与「被拒」。
	if errors.Is(err, ErrCommandDenied) {
		t.Fatalf("检查点错误不应被解包成 ErrCommandDenied: %v", err)
	}
	var ce *CheckpointError
	if !errors.As(err, &ce) {
		t.Fatalf("应为 *CheckpointError，实际 %T", err)
	}
	if ce.ID != "CP-7" {
		t.Fatalf("检查点展示 ID 应回传，实际 %q", ce.ID)
	}
	if calls != 1 {
		t.Fatalf("Checkpointer 应被调用 1 次，实际 %d", calls)
	}
	if got.Command != "rm -rf ./build" {
		t.Fatalf("落库命令不符: %q", got.Command)
	}
	if got.Workdir == "" || got.Reason == "" {
		t.Fatalf("检查点上下文缺失: %+v", got)
	}
	all := aud.All()
	if len(all) != 1 || all[0].Allowed {
		t.Fatalf("应写 1 条未放行审计，实际 %+v", all)
	}
	if !strings.Contains(all[0].Note, "CP-7") {
		t.Fatalf("审计 Note 应带检查点编号，实际 %q", all[0].Note)
	}
}

// TestSafeExecutor_CheckpointOnRunCommand 验证 argv 入口（RunCommand）与字符串入口行为一致。
func TestSafeExecutor_CheckpointOnRunCommand(t *testing.T) {
	h, _ := NewHostExecutor(t.TempDir())
	cp := func(req CheckpointRequest) (string, error) { return "CP-9", nil }
	se := NewSafeExecutor(h, NewDangerousCommandPolicy(ModeUnattended), nil, nil, cp)

	_, err := se.RunCommand(context.Background(), "git", "push", "--force", "origin", "main")
	var ce *CheckpointError
	if !errors.As(err, &ce) || ce.ID != "CP-9" {
		t.Fatalf("argv 入口也应生成检查点，实际 %v", err)
	}
}

// TestSafeExecutor_CheckpointFailureFallsBackToDeny 验证落库失败时安全退化为直接拒绝。
func TestSafeExecutor_CheckpointFailureFallsBackToDeny(t *testing.T) {
	h, _ := NewHostExecutor(t.TempDir())
	aud := NewMemoryAuditor()
	cp := func(req CheckpointRequest) (string, error) { return "", errors.New("db down") }
	se := NewSafeExecutor(h, NewDangerousCommandPolicy(ModeUnattended), aud, nil, cp)

	_, err := se.Run(context.Background(), "rm -rf ./build")
	if err == nil || !errors.Is(err, ErrCommandDenied) {
		t.Fatalf("检查点落库失败应退化为 deny，实际 %v", err)
	}
	if errors.Is(err, ErrCheckpointCreated) {
		t.Fatal("落库失败时不应宣称已生成检查点")
	}
	if len(aud.All()) != 1 || aud.All()[0].Allowed {
		t.Fatalf("应审计为未放行: %+v", aud.All())
	}
}

// TestSafeExecutor_InteractivePrefersAskHandler 验证交互模式仍以 AskHandler 为准，
// 不因挂了 Checkpointer 就改走「生成检查点」路径（避免打断人在场的确认流程）。
func TestSafeExecutor_InteractivePrefersAskHandler(t *testing.T) {
	h, _ := NewHostExecutor(t.TempDir())
	called := false
	cp := func(req CheckpointRequest) (string, error) { called = true; return "CP-1", nil }
	se := NewSafeExecutor(h, NewDangerousCommandPolicy(ModeInteractive), nil,
		func(command, reason string) bool { return true }, cp)

	if _, err := se.Run(context.Background(), "rm -rf ./build"); err != nil {
		t.Fatalf("交互确认后应放行执行: %v", err)
	}
	if called {
		t.Fatal("交互模式不应生成人工检查点")
	}
}

// TestSafeExecutor_FatalDenyNeverCheckpoints 验证致命级 deny（rm -rf /）不会退让为检查点，
// 人工检查点只作用于 ask 级命令。
func TestSafeExecutor_FatalDenyNeverCheckpoints(t *testing.T) {
	h, _ := NewHostExecutor(t.TempDir())
	called := false
	cp := func(req CheckpointRequest) (string, error) { called = true; return "CP-2", nil }
	se := NewSafeExecutor(h, NewDangerousCommandPolicy(ModeUnattended), nil, nil, cp)

	_, err := se.Run(context.Background(), "rm -rf /")
	if err == nil || !errors.Is(err, ErrCommandDenied) {
		t.Fatalf("致命命令必须直接拒绝，实际 %v", err)
	}
	if called {
		t.Fatal("致命命令不应生成人工检查点")
	}
}

// TestSafeExecutor_NoCheckpointerKeepsLegacyDeny 验证未挂 Checkpointer 时保持旧行为（deny）。
func TestSafeExecutor_NoCheckpointerKeepsLegacyDeny(t *testing.T) {
	h, _ := NewHostExecutor(t.TempDir())
	se := NewSafeExecutor(h, NewDangerousCommandPolicy(ModeUnattended), nil, nil, nil)

	_, err := se.Run(context.Background(), "rm -rf ./build")
	if err == nil || !errors.Is(err, ErrCommandDenied) {
		t.Fatalf("无 Checkpointer 时应保持 deny，实际 %v", err)
	}
}
