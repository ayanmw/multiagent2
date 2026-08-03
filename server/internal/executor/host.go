package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// defaultHostTimeout 是单次命令执行的默认超时（60s）。
// 命令执行时间不可无限延长，避免 Agent 卡死或资源被长期占用。
const defaultHostTimeout = 60 * time.Second

// HostExecutor 在宿主机的某个受限工作目录下执行命令，是 Executor 的默认实现（M1-04）。
// 它通过固定 cmd.Dir 把命令约束在 workdir 内，并为每次执行套上上下文超时。
// Docker 沙箱等更严格的后端留作 M3 接口预留。
type HostExecutor struct {
	workdir string        // 受限工作目录（绝对路径）
	timeout time.Duration // 单次命令超时；<=0 时使用 defaultHostTimeout
	shell   []string      // shell 调用形式，如 ["bash","-c"] 或 ["cmd.exe","/c"]
}

// NewHostExecutor 构造一个受限于 workdir 的主机执行器。
// workdir 必须存在且为目录；为空时回退到当前进程工作目录（os.Getwd）。
func NewHostExecutor(workdir string) (*HostExecutor, error) {
	if workdir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("executor: 获取当前工作目录失败: %w", err)
		}
		workdir = wd
	}
	abs, err := filepath.Abs(workdir)
	if err != nil {
		return nil, fmt.Errorf("executor: 解析工作目录失败: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("executor: 工作目录不存在: %s", abs)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("executor: 工作目录不是目录: %s", abs)
	}
	return &HostExecutor{
		workdir: abs,
		timeout: defaultHostTimeout,
		shell:   detectShell(),
	}, nil
}

// NewHostExecutorWithTimeout 同 NewHostExecutor，但显式指定单次命令超时。
func NewHostExecutorWithTimeout(workdir string, timeout time.Duration) (*HostExecutor, error) {
	h, err := NewHostExecutor(workdir)
	if err != nil {
		return nil, err
	}
	if timeout > 0 {
		h.timeout = timeout
	}
	return h, nil
}

// detectShell 根据运行平台选择 shell 调用形式。
// Windows 用 cmd.exe /c（git bash 环境也兼容），类 Unix 优先 bash 否则 sh。
func detectShell() []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd.exe", "/c"}
	}
	if sh, err := exec.LookPath("bash"); err == nil {
		return []string{sh, "-c"}
	}
	return []string{"sh", "-c"}
}

// Run 在受限工作目录内执行命令。
// 返回 Result：正常/非零退出为有效结果（nil error + 对应 ExitCode）；
// 超时则 ExitCode=-1 并返回 context.DeadlineExceeded 包装的错误；
// 命令无法启动（如 shell 缺失）返回非 nil error。
func (h *HostExecutor) Run(ctx context.Context, command string) (*Result, error) {
	if command == "" {
		return nil, fmt.Errorf("executor: 命令不能为空")
	}
	timeout := h.timeout
	if timeout <= 0 {
		timeout = defaultHostTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	shellArgs := append(append([]string{}, h.shell[1:]...), command)
	cmd := exec.CommandContext(runCtx, h.shell[0], shellArgs...)
	cmd.Dir = h.workdir // 关键：把所有命令约束在该工作目录下
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	return h.finishCommand(cmd, runCtx, timeout, &stdout, &stderr)
}

// RunCommand 以 argv 形式直接执行 name + args，不经过 shell 字符串解析。
// 其余语义（cwd 约束、超时、退出码映射）与 Run 完全一致。
// 适用于 git 这类需精确传递含空格参数（如提交说明）的外部程序，
// 规避 Windows cmd.exe 对带引号命令字符串的二次解析导致的参数崩坏。
func (h *HostExecutor) RunCommand(ctx context.Context, name string, args ...string) (*Result, error) {
	if name == "" {
		return nil, fmt.Errorf("executor: 程序名不能为空")
	}
	timeout := h.timeout
	if timeout <= 0 {
		timeout = defaultHostTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(runCtx, name, args...)
	cmd.Dir = h.workdir
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	return h.finishCommand(cmd, runCtx, timeout, &stdout, &stderr)
}

// finishCommand 运行已构造好的 *exec.Cmd，收集输出并按统一规则映射退出码。
func (h *HostExecutor) finishCommand(cmd *exec.Cmd, runCtx context.Context, timeout time.Duration, stdout, stderr *bytes.Buffer) (*Result, error) {
	res := &Result{ExitCode: 0}
	err := cmd.Run()
	if err != nil {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			// 超时中断：保留已捕获的输出，退出码置 -1。
			res.Stdout = stdout.String()
			res.Stderr = stderr.String()
			res.ExitCode = -1
			return res, fmt.Errorf("executor: 命令执行超时(>%s): %w", timeout, context.DeadlineExceeded)
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// 非零退出码是命令的正常结果，不算错误。
			res.Stdout = stdout.String()
			res.Stderr = stderr.String()
			res.ExitCode = exitErr.ExitCode()
			return res, nil
		}
		// 命令启动失败（程序缺失、权限等）：返回错误。
		return nil, fmt.Errorf("executor: 命令启动失败: %w", err)
	}
	res.Stdout = stdout.String()
	res.Stderr = stderr.String()
	return res, nil
}

// Workdir 返回受限工作目录的绝对路径。
func (h *HostExecutor) Workdir() string { return h.workdir }
