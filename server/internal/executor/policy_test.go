package executor

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestSafeExecutor_DenyRootDelete 验证致命级黑名单（rm -rf /）被直接拒绝并写审计。
func TestSafeExecutor_DenyRootDelete(t *testing.T) {
	h, _ := NewHostExecutor(t.TempDir())
	aud := NewMemoryAuditor()
	se := NewSafeExecutor(h, NewDangerousCommandPolicy(ModeUnattended), aud, nil)

	res, err := se.Run(context.Background(), "rm -rf /")
	if err == nil {
		t.Fatal("rm -rf / 应被拒绝")
	}
	if !errors.Is(err, ErrCommandDenied) {
		t.Fatalf("应为 ErrCommandDenied，实际 %v", err)
	}
	if res != nil {
		t.Fatalf("被拒命令不应返回 Result，实际 %+v", res)
	}
	all := aud.All()
	if len(all) != 1 {
		t.Fatalf("应写 1 条审计，实际 %d", len(all))
	}
	if all[0].Allowed {
		t.Fatal("审计应记为未放行")
	}
}

// TestSafeExecutor_SudoRootDelete 验证归一化后 sudo rm -rf / 也能命中 deny。
func TestSafeExecutor_SudoRootDelete(t *testing.T) {
	h, _ := NewHostExecutor(t.TempDir())
	aud := NewMemoryAuditor()
	se := NewSafeExecutor(h, NewDangerousCommandPolicy(ModeUnattended), aud, nil)

	if _, err := se.Run(context.Background(), "sudo   RM   -rf    /"); err == nil || !errors.Is(err, ErrCommandDenied) {
		t.Fatalf("sudo rm -rf / 应被拒: %v", err)
	}
	if len(aud.All()) != 1 || aud.All()[0].Allowed {
		t.Fatalf("应审计为未放行: %+v", aud.All())
	}
}

// TestSafeExecutor_AskDeniedInUnattended 验证无人值守下 ask 类命令（git push --force）降级为 deny 并审计。
func TestSafeExecutor_AskDeniedInUnattended(t *testing.T) {
	h, _ := NewHostExecutor(t.TempDir())
	aud := NewMemoryAuditor()
	se := NewSafeExecutor(h, NewDangerousCommandPolicy(ModeUnattended), aud, nil)

	// 策略层即拒绝，不依赖 git 是否可用。
	_, err := se.Run(context.Background(), "git push --force origin main")
	if err == nil || !errors.Is(err, ErrCommandDenied) {
		t.Fatalf("无人值守下 git push --force 应被拒: %v", err)
	}
	if len(aud.All()) != 1 || aud.All()[0].Allowed {
		t.Fatalf("应审计为未放行: %+v", aud.All())
	}
}

// TestSafeExecutor_AskAllowedInInteractive 验证交互模式且用户确认时 ask 类命令被放行执行。
func TestSafeExecutor_AskAllowedInInteractive(t *testing.T) {
	h, _ := NewHostExecutor(t.TempDir())
	aud := NewMemoryAuditor()
	se := NewSafeExecutor(h, NewDangerousCommandPolicy(ModeInteractive), aud,
		func(command, reason string) bool { return true })

	// rm -rf ./build 是 ask 规则，交互确认后应真正执行（无 build 目录，rm 为空操作退出 0）。
	res, err := se.Run(context.Background(), "rm -rf ./build")
	if err != nil {
		t.Fatalf("交互模式确认后应放行执行: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("rm -rf ./build 退出码应为 0，实际 %d (stderr=%q)", res.ExitCode, res.Stderr)
	}
	all := aud.All()
	if len(all) != 1 || !all[0].Allowed {
		t.Fatalf("应审计为放行: %+v", all)
	}
}

// TestSafeExecutor_AskDeniedByHandler 验证交互模式但用户拒绝时 ask 类命令被拒。
func TestSafeExecutor_AskDeniedByHandler(t *testing.T) {
	h, _ := NewHostExecutor(t.TempDir())
	aud := NewMemoryAuditor()
	se := NewSafeExecutor(h, NewDangerousCommandPolicy(ModeInteractive), aud,
		func(command, reason string) bool { return false })

	_, err := se.Run(context.Background(), "rm -rf ./build")
	if err == nil || !errors.Is(err, ErrCommandDenied) {
		t.Fatalf("交互模式用户拒绝应被拒: %v", err)
	}
	if aud.All()[0].Allowed {
		t.Fatal("审计应记为未放行")
	}
}

// TestSafeExecutor_AllowNormalCommand 验证正常命令（echo）被放行并审计为 allowed。
func TestSafeExecutor_AllowNormalCommand(t *testing.T) {
	h, _ := NewHostExecutor(t.TempDir())
	aud := NewMemoryAuditor()
	se := NewSafeExecutor(h, NewDangerousCommandPolicy(ModeUnattended), aud, nil)

	res, err := se.Run(context.Background(), "echo safe")
	if err != nil {
		t.Fatalf("正常命令应放行: %v", err)
	}
	if res.ExitCode != 0 || !strings.Contains(res.Stdout, "safe") {
		t.Fatalf("正常命令结果异常: %+v", res)
	}
	if len(aud.All()) != 1 || !aud.All()[0].Allowed {
		t.Fatalf("正常命令应审计为放行: %+v", aud.All())
	}
}

// TestSafeExecutor_ForceDeleteRecursiveAsk 验证无人值守下广义 rm -rf（非根目录）也按 ask→deny 处置。
func TestSafeExecutor_ForceDeleteRecursiveAsk(t *testing.T) {
	h, _ := NewHostExecutor(t.TempDir())
	aud := NewMemoryAuditor()
	se := NewSafeExecutor(h, NewDangerousCommandPolicy(ModeUnattended), aud, nil)

	_, err := se.Run(context.Background(), "rm -rf ./build")
	if err == nil || !errors.Is(err, ErrCommandDenied) {
		t.Fatalf("unattended 下 rm -rf ./build 应为 ask→deny: %v", err)
	}
	if len(aud.All()) != 1 || aud.All()[0].Allowed {
		t.Fatalf("应审计为未放行: %+v", aud.All())
	}
}

// TestNormalizeCommand 验证命令归一化（小写、折叠空白）。
func TestNormalizeCommand(t *testing.T) {
	if got := normalizeCommand("   RM   -rf    /  "); got != "rm -rf /" {
		t.Fatalf("归一化失败: %q", got)
	}
}
