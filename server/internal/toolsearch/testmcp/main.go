// Command testmcp 是一个最小可用的 stdio MCP 服务器，仅供 toolsearch 包的集成测试使用
// （M2-06 LoadMCPServerTools 真实连接路径验证）。它注册一个 echo 工具，把调用方传入的
// message 原样回显，用于证明「连接 → 列举工具 → 按需调用」全链路可用。
//
// 复用框架同款 trpc-mcp-go 库，确保与 tool/mcp 客户端的协议握手完全兼容。
package main

import (
	"context"
	"fmt"
	"log"

	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

func main() {
	server := mcp.NewStdioServer("testmcp", "1.0.0")

	server.RegisterTool(
		mcp.NewTool("demo_echo",
			mcp.WithDescription("回显调用方传入的 message，用于集成测试"),
		),
		func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			msg := ""
			if req != nil {
				if v, ok := req.Params.Arguments["message"]; ok {
					if s, ok := v.(string); ok {
						msg = s
					}
				}
			}
			return mcp.NewTextResult(fmt.Sprintf("echo: %s", msg)), nil
		},
	)

	log.Printf("starting testmcp stdio server")
	if err := server.Start(); err != nil {
		log.Fatalf("testmcp server failed: %v", err)
	}
}
