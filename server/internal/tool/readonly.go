// readonly.go 实现「只读工具集」（M1-10），供 Reviewer 等审阅型角色使用。
//
// 设计要点：
//   - 只读工具集**独立构造**（file_read + grep），不再由 CodeAct 全量工具集过滤而来，
//     从源头上保证 Reviewer 永远拿不到 file_write / file_edit / shell_exec；
//   - 提供 EnsureReadOnly 作为「兜底断言」：任何声称只读的工具集在装配前都要过一遍，
//     若混入写/执行工具则构造期直接报错（fail fast），避免未来重构悄悄放权；
//   - grep 与 file_read 一样走 resolveSafePath，检索范围被牢牢限制在工作目录内。
package codectool

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// 只读 / 变更类工具名称常量。工具名是 Agent 与框架之间的契约，集中定义避免各处硬编码。
const (
	ToolFileRead  = "file_read"
	ToolGrep      = "grep"
	ToolFileWrite = "file_write"
	ToolFileEdit  = "file_edit"
	ToolShellExec = "shell_exec"
)

// ErrMutatingTool 表示只读工具集中混入了具备写入/执行副作用的工具。
var ErrMutatingTool = errors.New("codectool: 只读工具集中混入了写/执行工具")

// readOnlyToolNames 是允许出现在只读工具集中的工具白名单。
var readOnlyToolNames = map[string]bool{
	ToolFileRead: true,
	ToolGrep:     true,
}

// mutatingToolNames 是明确具备副作用（写文件 / 执行命令）的工具黑名单。
var mutatingToolNames = map[string]bool{
	ToolFileWrite: true,
	ToolFileEdit:  true,
	ToolShellExec: true,
}

// IsReadOnlyToolName 报告工具名是否属于只读白名单。
func IsReadOnlyToolName(name string) bool { return readOnlyToolNames[name] }

// IsMutatingToolName 报告工具名是否属于写/执行黑名单。
func IsMutatingToolName(name string) bool { return mutatingToolNames[name] }

// ReadOnlyToolNames 返回只读工具名列表（顺序与 ReadOnlyTools 的返回一致）。
func ReadOnlyToolNames() []string { return []string{ToolFileRead, ToolGrep} }

// grep 的检索限额，防止 Agent 在大仓库里把上下文撑爆或长时间阻塞。
const (
	// DefaultGrepMaxResults 是默认最多返回的匹配行数。
	DefaultGrepMaxResults = 100
	// maxGrepFileSize 是参与检索的单文件大小上限（超过视为大文件跳过）。
	maxGrepFileSize = 1 << 20 // 1 MiB
	// maxGrepFiles 是一次检索最多扫描的文件数。
	maxGrepFiles = 2000
	// maxGrepLineLen 是单行输出的最大长度（超出截断，避免超长压缩行刷屏）。
	maxGrepLineLen = 300
)

// skippedGrepDirs 是检索时跳过的目录（依赖/产物/版本库元数据，审阅时无意义且巨大）。
var skippedGrepDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
	".idea":        true,
	".vscode":      true,
}

// GrepOptions 描述一次只读检索的参数。
type GrepOptions struct {
	// Pattern 是 Go 正则表达式（RE2），必填。
	Pattern string
	// Path 是检索起点，相对工作目录；为空表示工作目录根。可为文件或目录。
	Path string
	// IgnoreCase 开启大小写不敏感匹配。
	IgnoreCase bool
	// MaxResults 是最多返回的匹配行数；<=0 时取 DefaultGrepMaxResults。
	MaxResults int
}

// Grep 是 grep 工具的纯逻辑实现：在工作目录内按正则检索文本行（只读，无副作用）。
//
// 返回形如 `相对路径:行号: 行内容` 的多行结果；无匹配时返回可读提示（非 error），
// 便于 Agent 依据结果自行调整检索词，而不是被错误中断。
func Grep(workdir string, opt GrepOptions) (string, error) {
	if workdir == "" {
		return "", fmt.Errorf("workdir 不能为空")
	}
	if opt.Pattern == "" {
		return "", fmt.Errorf("pattern 不能为空")
	}
	expr := opt.Pattern
	if opt.IgnoreCase {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return "", fmt.Errorf("正则表达式非法: %w", err)
	}
	root := workdir
	if opt.Path != "" {
		if root, err = resolveSafePath(workdir, opt.Path); err != nil {
			return "", err
		}
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("检索路径不可用: %w", err)
	}
	limit := opt.MaxResults
	if limit <= 0 {
		limit = DefaultGrepMaxResults
	}

	var (
		matches  []string
		scanned  int
		truncate bool
	)
	appendFile := func(path string) error {
		if scanned >= maxGrepFiles {
			truncate = true
			return fs.SkipAll
		}
		scanned++
		lines, err := grepFile(workdir, path, re, limit-len(matches))
		if err != nil {
			return nil // 单个文件不可读（权限/二进制）不应中断整体检索
		}
		matches = append(matches, lines...)
		if len(matches) >= limit {
			truncate = true
			return fs.SkipAll
		}
		return nil
	}

	if info.IsDir() {
		werr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if p != root && skippedGrepDirs[d.Name()] {
					return fs.SkipDir
				}
				return nil
			}
			return appendFile(p)
		})
		if werr != nil && !errors.Is(werr, fs.SkipAll) {
			return "", fmt.Errorf("检索失败: %w", werr)
		}
	} else if err := appendFile(root); err != nil && !errors.Is(err, fs.SkipAll) {
		return "", fmt.Errorf("检索失败: %w", err)
	}

	if len(matches) == 0 {
		return fmt.Sprintf("未找到匹配 %q 的内容（已扫描 %d 个文件）", opt.Pattern, scanned), nil
	}
	var sb strings.Builder
	sb.WriteString("匹配 ")
	sb.WriteString(strconv.Itoa(len(matches)))
	sb.WriteString(" 行（扫描 ")
	sb.WriteString(strconv.Itoa(scanned))
	sb.WriteString(" 个文件）")
	if truncate {
		sb.WriteString("，结果已截断")
	}
	sb.WriteString("：\n")
	sb.WriteString(strings.Join(matches, "\n"))
	return sb.String(), nil
}

// grepFile 检索单个文件，返回至多 limit 条 `相对路径:行号: 内容` 结果。
// 大文件与二进制文件直接跳过（返回空结果，不报错）。
func grepFile(workdir, abs string, re *regexp.Regexp, limit int) ([]string, error) {
	if limit <= 0 {
		return nil, nil
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if st.Size() > maxGrepFileSize {
		return nil, nil
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, nil // 含 NUL 字节，视为二进制
	}
	rel, err := filepath.Rel(workdir, abs)
	if err != nil {
		rel = abs
	}
	rel = filepath.ToSlash(rel)

	var out []string
	for i, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		if !re.MatchString(line) {
			continue
		}
		if len(line) > maxGrepLineLen {
			line = line[:maxGrepLineLen] + "…"
		}
		out = append(out, fmt.Sprintf("%s:%d: %s", rel, i+1, line))
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// grepInput 是 grep 工具入参。
type grepInput struct {
	Pattern    string `json:"pattern"`
	Path       string `json:"path"`
	IgnoreCase bool   `json:"ignore_case"`
	MaxResults int    `json:"max_results"`
}

// grepTool 构造 grep 工具（只读检索）。
func grepTool(workdir string) tool.Tool {
	return function.NewFunctionTool(
		func(_ context.Context, in grepInput) (string, error) {
			return Grep(workdir, GrepOptions{
				Pattern:    in.Pattern,
				Path:       in.Path,
				IgnoreCase: in.IgnoreCase,
				MaxResults: in.MaxResults,
			})
		},
		function.WithName(ToolGrep),
		function.WithDescription("在工作目录内按正则表达式检索文本，返回「文件:行号: 内容」列表。"+
			"pattern 为 Go(RE2) 正则；path 可选，指定检索的子目录或单个文件（相对工作目录，默认全目录，"+
			"自动跳过 .git/node_modules 等）；ignore_case 忽略大小写；max_results 限制返回行数。"+
			"本工具只读，不会修改任何文件。"),
	)
}

// EnsureReadOnly 校验一组工具全部为只读：出现写/执行工具立即报错（fail fast）。
// 任何「声称只读」的工具集在交给审阅型代理前都应过一遍本函数，
// 避免后续重构不慎把 file_write/shell_exec 漏进 Reviewer（M1-10 安全护栏）。
func EnsureReadOnly(tools []tool.Tool) error {
	for _, t := range tools {
		if t == nil {
			continue
		}
		d := t.Declaration()
		if d == nil {
			return fmt.Errorf("%w: 存在无法识别的工具（Declaration 为空）", ErrMutatingTool)
		}
		if IsMutatingToolName(d.Name) {
			return fmt.Errorf("%w: %s", ErrMutatingTool, d.Name)
		}
		if !IsReadOnlyToolName(d.Name) {
			return fmt.Errorf("%w: %s 不在只读白名单内", ErrMutatingTool, d.Name)
		}
	}
	return nil
}

// ReadOnlyTools 构造只读工具集（M1-10）：file_read + grep，无任何写入/执行能力。
//
// 与 NewCodeAct 的区别：本函数**不创建任何 Executor**（连底层执行通道都没有），
// 因此从结构上就不可能执行命令；文件访问全部经 resolveSafePath 限制在 workdir 内。
func ReadOnlyTools(workdir string) ([]tool.Tool, error) {
	if workdir == "" {
		return nil, fmt.Errorf("codectool: workdir 不能为空")
	}
	if st, err := os.Stat(workdir); err != nil {
		return nil, fmt.Errorf("codectool: 工作目录不可用: %w", err)
	} else if !st.IsDir() {
		return nil, fmt.Errorf("codectool: 工作目录不是目录: %s", workdir)
	}
	tools := []tool.Tool{
		fileReadTool(workdir),
		grepTool(workdir),
	}
	if err := EnsureReadOnly(tools); err != nil {
		return nil, err
	}
	return tools, nil
}
