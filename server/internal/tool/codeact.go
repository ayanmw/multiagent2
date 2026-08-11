// Package codectool 实现 CodeAgent 的代码执行工具集（M1-06）。
//
// 提供的工具（均为基于 executor.Executor 的安全封装）：
//   - shell_exec  在受限工作目录内执行 shell 命令，返回 stdout/stderr/退出码；
//   - file_read   读取工作目录内的文件内容；
//   - file_write  写入/创建文件（自动建目录）；
//   - file_edit   把文件中的某段文本替换为新文本（支持期望替换次数校验）。
//
// 安全约束（见 docs/03 2.1 与 LEARNINGS）：
//   - 所有命令执行必须经由 executor.SafeExecutor（危险命令策略包装），禁止裸用 HostExecutor.Run；
//   - 文件类工具的路径一律解析到工作目录内，超出边界（path traversal）一律拒绝，
//     避免 Agent 越权读写系统文件。
//
// 本包只依赖 executor 与框架 tool/function，业务层（engine/api）只消费 []tool.Tool。
package codectool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"

	"github.com/ayanmw/multiagent2/server/internal/executor"
)

// resolveSafePath 把工具传入的路径解析为工作目录内的绝对路径，防止越界读写。
// 绝对路径与相对路径都按「相对 workdir 解析后是否仍在 workdir 内」校验，
// 越界（含 .. 逃逸）一律报错。符号链接解析留待 M3 沙箱后端，M1 阶段不做。
func resolveSafePath(workdir, p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("路径不能为空")
	}
	var abs string
	if filepath.IsAbs(p) {
		abs = filepath.Clean(p)
	} else {
		abs = filepath.Join(workdir, p)
	}
	rel, err := filepath.Rel(workdir, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("路径越界：%s 不在工作目录 %s 内", p, workdir)
	}
	return abs, nil
}

// ShellExec 是 shell_exec 的纯逻辑实现（便于单测，不依赖框架工具包装）。
// 通过 ex 执行命令，返回「exit_code / stdout / stderr」三段式可读结果。
// 命令被危险策略拒绝时返回可读的拒绝说明（非 error），便于 Agent 自适应。
// 命中 ask 策略且无人值守下生成人工检查点时，返回「⏸ 已创建人工检查点」提示（M3-05）。
func ShellExec(ctx context.Context, ex executor.Executor, command string) (string, error) {
	if command == "" {
		return "", fmt.Errorf("command 不能为空")
	}
	res, err := ex.Run(ctx, command)
	if err != nil {
		var cpErr *executor.CheckpointError
		if errors.As(err, &cpErr) {
			// 无人值守下命中 ask 危险命令：已生成人工检查点并暂停本轮运行。
			return "⏸ 已创建人工检查点 " + cpErr.ID + "（" + cpErr.Reason + "），等待管理员审批后再执行；本轮运行已暂停。", nil
		}
		if errors.Is(err, executor.ErrCommandDenied) {
			// 危险命令：作为正常结果返回拒绝说明，而不是抛错中断 Agent。
			return "⛔ 命令被安全策略拒绝：" + err.Error(), nil
		}
		return "", err
	}
	var sb strings.Builder
	sb.WriteString("exit_code: ")
	sb.WriteString(strconv.Itoa(res.ExitCode))
	sb.WriteString("\n--- stdout ---\n")
	sb.WriteString(res.Stdout)
	sb.WriteString("\n--- stderr ---\n")
	sb.WriteString(res.Stderr)
	return sb.String(), nil
}

// FileRead 是 file_read 的纯逻辑实现：读取工作目录内文件内容。
func FileRead(workdir, path string) (string, error) {
	abs, err := resolveSafePath(workdir, path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", fmt.Errorf("读取文件失败: %w", err)
	}
	return string(data), nil
}

// FileWrite 是 file_write 的纯逻辑实现：写入/创建文件（自动建目录）。
func FileWrite(workdir, path, content string) (string, error) {
	abs, err := resolveSafePath(workdir, path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", fmt.Errorf("创建目录失败: %w", err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("写入文件失败: %w", err)
	}
	return fmt.Sprintf("已写入 %s（%d 字节）", path, len(content)), nil
}

// FileEdit 是 file_edit 的纯逻辑实现：把文件中 oldString 替换为 newString。
// expectedReplacements>0 时要求精确匹配该次数，否则报错以避免歧义替换。
func FileEdit(workdir, path, oldString, newString string, expectedReplacements int) (string, error) {
	if oldString == "" {
		return "", fmt.Errorf("old_string 不能为空")
	}
	abs, err := resolveSafePath(workdir, path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", fmt.Errorf("读取文件失败: %w", err)
	}
	content := string(data)
	count := strings.Count(content, oldString)
	if count == 0 {
		return "", fmt.Errorf("未在 %s 中找到 old_string", path)
	}
	if expectedReplacements > 0 && count != expectedReplacements {
		return "", fmt.Errorf("期望替换 %d 处，实际找到 %d 处，已中止以避免歧义替换", expectedReplacements, count)
	}
	if err := os.WriteFile(abs, []byte(strings.Replace(content, oldString, newString, -1)), 0o644); err != nil {
		return "", fmt.Errorf("写回文件失败: %w", err)
	}
	return fmt.Sprintf("已编辑 %s：替换 %d 处", path, count), nil
}

// shellExecInput 是 shell_exec 工具入参。
type shellExecInput struct {
	Command string `json:"command"`
}

// fileReadInput 是 file_read 工具入参。
type fileReadInput struct {
	Path string `json:"path"`
}

// fileWriteInput 是 file_write 工具入参。
type fileWriteInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// fileEditInput 是 file_edit 工具入参。
type fileEditInput struct {
	Path                 string `json:"path"`
	OldString            string `json:"old_string"`
	NewString            string `json:"new_string"`
	ExpectedReplacements int    `json:"expected_replacements"` // 0 = 替换全部
}

// shellExecTool 构造 shell_exec 工具。
func shellExecTool(ex executor.Executor) tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, in shellExecInput) (string, error) {
			return ShellExec(ctx, ex, in.Command)
		},
		function.WithName(ToolShellExec),
		function.WithDescription("在受限制的工作目录中执行 shell 命令，返回 exit_code、stdout 与 stderr。"+
			"可用于列出目录、编译运行、查看文件等。注意：危险命令（如 rm -rf /、git push --force）会被安全策略拒绝。"),
	)
}

// fileReadTool 构造 file_read 工具。
func fileReadTool(workdir string) tool.Tool {
	return function.NewFunctionTool(
		func(_ context.Context, in fileReadInput) (string, error) {
			return FileRead(workdir, in.Path)
		},
		function.WithName(ToolFileRead),
		function.WithDescription("读取工作目录内指定路径的文件内容并以字符串返回。"+
			"路径相对于工作目录；超出工作目录的请求会被拒绝。"),
	)
}

// fileWriteTool 构造 file_write 工具。
func fileWriteTool(workdir string) tool.Tool {
	return function.NewFunctionTool(
		func(_ context.Context, in fileWriteInput) (string, error) {
			return FileWrite(workdir, in.Path, in.Content)
		},
		function.WithName(ToolFileWrite),
		function.WithDescription("在工作目录内写入/创建文件，自动创建缺失的父目录。"+
			"path 相对于工作目录；content 为完整文件内容（覆盖写入）。"),
	)
}

// fileEditTool 构造 file_edit 工具。
func fileEditTool(workdir string) tool.Tool {
	return function.NewFunctionTool(
		func(_ context.Context, in fileEditInput) (string, error) {
			return FileEdit(workdir, in.Path, in.OldString, in.NewString, in.ExpectedReplacements)
		},
		function.WithName(ToolFileEdit),
		function.WithDescription("把工作目录内文件中的 old_string 替换为 new_string。"+
			"expected_replacements>0 时要求精确匹配该次数（用于避免歧义）；为 0 时替换全部匹配。"),
	)
}

// CodeActTools 返回一组基于 Executor 的代码执行工具（M1-06）。
// workdir 是文件类工具的根目录（相对路径以其为基准，且限制不越界）；
// ex 必须是经过危险命令策略包装的执行器（NewSafeExecutor(HostExecutor, policy, auditor, nil)），
// 禁止裸用 HostExecutor.Run（见 LEARNINGS M1-05）。
func CodeActTools(workdir string, ex executor.Executor) []tool.Tool {
	return []tool.Tool{
		shellExecTool(ex),
		fileReadTool(workdir),
		fileWriteTool(workdir),
		fileEditTool(workdir),
	}
}

// normalizeExecutorMode 把入参 executor.Mode 归一到合法值；非法/零值回落 Unattended
// （无人值守是 24h 自主平台的安全默认，见 M4-06）。
func normalizeExecutorMode(mode executor.Mode) executor.Mode {
	if mode == executor.ModeUnattended || mode == executor.ModeInteractive {
		return mode
	}
	return executor.ModeUnattended
}

// NewCodeAct 构造一组经危险命令策略包装的 CodeAct 工具（M1-06 业务入口）。
// workdir 必须存在（调用方负责创建，api 层按 WorkspaceRoot/<uid> 自动建）；
// 内部使用 NewSafeExecutor(HostExecutor, 危险命令策略(mode), auditor, nil, cp)，
// 禁止裸用 HostExecutor（见 LEARNINGS M1-05）。
// auditor 为审计器：nil 时回落到日志审计（LogAuditor），不阻断命令执行；
// 业务层在请求级/worker 级传入 repo.NewDBAuditor（M3-01 执行审计落库）。
// cp 为无人值守下 ask 危险命令的「人工检查点」落库回调（M3-05）：传入后命中 ask 的命令
// 不再直接 deny，而是生成 checkpoint 并暂停；nil 时回退为直接 deny（与旧行为一致）。
// mode 为执行器运行模式（M4-06）：Unattended 时 ask→检查点/deny（自主 Loop 安全默认）；
// Interactive 时 ask→deny（有人值守调试会话）；非法值回落 Unattended。
func NewCodeAct(workdir string, auditor executor.Auditor, cp executor.Checkpointer, mode executor.Mode) ([]tool.Tool, error) {
	if workdir == "" {
		return nil, fmt.Errorf("codectool: workdir 不能为空")
	}
	host, err := executor.NewHostExecutor(workdir)
	if err != nil {
		return nil, err
	}
	if auditor == nil {
		auditor = executor.NewLogAuditor(nil)
	}
	ex := executor.NewSafeExecutor(
		host,
		executor.NewDangerousCommandPolicy(normalizeExecutorMode(mode)),
		auditor,
		nil, // 无人值守：ask 类命令不交交互确认
		cp,  // 无人值守 ask → 生成人工检查点（nil 时退化 deny，M3-05）
	)
	return CodeActTools(workdir, ex), nil
}
