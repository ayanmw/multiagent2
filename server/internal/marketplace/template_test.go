package marketplace

import (
	"strings"
	"testing"

	"github.com/ayanmw/multiagent2/server/internal/model"
)

// TestTemplates_Validate 验收：内置模板库自洽（元数据齐全 / 必填字段 / SecretFields 与占位符一致）。
func TestTemplates_Validate(t *testing.T) {
	if err := ValidateTemplates(); err != nil {
		t.Fatalf("内置模板库不自洽: %v", err)
	}
	if len(Templates) < 6 {
		t.Fatalf("模板数应 ≥6（连接器市场基本规模），实际 %d", len(Templates))
	}
}

// TestTemplates_GetTemplate 验收：按 ID 查找与未命中。
func TestTemplates_GetTemplate(t *testing.T) {
	tmpl, ok := GetTemplate("github")
	if !ok || tmpl.Name != "GitHub" {
		t.Fatalf("GetTemplate(github) 应命中, got %+v ok=%v", tmpl, ok)
	}
	if _, ok := GetTemplate("not-exist"); ok {
		t.Fatal("GetTemplate(not-exist) 应未命中")
	}
}

// TestTemplates_UniqueIDs 验收：模板 ID 全局唯一（路由参数无歧义）。
func TestTemplates_UniqueIDs(t *testing.T) {
	seen := map[string]bool{}
	for _, tmpl := range Templates {
		if seen[tmpl.ID] {
			t.Fatalf("模板 ID 重复: %s", tmpl.ID)
		}
		seen[tmpl.ID] = true
	}
}

// TestRender_Github 验收：streamable 模板占位符替换进 headers，未提供则保留占位符。
func TestRender_Github(t *testing.T) {
	tmpl, _ := GetTemplate("github")
	m := tmpl.Render(RenderOptions{Headers: map[string]string{"GITHUB_TOKEN": "ghp_secret"}})
	if m.Transport != model.MCPTransportStreamable {
		t.Fatalf("transport 应为 streamable, got %v", m.Transport)
	}
	if m.URL != "https://api.githubcopilot.com/mcp/" {
		t.Fatalf("url 不符: %s", m.URL)
	}
	if got := m.Headers["Authorization"]; got != "Bearer ghp_secret" {
		t.Fatalf("Authorization 应替换为 Bearer ghp_secret, got %q", got)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("渲染后 Validate 应通过: %v", err)
	}
}

// TestRender_LookupUnion 验收：占位符取值源 = env ∪ headers 合并——token 放 env
// 也能替换 headers 里的占位符（导入表单统一收 env 的常见姿势）。
func TestRender_LookupUnion(t *testing.T) {
	tmpl, _ := GetTemplate("github")
	m := tmpl.Render(RenderOptions{Env: map[string]string{"GITHUB_TOKEN": "ghp_via_env"}})
	if got := m.Headers["Authorization"]; got != "Bearer ghp_via_env" {
		t.Fatalf("env 值应替换 headers 占位符, got %q", got)
	}
	// headers 键仍按模板保留，env 不额外混入。
	if _, ok := m.Env["GITHUB_TOKEN"]; ok {
		t.Fatal("占位符值不应作为 env 键混入（仅做查找源）")
	}
}

// TestRender_PlaceholderKept 验收：未提供的占位符保留原样，便于后续编辑补齐。
func TestRender_PlaceholderKept(t *testing.T) {
	tmpl, _ := GetTemplate("postgres")
	m := tmpl.Render(RenderOptions{})
	if len(m.Args) != 3 {
		t.Fatalf("args 应 3 项（npx -y <pkg> <conn>）, got %v", m.Args)
	}
	if m.Args[2] != "{{POSTGRES_CONNECTION_STRING}}" {
		t.Fatalf("未提供的占位符应保留, got %q", m.Args[2])
	}
	if m.Enabled {
		t.Fatal("postgres 默认不应启用（含敏感连接串）")
	}
}

// TestRender_ArgsAndCustomMerge 验收：stdio 模板 args 占位符替换 + 自定义 env 合并。
func TestRender_ArgsAndCustomMerge(t *testing.T) {
	tmpl, _ := GetTemplate("gitlab")
	m := tmpl.Render(RenderOptions{
		Env: map[string]string{
			"GITLAB_TOKEN": "glpat_x",
			"EXTRA":        "y",
		},
	})
	// 模板默认 GITLAB_API_URL 保留。
	if m.Env["GITLAB_API_URL"] != "https://gitlab.com/api/v4" {
		t.Fatalf("模板默认 env 应保留, got %q", m.Env["GITLAB_API_URL"])
	}
	// 占位符替换。
	if m.Env["GITLAB_PERSONAL_ACCESS_TOKEN"] != "glpat_x" {
		t.Fatalf("占位符应替换为 glpat_x, got %q", m.Env["GITLAB_PERSONAL_ACCESS_TOKEN"])
	}
	// 自定义键合并。
	if m.Env["EXTRA"] != "y" {
		t.Fatalf("自定义 env 应合并, got %q", m.Env["EXTRA"])
	}
	// 渲染结果必填字段齐全 → Validate 通过。
	if err := m.Validate(); err != nil {
		t.Fatalf("gitlab 渲染后 Validate 应通过: %v", err)
	}
}

// TestRender_FetchNoSecret 验收：无密钥模板导入即用，env/headers 为空。
func TestRender_FetchNoSecret(t *testing.T) {
	tmpl, _ := GetTemplate("fetch")
	m := tmpl.Render(RenderOptions{})
	if len(m.Env) != 0 || len(m.Headers) != 0 {
		t.Fatalf("fetch 模板不应有 env/headers, got env=%v headers=%v", m.Env, m.Headers)
	}
	if !m.Enabled {
		t.Fatal("fetch 默认应启用")
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("fetch 渲染后 Validate 应通过: %v", err)
	}
}

// TestRender_PlaceholderRegexp 验收：占位符语法容错（大小写、下划线、无空白）。
func TestRender_PlaceholderRegexp(t *testing.T) {
	cases := map[string]string{
		"{{KEY}}":        "v1",
		"{{A_B_C}}":      "v2",
		"Bearer {{T}}":   "Bearer v3",
		"x={{K}}&y={{K}}": "x=v4&y=v4",
		"no placeholder": "no placeholder",
	}
	for in, want := range cases {
		if got := renderPlaceholders(in, map[string]string{"KEY": "v1", "A_B_C": "v2", "T": "v3", "K": "v4"}); got != want {
			t.Errorf("renderPlaceholders(%q) = %q, want %q", in, got, want)
		}
	}
	// 缺失键 → 原样保留。
	if got := renderPlaceholders("{{MISSING}}", map[string]string{}); got != "{{MISSING}}" {
		t.Errorf("缺失键应保留占位符, got %q", got)
	}
}

// TestRender_EmptyOptions 验收：nil 选项等价于空选项，不 panic。
func TestRender_EmptyOptions(t *testing.T) {
	for _, tmpl := range Templates {
		m := tmpl.Render(RenderOptions{})
		if err := m.Validate(); err != nil {
			t.Errorf("template %s 空选项渲染后 Validate 失败: %v", tmpl.ID, err)
		}
		if strings.TrimSpace(m.Name) == "" {
			t.Errorf("template %s DefaultName 为空", tmpl.ID)
		}
	}
}
