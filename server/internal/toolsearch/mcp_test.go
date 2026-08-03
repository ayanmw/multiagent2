package toolsearch

import (
	"context"
	"testing"

	mcptool "trpc.group/trpc-go/trpc-agent-go/tool/mcp"

	"github.com/ayanmw/multiagent2/server/internal/model"
)

// TestMCPConnectionConfig 验证领域模型 → 框架连接配置的字段映射（不涉及真实连接）。
func TestMCPConnectionConfig(t *testing.T) {
	m := model.MCPServer{
		Name:      "demo",
		Transport: model.MCPTransportStdio,
		Command:   "npx",
		Args:      []string{"-y", "my-mcp"},
	}
	cfg := mcpConnectionConfig(m)
	if cfg.Transport != "stdio" {
		t.Fatalf("Transport 映射错误：%q", cfg.Transport)
	}
	if cfg.Command != "npx" || len(cfg.Args) != 2 {
		t.Fatalf("stdio 的 command/args 映射错误：%#v", cfg)
	}

	sse := model.MCPServer{
		Name:      "remote",
		Transport: model.MCPTransportSSE,
		URL:       "http://localhost:9000/sse",
		Headers:   map[string]string{"Authorization": "Bearer x"},
	}
	cfg = mcpConnectionConfig(sse)
	if cfg.Transport != "sse" || cfg.ServerURL != "http://localhost:9000/sse" {
		t.Fatalf("sse 的 transport/url 映射错误：%#v", cfg)
	}
	if cfg.Headers["Authorization"] != "Bearer x" {
		t.Fatalf("sse 的 headers 映射错误：%#v", cfg)
	}
}

// TestLoadMCPServerTools_ValidateFail 验证非法配置（stdio 缺 command）就地报错、
// 不发起真实连接（避免单测挂起）。
func TestLoadMCPServerTools_ValidateFail(t *testing.T) {
	m := model.MCPServer{
		Name:      "bad",
		Transport: model.MCPTransportStdio,
		// 缺 Command → Validate 失败
	}
	box, err := LoadMCPServerTools(context.Background(), m)
	if err == nil {
		t.Fatalf("非法 MCP 配置应返回错误")
	}
	if box != nil {
		t.Fatalf("失败时不应返回 toolbox")
	}
}

// TestMCPToolSet_API 验证框架 NewMCPToolSet 可被构造（编译期接口契约），
// 不发起连接（仅确认 WithName/Init 等方法存在且签名一致）。
func TestMCPToolSet_API(t *testing.T) {
	ts := mcptool.NewMCPToolSet(mcptool.ConnectionConfig{
		Transport: "stdio",
		Command:   "echo",
	}, mcptool.WithName("probe"))
	if ts == nil {
		t.Fatalf("NewMCPToolSet 不应返回 nil")
	}
	if ts.Name() != "probe" {
		t.Fatalf("WithName 未生效：%q", ts.Name())
	}
}
