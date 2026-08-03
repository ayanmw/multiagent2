package toolsearch

import (
	"context"
	"fmt"
	"time"

	mcptool "trpc.group/trpc-go/trpc-agent-go/tool/mcp"

	"github.com/ayanmw/multiagent2/server/internal/model"
)

// mcpConnectionTimeout 是连接单个 MCP 服务器的超时（仅 Init/ListTools/Call 阶段生效，
// 框架内部每次操作会在此基础上再叠加各自超时）。
const mcpConnectionTimeout = 30 * time.Second

// mcpConnectionConfig 把领域模型 model.MCPServer 映射为框架 MCP 连接配置。
// 覆盖 stdio（command/args）与 sse/streamable（url/headers）两类传输。
//
// 注意：trpc-agent-go v1.10.0 的 mcp.ConnectionConfig 无 env 字段，stdio 子进程的
// 环境变量来自启动进程的 os.Environ()。因此 MCP 服务器若需在子进程内读取密钥类 env，
// 应在服务端进程环境中预置（与 Provider AES 密钥同级处理），而非经此结构透传。
func mcpConnectionConfig(m model.MCPServer) mcptool.ConnectionConfig {
	return mcptool.ConnectionConfig{
		Transport:   string(m.Transport),
		ServerURL:   m.URL,
		Headers:     m.Headers,
		Command:     m.Command,
		Args:        m.Args,
		Timeout:     mcpConnectionTimeout,
		Description: m.Description,
	}
}

// LoadMCPServerTools 连接一个 MCP 服务器并预取其工具列表，已按 mcp__<name>__<tool>
// 命名空间注册进返回的 toolbox。返回的 toolbox 内部持有一个仍存活的 MCP 会话连接，
// 调用方应在本轮对话结束后调用 toolbox.Close() 释放连接（见 engine.New / Engine.Close）。
//
// 任何连接/初始化错误都会就地返回，不写入工具箱——上层据此 fail-open（跳过该服务器）。
func LoadMCPServerTools(ctx context.Context, m model.MCPServer) (*Toolbox, error) {
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("toolsearch: MCP 配置校验失败: %w", err)
	}
	ts := mcptool.NewMCPToolSet(mcpConnectionConfig(m), mcptool.WithName(m.Name))
	if err := ts.Init(ctx); err != nil {
		return nil, fmt.Errorf("toolsearch: 初始化 MCP 工具集 %q 失败: %w", m.Name, err)
	}
	tools := ts.Tools(ctx)
	box := NewToolbox()
	box.Add(MCPNamespace(m.Name), tools)
	box.AddCloser(ts)
	return box, nil
}
