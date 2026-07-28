package engine

import (
	"context"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// echoTool 是一个回显工具，用于演示与验证 Agent 的工具调用链路。
func echoTool() tool.Tool {
	type echoInput struct {
		Message string `json:"message"`
	}
	return function.NewFunctionTool(
		func(_ context.Context, in echoInput) (string, error) {
			return in.Message, nil
		},
		function.WithName("echo"),
		function.WithDescription("回显输入的内容，用于调试或确认工具调用可用"),
	)
}

// getTimeTool 返回当前服务器时间，演示带参数的工具调用。
func getTimeTool() tool.Tool {
	type timeInput struct {
		Timezone string `json:"timezone"`
	}
	return function.NewFunctionTool(
		func(_ context.Context, in timeInput) (map[string]string, error) {
			loc := time.Local
			if in.Timezone != "" {
				if l, err := time.LoadLocation(in.Timezone); err == nil {
					loc = l
				}
			}
			now := time.Now().In(loc)
			return map[string]string{
				"utc":   time.Now().UTC().Format(time.RFC3339),
				"local": now.Format(time.RFC3339),
			}, nil
		},
		function.WithName("get_time"),
		function.WithDescription("获取当前时间（UTC 与指定时区），时区示例 Asia/Shanghai"),
	)
}
