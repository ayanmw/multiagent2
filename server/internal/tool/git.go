// git.go 实现「Git 工具集」（M2-01）：把版本控制能力暴露给代码代理（Coder / 单代理根 Agent）。
//
// 提供的工具（全部经 executor.SafeExecutor 执行，危险命令策略默认无人值守 deny，
// 正常 git 子命令放行）：
//   - git_status   查看工作区状态（porcelain 格式，仓库干净时输出为空）；
//   - git_diff     查看工作区与索引的差异（cached=true 比对已暂存内容）；
//   - git_commit   暂存全部改动（git add -A）并提交，message 必填；
//   - git_log      查看最近若干条提交（oneline 格式）；
//   - git_branch   列出分支；create=true 且 name 非空时创建并切换到新分支。
//
// 设计要点：
//   - 所有命令经 SafeExecutor（与 CodeAct 同一套危险命令策略）执行，禁止裸用 HostExecutor.Run；
//   - git 命令经 executor.RunCommand 以 argv 形式直调（不经 shell），彻底规避 Windows cmd.exe 引号
//     转义问题，且提交说明含空格/特殊字符也能作为单一参数精确传递（缩小命令注入面）；
//   - git 的部分正常场景会返回非零退出码（如 git diff 有改动返回 1、git commit 无改动返回 1），
//     这些一律视为「正常输出」而非错误，故 runGit 不把非零退出码当失败处理。
//
// 本包只依赖 executor 与框架 tool/function，业务层（engine/api）只消费 []tool.Tool。
package codectool

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"

	"github.com/ayanmw/multiagent2/server/internal/executor"
)

// Git 工具名称常量（工具名是 Agent 与框架之间的契约，集中定义避免各处硬编码）。
const (
	ToolGitStatus = "git_status"
	ToolGitDiff   = "git_diff"
	ToolGitCommit = "git_commit"
	ToolGitLog    = "git_log"
	ToolGitBranch = "git_branch"
)

// GitToolNames 返回 Git 工具集的全部工具名（供上层枚举/校验）。
func GitToolNames() []string {
	return []string{ToolGitStatus, ToolGitDiff, ToolGitCommit, ToolGitLog, ToolGitBranch}
}

// NewGitExecutor 为工作目录构造一个经危险命令策略包装的执行器（M2-01），
// 供 Git 工具与 workspace 创建时的自动 git init 复用。workdir 必须存在且为目录。
// auditor 为审计器：nil 时回落到日志审计（LogAuditor）；业务层传入 repo.NewDBAuditor
// 可使 git 命令同样写入审计日志（M3-01 执行审计落库）。
// cp 为无人值守下 ask 危险命令的「人工检查点」落库回调（M3-05），语义同 NewCodeAct。
func NewGitExecutor(workdir string, auditor executor.Auditor, cp executor.Checkpointer) (executor.Executor, error) {
	if workdir == "" {
		return nil, fmt.Errorf("codectool: workdir 不能为空")
	}
	host, err := executor.NewHostExecutor(workdir)
	if err != nil {
		return nil, err
	}
	// 复用 CodeAct 同款危险命令策略：正常 git 子命令（status/diff/commit/log/branch/add）
	// 不在黑名单内，只有 git push --force / git reset --hard / git checkout -- 等高风险
	// 子命令在无人值守模式下被拒（见 executor.DefaultDangerousRules）。
	if auditor == nil {
		auditor = executor.NewLogAuditor(nil)
	}
	return executor.NewSafeExecutor(
		host,
		executor.NewDangerousCommandPolicy(executor.ModeUnattended),
		auditor,
		nil, // 无人值守：ask 类命令不交交互确认
		cp,  // 无人值守 ask → 生成人工检查点（nil 时退化 deny，M3-05）
	), nil
}

// runGit 通过执行器以 argv 形式运行一条 git 子命令，返回合并后的 stdout/stderr。
// 经 executor.RunCommand 直调 git（不经 shell），彻底规避 Windows cmd.exe 引号转义问题，
// 且含空格的提交说明（如 "add hello.txt"）也能作为单一参数精确传递、缩小命令注入面。
// git 在「有改动时 diff 退出码 1」「无改动时 commit 退出码 1」等均属正常，
// 故这里不把非零退出码当错误——只要不是被安全策略拒绝或命令启动失败，都返回输出。
// 命中 ask 策略且无人值守下生成人工检查点时，返回「⏸ 已创建人工检查点」提示（M3-05）。
func runGit(ctx context.Context, ex executor.Executor, args ...string) (string, error) {
	if ex == nil {
		return "", fmt.Errorf("codectool: 执行器未初始化")
	}
	if len(args) == 0 {
		return "", fmt.Errorf("codectool: git 子命令不能为空")
	}
	res, err := ex.RunCommand(ctx, "git", args...)
	if err != nil {
		var cpErr *executor.CheckpointError
		if errors.As(err, &cpErr) {
			return "⏸ 已创建人工检查点 " + cpErr.ID + "（" + cpErr.Reason + "），等待管理员审批后再执行；本轮运行已暂停。", nil
		}
		if errors.Is(err, executor.ErrCommandDenied) {
			// 被危险命令策略拒绝：作为正常结果返回拒绝说明，便于 Agent 自适应。
			return "⛔ 命令被安全策略拒绝：" + err.Error(), nil
		}
		return "", err
	}
	var sb strings.Builder
	sb.WriteString(res.Stdout)
	if res.Stderr != "" {
		if sb.Len() > 0 && !strings.HasSuffix(sb.String(), "\n") {
			sb.WriteString("\n")
		}
		sb.WriteString(res.Stderr)
	}
	return sb.String(), nil
}

// GitInit 在 workdir 内执行 `git init`（M2-01：workspace 创建时自动初始化仓库）。
// 返回合并后的 stdout/stderr；若 git 不可用或目录异常，返回错误（调用方可 best-effort 忽略）。
func GitInit(ctx context.Context, ex executor.Executor) (string, error) {
	return runGit(ctx, ex, "init")
}

// GitStatus 返回工作区状态（porcelain 格式，便于 Agent 解析；未跟踪以 ??、已修改以 M 标记）。
func GitStatus(ctx context.Context, ex executor.Executor) (string, error) {
	return runGit(ctx, ex, "status", "--short")
}

// GitDiff 返回工作区与索引的差异；cached=true 时比对已暂存（--cached）内容。
func GitDiff(ctx context.Context, ex executor.Executor, cached bool) (string, error) {
	if cached {
		return runGit(ctx, ex, "diff", "--cached")
	}
	return runGit(ctx, ex, "diff")
}

// GitCommit 先把改动暂存（git add -A）再提交，message 为提交说明且不可为空。
func GitCommit(ctx context.Context, ex executor.Executor, message string) (string, error) {
	if strings.TrimSpace(message) == "" {
		return "", fmt.Errorf("commit message 不能为空")
	}
	if _, err := runGit(ctx, ex, "add", "-A"); err != nil {
		return "", err
	}
	return runGit(ctx, ex, "commit", "-m", message)
}

// GitLog 返回最近 limit 条提交（oneline 格式）；limit<=0 时取默认 20。
func GitLog(ctx context.Context, ex executor.Executor, limit int) (string, error) {
	if limit <= 0 {
		limit = 20
	}
	return runGit(ctx, ex, "log", "--oneline", "-n", strconv.Itoa(limit))
}

// GitBranch 列出分支；create=true 且 name 非空时创建（并切换到）新分支。
func GitBranch(ctx context.Context, ex executor.Executor, name string, create bool) (string, error) {
	if create {
		if strings.TrimSpace(name) == "" {
			return "", fmt.Errorf("创建分支时 name 不能为空")
		}
		return runGit(ctx, ex, "checkout", "-b", name)
	}
	if strings.TrimSpace(name) != "" {
		return runGit(ctx, ex, "branch", name)
	}
	return runGit(ctx, ex, "branch", "--list")
}

// —— 工具入参与包装层（统一用 tool/function.NewFunctionTool，与 CodeAct 一致）——

type gitStatusInput struct{}

type gitDiffInput struct {
	Cached bool `json:"cached"`
}

type gitCommitInput struct {
	Message string `json:"message"`
}

type gitLogInput struct {
	Limit int `json:"limit"`
}

type gitBranchInput struct {
	Name   string `json:"name"`
	Create bool   `json:"create"`
}

func gitStatusTool(ex executor.Executor) tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, _ gitStatusInput) (string, error) {
			return GitStatus(ctx, ex)
		},
		function.WithName(ToolGitStatus),
		function.WithDescription("查看当前 git 仓库的工作区状态（porcelain 格式）：未跟踪文件以 ?? 标记、"+
			"已修改文件以 M 标记、已暂存以 A 标记。仓库干净（无未提交改动）时输出为空。"+
			"本工具只读，不会修改任何文件。"),
	)
}

func gitDiffTool(ex executor.Executor) tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, in gitDiffInput) (string, error) {
			return GitDiff(ctx, ex, in.Cached)
		},
		function.WithName(ToolGitDiff),
		function.WithDescription("查看当前 git 仓库的差异。"+
			"cached=true 时显示已暂存（git add 后）但还未提交的内容；"+
			"cached 缺省或 false 时显示工作区相对已暂存内容的改动。返回 unified diff 文本。"+
			"本工具只读，不会修改任何文件。"),
	)
}

func gitCommitTool(ex executor.Executor) tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, in gitCommitInput) (string, error) {
			return GitCommit(ctx, ex, in.Message)
		},
		function.WithName(ToolGitCommit),
		function.WithDescription("把当前工作区的全部改动暂存（git add -A）并提交。"+
			"message 参数必填，为本次提交的说明（建议简明描述改了什么）。"+
			"提交成功返回提交摘要；若没有可提交的改动，返回 git 的提示信息。"),
	)
}

func gitLogTool(ex executor.Executor) tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, in gitLogInput) (string, error) {
			return GitLog(ctx, ex, in.Limit)
		},
		function.WithName(ToolGitLog),
		function.WithDescription("查看当前 git 仓库的最近提交历史（oneline 格式，每行一条：<短哈希> <提交说明>）。"+
			"limit 可选，指定最多显示条数（缺省 20）。本工具只读。"),
	)
}

func gitBranchTool(ex executor.Executor) tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, in gitBranchInput) (string, error) {
			return GitBranch(ctx, ex, in.Name, in.Create)
		},
		function.WithName(ToolGitBranch),
		function.WithDescription("查看或创建 git 分支。"+
			"不传 name 时列出所有本地分支（当前分支以 * 标记）；"+
			"传 name 且 create=true 时创建并切换到该分支（git checkout -b <name>）；"+
			"仅传 name（create 缺省）时创建一个新分支但不切换。"),
	)
}

// NewGitTools 构造一组经危险命令策略包装的 Git 工具（M2-01）。
// workdir 必须存在（调用方负责创建，api 层按 workspace 本地目录传入），
// 内部使用 NewGitExecutor 包装 SafeExecutor，禁止裸用 HostExecutor.Run。
// auditor 为审计器（nil 回落日志审计）；传入 repo.NewDBAuditor 可使 git 命令落库审计（M3-01）。
func NewGitTools(workdir string, auditor executor.Auditor, cp executor.Checkpointer) ([]tool.Tool, error) {
	if workdir == "" {
		return nil, fmt.Errorf("codectool: workdir 不能为空")
	}
	ex, err := NewGitExecutor(workdir, auditor, cp)
	if err != nil {
		return nil, err
	}
	return []tool.Tool{
		gitStatusTool(ex),
		gitDiffTool(ex),
		gitCommitTool(ex),
		gitLogTool(ex),
		gitBranchTool(ex),
	}, nil
}

// NewCodeActWithGit 返回「CodeAct 工具集 + Git 工具集」（M2-01），用于单代理模式的根 Agent：
// 既有文件读写/执行能力，又能显式 git 提交与查看变更。team 模式下则改由 Coder 子代理持有
// （见 codeagent.NewCoder），二者不要重复装配以免工具名冲突。
// auditor 为审计器（nil 回落日志审计）；传入 repo.NewDBAuditor 可使本次会话全部命令落库审计（M3-01）。
// cp 为无人值守下 ask 危险命令的「人工检查点」落库回调（M3-05），语义同 NewCodeAct。
func NewCodeActWithGit(workdir string, auditor executor.Auditor, cp executor.Checkpointer) ([]tool.Tool, error) {
	code, err := NewCodeAct(workdir, auditor, cp)
	if err != nil {
		return nil, err
	}
	git, err := NewGitTools(workdir, auditor, cp)
	if err != nil {
		return nil, err
	}
	return append(code, git...), nil
}
