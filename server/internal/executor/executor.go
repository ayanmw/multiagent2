// Package executor 定义代码执行的统一抽象（Executor 接口），
// 所有 shell/脚本执行都必须经由 Executor，禁止在业务层直接散写 os/exec。
//
// 设计原则（见 docs/03 2.1 与 LEARNINGS）：
//   - Executor 是统一接口，业务层（CodeAct 工具、子代理）只依赖接口，不依赖具体实现；
//   - M1-04 提供默认实现 HostExecutor（在受限工作目录下执行，cwd 约束）；
//   - Docker 沙箱等更严格的执行后端留作 M3 接口预留，未来只需新增一个 Executor 实现；
//   - 危险命令策略（黑名单 + allow/ask/deny）由 M1-05 在 Executor 之上叠加，不在本包实现。
package executor

import "context"

// Result 是命令执行的结果。
type Result struct {
	Stdout   string // 标准输出
	Stderr   string // 标准错误
	ExitCode int    // 退出码；0=正常，>0=命令自身非零退出，-1=被超时中断
}

// Executor 是所有代码执行后端的统一抽象。
// 任何代码执行（shell 命令、脚本运行）都必须经由 Executor.Run，
// 以便统一施加 cwd 约束、超时、危险命令策略与审计。
type Executor interface {
	// Run 在 Executor 的约束范围内执行命令，返回标准输出、标准错误与退出码。
	// 命令的具体解释由实现决定（HostExecutor 走 shell -c）。
	Run(ctx context.Context, command string) (*Result, error)

	// RunCommand 以 argv 形式（程序名 + 参数列表）直接执行命令，不经过 shell 字符串解析，
	// 避免 Windows cmd.exe 引号转义等跨平台差异；其余语义（cwd 约束、超时、退出码映射）
	// 与 Run 一致。适用于 git 这类需精确传递含空格参数（如提交说明）的外部程序，
	// 同时缩小命令注入面（参数不再经 shell 重新分词）。
	RunCommand(ctx context.Context, name string, args ...string) (*Result, error)

	// Workdir 返回当前受限工作目录的绝对路径（供工具、审计与展示使用）。
	Workdir() string
}
