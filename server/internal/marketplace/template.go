// Package marketplace 提供「连接器市场」：预置一批常用 MCP 服务器模板
// （GitHub/GitLab/Slack/Jira/Postgres/Redis/Filesystem/Fetch），用户可一键
// 导入为自己的 MCP 配置（M8-08）。
//
// 设计要点：
//   - 模板是纯数据 + 纯函数，不依赖框架、不落库、不发起网络请求；
//   - 敏感/个性化字段（token、连接串、路径）用 `{{KEY}}` 占位符表示，
//     导入时由用户提供实际值，经 Render 替换后生成 model.MCPServer；
//   - 未替换的占位符保留原样（用户可导入后再编辑补齐），但必填字段
//     （stdio→command / sse|streamable→url）不会缺失，Validate 恒可通过。
package marketplace

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/ayanmw/multiagent2/server/internal/model"
)

// Template 描述一个预置 MCP 服务器模板（连接器市场条目）。
type Template struct {
	// ID 是稳定标识（URL 路径参数），如 "github"。
	ID string
	// Name 是展示名，如 "GitHub"。
	Name string
	// Category 分类（代码托管 / 团队协作 / 数据与存储 / 通用工具）。
	Category string
	// Description 说明该连接器能做什么、需要什么。
	Description string
	// Transport 传输方式（stdio / sse / streamable），复用 model.MCPTransport。
	Transport model.MCPTransport
	// Command 仅 stdio 使用（如 npx）。
	Command string
	// Args 仅 stdio 使用；支持 `{{KEY}}` 占位符（如 Postgres 连接串）。
	Args []string
	// URL 仅 sse/streamable 使用；支持 `{{KEY}}` 占位符。
	URL string
	// Env 是 stdio 进程环境变量；值支持 `{{KEY}}` 占位符，非占位符值为合理默认。
	Env map[string]string
	// Headers 是远程请求头；值支持 `{{KEY}}` 占位符。
	Headers map[string]string
	// SecretFields 列出需要用户提供的密钥/参数名（导入表单据此渲染输入框），
	// 与占位符键名一一对应（如 "GITHUB_TOKEN"）。
	SecretFields []string
	// DefaultName 是导入后的默认配置名（可被导入请求覆盖）。
	DefaultName string
	// DefaultEnabled 导入后是否默认启用。
	DefaultEnabled bool
}

// Templates 是内置连接器市场模板库（按 ID 升序，便于稳定输出与测试）。
var Templates = []Template{
	{
		ID:             "github",
		Name:           "GitHub",
		Category:       "代码托管",
		Description:    "GitHub 官方远程 MCP 端点：仓库/Issue/PR/Code Search 等能力。需要 GitHub Personal Access Token（仅需 repo 读权限即可）。",
		Transport:      model.MCPTransportStreamable,
		URL:            "https://api.githubcopilot.com/mcp/",
		Headers:        map[string]string{"Authorization": "Bearer {{GITHUB_TOKEN}}"},
		SecretFields:   []string{"GITHUB_TOKEN"},
		DefaultName:    "github",
		DefaultEnabled: true,
	},
	{
		ID:             "gitlab",
		Name:           "GitLab",
		Category:       "代码托管",
		Description:    "GitLab MCP：项目/Issue/MR/流水线。需要 GitLab Personal Access Token（默认 gitlab.com，自建实例可改 GITLAB_URL）。",
		Transport:      model.MCPTransportStdio,
		Command:        "npx",
		Args:           []string{"-y", "@modelcontextprotocol/server-gitlab"},
		Env:            map[string]string{"GITLAB_PERSONAL_ACCESS_TOKEN": "{{GITLAB_TOKEN}}", "GITLAB_API_URL": "https://gitlab.com/api/v4"},
		SecretFields:   []string{"GITLAB_TOKEN"},
		DefaultName:    "gitlab",
		DefaultEnabled: true,
	},
	{
		ID:             "slack",
		Name:           "Slack",
		Category:       "团队协作",
		Description:    "Slack 官方远程 MCP 端点：频道消息/线程/搜索。需要 Slack MCP 集成产生的 Bot Token（mcp.slack.com 渠道）。",
		Transport:      model.MCPTransportStreamable,
		URL:            "https://mcp.slack.com/mcp",
		Headers:        map[string]string{"Authorization": "Bearer {{SLACK_TOKEN}}"},
		SecretFields:   []string{"SLACK_TOKEN"},
		DefaultName:    "slack",
		DefaultEnabled: true,
	},
	{
		ID:             "atlassian",
		Name:           "Jira / Confluence",
		Category:       "团队协作",
		Description:    "Atlassian MCP：Jira Issue/看板 + Confluence 页面。需要 Jira 站点地址、账号邮箱与 API Token。",
		Transport:      model.MCPTransportStdio,
		Command:        "npx",
		Args:           []string{"-y", "@modelcontextprotocol/server-atlassian"},
		Env:            map[string]string{"JIRA_URL": "{{JIRA_URL}}", "JIRA_EMAIL": "{{JIRA_EMAIL}}", "JIRA_API_TOKEN": "{{JIRA_API_TOKEN}}"},
		SecretFields:   []string{"JIRA_URL", "JIRA_EMAIL", "JIRA_API_TOKEN"},
		DefaultName:    "atlassian",
		DefaultEnabled: true,
	},
	{
		ID:             "postgres",
		Name:           "PostgreSQL",
		Category:       "数据与存储",
		Description:    "PostgreSQL MCP：连接串读写、只读查询分析 schema。连接串含密码属敏感信息，导入后加密落库。",
		Transport:      model.MCPTransportStdio,
		Command:        "npx",
		Args:           []string{"-y", "@modelcontextprotocol/server-postgres", "{{POSTGRES_CONNECTION_STRING}}"},
		SecretFields:   []string{"POSTGRES_CONNECTION_STRING"},
		DefaultName:    "postgres",
		DefaultEnabled: false,
	},
	{
		ID:             "redis",
		Name:           "Redis",
		Category:       "数据与存储",
		Description:    "Redis MCP：键值读写/过期/扫描等。需要 redis:// 连接串（含密码时属敏感信息）。",
		Transport:      model.MCPTransportStdio,
		Command:        "npx",
		Args:           []string{"-y", "@modelcontextprotocol/server-redis", "{{REDIS_URL}}"},
		SecretFields:   []string{"REDIS_URL"},
		DefaultName:    "redis",
		DefaultEnabled: false,
	},
	{
		ID:             "filesystem",
		Name:           "文件系统",
		Category:       "通用工具",
		Description:    "Filesystem MCP：目录浏览/文件读写/搜索。请把 WORKSPACE_DIR 指向允许 Agent 操作的目录（如 data/workspaces 下某工作区）。",
		Transport:      model.MCPTransportStdio,
		Command:        "npx",
		Args:           []string{"-y", "@modelcontextprotocol/server-filesystem", "{{WORKSPACE_DIR}}"},
		SecretFields:   []string{"WORKSPACE_DIR"},
		DefaultName:    "filesystem",
		DefaultEnabled: true,
	},
	{
		ID:             "fetch",
		Name:           "网页抓取",
		Category:       "通用工具",
		Description:    "Fetch MCP：抓取网页转 Markdown、读 PDF。无需任何密钥，导入即用。",
		Transport:      model.MCPTransportStdio,
		Command:        "npx",
		Args:           []string{"-y", "@modelcontextprotocol/server-fetch"},
		DefaultName:    "fetch",
		DefaultEnabled: true,
	},
}

// GetTemplate 按 ID 返回模板副本；未找到返回 ok=false。
func GetTemplate(id string) (Template, bool) {
	for _, t := range Templates {
		if t.ID == id {
			return t, true
		}
	}
	return Template{}, false
}

// RenderOptions 是导入时的用户输入：占位符实际值 + 自定义覆盖。
type RenderOptions struct {
	// Env 提供占位符实际值并合并进 stdio 环境变量（键为 SecretFields 名或自定义键）。
	Env map[string]string
	// Headers 提供占位符实际值并合并进远程请求头。
	Headers map[string]string
}

// Render 把模板渲染成可直接落库的 MCPServer：
//   - Args / URL / Env 值 / Headers 值中的 `{{KEY}}` 占位符用 opts 里同名键替换；
//   - opts.Env / opts.Headers 中未匹配任何占位符的键按原样合并进最终映射
//     （允许用户添加模板之外的 env/header）；
//   - 未提供的占位符保留 `{{KEY}}` 原样（用户可导入后编辑补齐，Validate 不受影响）。
func (t Template) Render(opts RenderOptions) *model.MCPServer {
	// 占位符查找表 = env ∪ headers 合并（headers 优先）。这样 SecretFields 的值
	// 无论用户放在 env 还是 headers 提交，都能替换任意位置的 `{{KEY}}` 占位符
	// （如 GitHub 模板：占位符在 headers，但导入表单统一收进 env 也能替换）。
	lookup := make(map[string]string, len(opts.Env)+len(opts.Headers))
	for k, v := range opts.Env {
		lookup[k] = v
	}
	for k, v := range opts.Headers {
		lookup[k] = v
	}
	// 模板里真实出现过的占位符键集合：匹配占位符的用户键只作查找源，
	// 不冗余落库（GitHub 导入后 env 应保持为空，token 只出现在 headers）。
	used := t.placeholderKeys()
	env := make(map[string]string, len(t.Env)+len(opts.Env))
	for k, v := range t.Env {
		env[k] = renderPlaceholders(v, lookup)
	}
	for k, v := range opts.Env {
		if used[k] {
			continue
		}
		env[k] = v
	}
	headers := make(map[string]string, len(t.Headers)+len(opts.Headers))
	for k, v := range t.Headers {
		headers[k] = renderPlaceholders(v, lookup)
	}
	for k, v := range opts.Headers {
		if used[k] {
			continue
		}
		headers[k] = v
	}
	args := make([]string, 0, len(t.Args))
	for _, a := range t.Args {
		args = append(args, renderPlaceholders(a, lookup))
	}
	return &model.MCPServer{
		Name:        t.DefaultName,
		Transport:   t.Transport,
		Command:     t.Command,
		Args:        args,
		URL:         renderPlaceholders(t.URL, lookup),
		Env:         env,
		Headers:     headers,
		Enabled:     t.DefaultEnabled,
		Description: t.Description,
	}
}

// placeholderKeys 收集模板所有字段中出现的 `{{KEY}}` 占位符键。
func (t Template) placeholderKeys() map[string]bool {
	keys := map[string]bool{}
	scan := func(s string) {
		for _, m := range placeholderRe.FindAllStringSubmatch(s, -1) {
			keys[m[1]] = true
		}
	}
	scan(t.URL)
	for _, a := range t.Args {
		scan(a)
	}
	for _, v := range t.Env {
		scan(v)
	}
	for _, v := range t.Headers {
		scan(v)
	}
	return keys
}

// placeholderRe 匹配 `{{KEY}}` 占位符。
var placeholderRe = regexp.MustCompile(`\{\{([A-Za-z0-9_]+)\}\}`)

// renderPlaceholders 把 s 中出现的所有 `{{KEY}}` 用 values[KEY] 替换；
// values 缺失对应键时保留占位符原样。
func renderPlaceholders(s string, values map[string]string) string {
	if s == "" || len(values) == 0 {
		return s
	}
	return placeholderRe.ReplaceAllStringFunc(s, func(m string) string {
		key := strings.TrimSuffix(strings.TrimPrefix(m, "{{"), "}}")
		if v, ok := values[key]; ok {
			return v
		}
		return m
	})
}

// ValidateTemplates 校验内置模板库自洽性（测试与启动自检共用）：
// 每个模板必填字段齐全、DefaultName 非空、占位符与 SecretFields 一致。
func ValidateTemplates() error {
	for _, t := range Templates {
		if t.ID == "" || t.Name == "" || t.Category == "" || t.Description == "" || t.DefaultName == "" {
			return fmt.Errorf("template %q: missing required metadata", t.ID)
		}
		if _, ok := model.ParseMCPTransport(string(t.Transport)); !ok {
			return fmt.Errorf("template %q: invalid transport %q", t.ID, t.Transport)
		}
		switch t.Transport {
		case model.MCPTransportStdio:
			if strings.TrimSpace(t.Command) == "" {
				return fmt.Errorf("template %q: command required for stdio", t.ID)
			}
		case model.MCPTransportSSE, model.MCPTransportStreamable:
			if strings.TrimSpace(t.URL) == "" {
				return fmt.Errorf("template %q: url required for %s", t.ID, t.Transport)
			}
		}
		// 每个 SecretFields 名必须在模板某个字段的占位符中出现过（防手滑写错键名）。
		fieldText := strings.Join(t.Args, "\n") + "\n" + t.URL + "\n" +
			strings.Join(mapValues(t.Env), "\n") + "\n" + strings.Join(mapValues(t.Headers), "\n")
		for _, sf := range t.SecretFields {
			if !strings.Contains(fieldText, "{{"+sf+"}}") {
				return fmt.Errorf("template %q: secret field %q not found in any placeholder", t.ID, sf)
			}
		}
	}
	return nil
}

func mapValues(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
