package executor

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// longRunningCommand 返回一段必然超过测试超时的命令（按平台选择）。
func longRunningCommand() string {
	if runtime.GOOS == "windows" {
		// ping 4 次约 3s，远超 200ms 超时。
		return "ping -n 4 127.0.0.1 >nul"
	}
	return "sleep 2"
}

func TestHostExecutor_Normal(t *testing.T) {
	dir := t.TempDir()
	h, err := NewHostExecutor(dir)
	if err != nil {
		t.Fatalf("NewHostExecutor failed: %v", err)
	}
	if h.Workdir() != dir {
		t.Fatalf("Workdir 应为 %q，实际 %q", dir, h.Workdir())
	}

	res, err := h.Run(context.Background(), "echo hello")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("ExitCode 应为 0，实际 %d", res.ExitCode)
	}
	if !strings.Contains(res.Stdout, "hello") {
		t.Fatalf("stdout 应包含 hello，实际 %q", res.Stdout)
	}
}

func TestHostExecutor_NonZeroExit(t *testing.T) {
	h, err := NewHostExecutor(t.TempDir())
	if err != nil {
		t.Fatalf("NewHostExecutor failed: %v", err)
	}
	res, err := h.Run(context.Background(), "exit 1")
	if err != nil {
		t.Fatalf("非零退出不应返回 error: %v", err)
	}
	if res.ExitCode != 1 {
		t.Fatalf("ExitCode 应为 1，实际 %d", res.ExitCode)
	}
}

// TestHostExecutor_CwdConstrained 验证命令被约束在 workdir 内：
// 通过 shell 重定向写出的文件应落在 workdir，而非进程当前目录（cwd 越界防护）。
func TestHostExecutor_CwdConstrained(t *testing.T) {
	dir := t.TempDir()
	h, err := NewHostExecutor(dir)
	if err != nil {
		t.Fatalf("NewHostExecutor failed: %v", err)
	}
	// echo ... > probe.txt 由 shell 处理重定向，Windows cmd.exe 与 Unix bash 均支持。
	res, err := h.Run(context.Background(), "echo inside > probe.txt")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("ExitCode 应为 0，实际 %d (stderr=%q)", res.ExitCode, res.Stderr)
	}
	// 文件必须写在 workdir 内，证明执行被 cwd 约束。
	data, rerr := os.ReadFile(filepath.Join(dir, "probe.txt"))
	if rerr != nil {
		t.Fatalf("probe.txt 未落在 workdir 内（cwd 越界）: %v", rerr)
	}
	if !strings.Contains(string(data), "inside") {
		t.Fatalf("probe.txt 内容异常: %q", string(data))
	}
}

func TestHostExecutor_Timeout(t *testing.T) {
	// 200ms 超时，命令必然超过它。
	h, err := NewHostExecutorWithTimeout(t.TempDir(), 200*time.Millisecond)
	if err != nil {
		t.Fatalf("NewHostExecutorWithTimeout failed: %v", err)
	}
	res, err := h.Run(context.Background(), longRunningCommand())
	if err == nil {
		t.Fatal("预期超时错误，实际 nil")
	}
	if res == nil || res.ExitCode != -1 {
		t.Fatalf("超时 ExitCode 应为 -1，实际 %+v", res)
	}
	if ctxErr := context.DeadlineExceeded; !strings.Contains(err.Error(), "超时") {
		_ = ctxErr
		t.Fatalf("错误信息应含『超时』，实际 %q", err.Error())
	}
}

func TestNewHostExecutor_RejectsBadWorkdir(t *testing.T) {
	if _, err := NewHostExecutor(filepath.Join(t.TempDir(), "no-such-dir")); err == nil {
		t.Fatal("不存在的工作目录应返回错误")
	}
	// 文件而非目录也应被拒绝。
	f := filepath.Join(t.TempDir(), "a-file")
	if werr := os.WriteFile(f, []byte("x"), 0o644); werr != nil {
		t.Fatalf("准备测试文件失败: %v", werr)
	}
	if _, err := NewHostExecutor(f); err == nil {
		t.Fatal("非目录路径应返回错误")
	}
}

func TestHostExecutor_EmptyCommand(t *testing.T) {
	h, err := NewHostExecutor(t.TempDir())
	if err != nil {
		t.Fatalf("NewHostExecutor failed: %v", err)
	}
	if _, err := h.Run(context.Background(), ""); err == nil {
		t.Fatal("空命令应返回错误")
	}
}
