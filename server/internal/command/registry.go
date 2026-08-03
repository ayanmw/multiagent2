// Package command 定义斜杠命令注册表（M1-14）。
//
// 这是后端唯一事实源：Web 前端与未来 CLI 共用同一份命令元数据，
// 新增命令只需在 Builtin() 里追加一条，前端/CLI 自动渲染，无需改客户端代码。
// 命令分三类：
//   - client：完全由客户端处理（如 /clear 重置本地视图、/model /workspace 切换本地状态）；
//   - prompt：把「模板 + 用户输入」渲染成一段提示词，发给既有 /chat 或 /stream 端点，
//     复用已装配的 CodeAct / Goal / Plan 等能力，无需新增后端执行路径；
//   - endpoint：直接调用某条既有后端端点（Endpoint 字段给出相对路径）。
package command

import "strings"

// Command 定义一条斜杠命令的元数据，供前端/CLI 渲染命令浮层。
// JSON tag 即前端约定契约，勿随意改名。
type Command struct {
	// Name 命令名（不含前导斜杠），如 "run"。
	Name string `json:"name"`
	// Description 一句话说明。
	Description string `json:"description"`
	// Usage 用法示例，含前导斜杠，如 "/run <command>"。
	Usage string `json:"usage"`
	// Category 分组：system / workspace / agent。
	Category string `json:"category"`
	// Args 参数说明（可选，用于前端填参表单）。
	Args []Arg `json:"args,omitempty"`
	// Kind 解释方式：client / prompt / endpoint（见包注释）。
	Kind string `json:"kind"`
	// Template 仅 prompt 类使用：模板中的占位符 {{args}} 会被用户输入替换。
	Template string `json:"template,omitempty"`
	// Endpoint 仅 endpoint 类使用：相对路径，如 "/api/workspaces"。
	Endpoint string `json:"endpoint,omitempty"`
}

// Arg 描述命令的一个参数（用于前端填参表单）。
type Arg struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

// 命令类别常量。
const (
	CategorySystem    = "system"
	CategoryWorkspace = "workspace"
	CategoryAgent     = "agent"
)

// 命令解释方式常量。
const (
	KindClient   = "client"
	KindPrompt   = "prompt"
	KindEndpoint = "endpoint"
)

// argsPlaceholder 是 prompt 模板中用户输入参数的占位符。
const argsPlaceholder = "{{args}}"

// Builtin 返回内置斜杠命令列表（单一事实源）。
// 新增命令：在此追加一条 Command 即可，前端自动渲染，无需改动客户端。
func Builtin() []Command {
	return []Command{
		{
			Name:        "clear",
			Description: "清空当前对话上下文（仅重置本地视图，服务端按单次请求记忆）",
			Usage:       "/clear",
			Category:    CategorySystem,
			Kind:        KindClient,
		},
		{
			Name:        "model",
			Description: "切换当前对话使用的模型（留空则恢复默认模型）",
			Usage:       "/model <model_id>",
			Category:    CategorySystem,
			Kind:        KindClient,
			Args: []Arg{
				{Name: "model_id", Description: "目标模型 id（留空则用后端默认）", Required: false},
			},
		},
		{
			Name:        "workspace",
			Description: "绑定或切换当前对话的工作区（留空则取消绑定，回退默认目录）",
			Usage:       "/workspace <workspace_key>",
			Category:    CategoryWorkspace,
			Kind:        KindClient,
			Args: []Arg{
				{Name: "workspace_key", Description: "工作区 key（留空则取消绑定）", Required: false},
			},
		},
		{
			Name:        "run",
			Description: "在当前工作区执行一条 shell 命令并汇报结果",
			Usage:       "/run <command>",
			Category:    CategoryAgent,
			Kind:        KindPrompt,
			Template:    "请在当前工作区执行以下 shell 命令，并汇报执行结果与输出：\n{{args}}",
			Args: []Arg{
				{Name: "command", Description: "要执行的 shell 命令（其余整行）", Required: true},
			},
		},
		{
			Name:        "review",
			Description: "对当前工作区最近的代码改动做一次代码审阅，指出问题与风险",
			Usage:       "/review",
			Category:    CategoryAgent,
			Kind:        KindPrompt,
			Template:    "请对当前工作区最近的代码改动做一次代码审查（Review）：指出潜在问题、风险与可优化点，并给出修复建议。",
		},
		{
			Name:        "plan",
			Description: "就给定任务先制定计划，再逐项执行并更新进度",
			Usage:       "/plan <task>",
			Category:    CategoryAgent,
			Kind:        KindPrompt,
			Template:    "请就以下任务先制定执行计划（Plan），再逐项执行并更新进度：\n{{args}}",
			Args: []Arg{
				{Name: "task", Description: "任务描述（其余整行）", Required: true},
			},
		},
	}
}

// Find 按命令名（不含斜杠）查找内置命令。
func Find(name string) (Command, bool) {
	for _, c := range Builtin() {
		if c.Name == name {
			return c, true
		}
	}
	return Command{}, false
}

// RenderPrompt 把 prompt 类命令的模板渲染为最终提示词。
// args 为用户输入的命令参数（/run 与 /plan 即命令后的整行文本）。
// 若命令非 prompt 类或模板不含占位符，直接返回模板文本。
func RenderPrompt(cmd Command, args string) string {
	if cmd.Kind != KindPrompt {
		return ""
	}
	if !strings.Contains(cmd.Template, argsPlaceholder) {
		return cmd.Template
	}
	return strings.ReplaceAll(cmd.Template, argsPlaceholder, args)
}
