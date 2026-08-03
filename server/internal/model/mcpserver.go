package model

import (
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// MCPTransport 枚举一个 MCP 服务器配置支持的传输方式（M2-02）。
//   - stdio:      本地子进程，需 command（可选 args / env）
//   - sse:        远程 Server-Sent-Events 端点，需 url（可选 headers）
//   - streamable: 远程 Streamable HTTP 端点，需 url（可选 headers）
type MCPTransport string

const (
	MCPTransportStdio      MCPTransport = "stdio"
	MCPTransportSSE        MCPTransport = "sse"
	MCPTransportStreamable MCPTransport = "streamable"
)

// ValidMCPTransports 列出所有合法 transport 值（供校验与前端提示）。
var ValidMCPTransports = []MCPTransport{
	MCPTransportStdio, MCPTransportSSE, MCPTransportStreamable,
}

// ParseMCPTransport 校验并归一化 transport 字符串（大小写/空白容错）。
func ParseMCPTransport(s string) (MCPTransport, bool) {
	t := MCPTransport(strings.TrimSpace(strings.ToLower(s)))
	switch t {
	case MCPTransportStdio, MCPTransportSSE, MCPTransportStreamable:
		return t, true
	}
	return "", false
}

// MCPServer 表示一个用户归属的 MCP 服务器配置（M2-02 管理面）。
//
// 本任务只做「配置持久化 + 校验 + 管理 API」，不在此装载工具；真实装载
// 由 M2-06 toolsearch 按需调用框架 tool/mcp 完成（届时读取本表配置）。
//
// 配置字段按 transport 分两组：
//   - stdio:          Command / Args / Env
//   - sse|streamable: URL / Headers
//
// Args / Env / Headers 经 GORM `serializer:json` 与 DB 互转 JSON；对外 JSON
// （model 的 json tag）直接是数组/对象，便于前端渲染与 M2-06 消费。
type MCPServer struct {
	gorm.Model
	UserID      uint              `gorm:"not null;index" json:"user_id"`
	Name        string            `gorm:"size:128;not null;uniqueIndex:idx_user_mcp,priority:1" json:"name"`
	Transport   MCPTransport      `gorm:"size:32;not null" json:"transport"`
	Command     string            `gorm:"size:256" json:"command"`
	Args        []string          `gorm:"serializer:json" json:"args"`
	Env         map[string]string `gorm:"serializer:json" json:"env"`
	URL         string            `gorm:"size:512" json:"url"`
	Headers     map[string]string `gorm:"serializer:json" json:"headers"`
	Enabled     bool              `gorm:"not null;default:true" json:"enabled"`
	Description string            `gorm:"size:512" json:"description"`
}

// TableName overrides the default GORM table name.
func (MCPServer) TableName() string { return "mcp_servers" }

// Validate 校验配置自洽性：名称必填；transport 必为合法值；
// 不同 transport 对必填字段有不同要求（stdio→command，sse/streamable→url）。
func (m *MCPServer) Validate() error {
	if strings.TrimSpace(m.Name) == "" {
		return errors.New("name is required")
	}
	if _, ok := ParseMCPTransport(string(m.Transport)); !ok {
		return fmt.Errorf("invalid transport %q (must be one of stdio/sse/streamable)", m.Transport)
	}
	switch m.Transport {
	case MCPTransportStdio:
		if strings.TrimSpace(m.Command) == "" {
			return errors.New("command is required for stdio transport")
		}
	case MCPTransportSSE, MCPTransportStreamable:
		if strings.TrimSpace(m.URL) == "" {
			return fmt.Errorf("url is required for %s transport", m.Transport)
		}
	}
	return nil
}
