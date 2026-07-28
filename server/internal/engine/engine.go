// Package engine 封装 trpc-agent-go 的 Runner/LLMAgent，作为 Agent 对话引擎层。
// 设计目标：把框架 API 收敛在此包内，框架版本升级时只需改动本层（见 LEARNINGS）。
package engine

import (
	"context"
	"errors"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// defaultInstruction 是 Agent 的系统提示词（中文优先，编程助手定位）。
const defaultInstruction = "你是一个有用的编程助手，基于 trpc-agent-go 运行。" +
	"请用简洁、准确的中文回答用户的编程与技术问题；当需要使用工具时优先调用可用工具。"

// ModelConfig 描述一次对话所需的模型连接信息。
// BaseURL 应为 OpenAI 兼容端点（含 /v1，例如 http://localhost:8080/v1）。
type ModelConfig struct {
	ModelID  string // 上游模型 id（如 gpt-4o、qwen2.5）
	BaseURL  string // OpenAI 兼容 base URL（含 /v1）
	APIKey   string // 上游 API Key（本地无鉴权代理可留空）
	Protocol string // openai / anthropic / gemini（M0-10 仅实现 openai 兼容）
}

// Engine 封装 trpc-agent-go 的 Runner/LLMAgent，负责连接 Provider+Model 并产出事件流。
type Engine struct {
	cfg    ModelConfig
	runner runner.Runner
}

// New 依据 ModelConfig 构造 Agent 引擎（openai 兼容协议）。
func New(cfg ModelConfig) (*Engine, error) {
	if cfg.ModelID == "" {
		return nil, errors.New("engine: 模型 id 不能为空")
	}
	if cfg.Protocol != "" && cfg.Protocol != "openai" {
		return nil, errors.New("engine: M0-10 暂仅支持 openai 兼容协议，其他协议将在后续里程碑实现")
	}
	if cfg.BaseURL == "" {
		return nil, errors.New("engine: BaseURL 不能为空（需为 OpenAI 兼容端点，含 /v1）")
	}

	// 框架模型抽象：OpenAI 兼容客户端。上游如需鉴权，api_key 透传。
	m := openai.New(cfg.ModelID, openai.WithAPIKey(cfg.APIKey), openai.WithBaseURL(cfg.BaseURL))

	// LLM Agent：注入系统提示词、模型与基础工具集（echo/get_time）。
	ag := llmagent.New("codeagent",
		llmagent.WithModel(m),
		llmagent.WithInstruction(defaultInstruction),
		llmagent.WithTools([]tool.Tool{echoTool(), getTimeTool()}),
	)

	// Runner：未显式提供 session service 时框架会自动创建内存版会话服务。
	r := runner.NewRunner("go-multi-agent-v2", ag)
	return &Engine{cfg: cfg, runner: r}, nil
}

// Stream 发送一条用户消息并返回 Agent 事件流（channel）。
// 调用方负责消费 channel，并在结束后调用 Close 释放 Runner 资源；
// 当 ctx 被取消（如客户端断开）或 90s 超时后，底层 channel 会自动关闭。
func (e *Engine) Stream(ctx context.Context, sessionID, userMessage string) (<-chan *event.Event, error) {
	if sessionID == "" {
		sessionID = "default"
	}
	runCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	ch, err := e.runner.Run(runCtx, "user", sessionID, model.NewUserMessage(userMessage))
	if err != nil {
		cancel()
		return nil, err
	}

	// 桥接到独立输出 channel：源 channel 关闭或 ctx 取消时收尾并释放资源。
	out := make(chan *event.Event)
	go func() {
		defer close(out)
		defer cancel()
		for ev := range ch {
			select {
			case out <- ev:
			case <-runCtx.Done():
				return
			}
		}
	}()
	return out, nil
}

// Chat 发送一条用户消息并返回模型的最终文本回复（Stream 的累积版）。
// sessionID 用于多轮隔离（M0-11 起可配合 DB 持久化的 Session）。
func (e *Engine) Chat(ctx context.Context, sessionID, userMessage string) (string, error) {
	ch, err := e.Stream(ctx, sessionID, userMessage)
	if err != nil {
		return "", err
	}

	// 累加所有事件中的文本片段（同时兼容流式 Delta 与非流式 Message）。
	var sb strings.Builder
	for ev := range ch {
		if ev == nil || ev.Response == nil {
			continue
		}
		for i := range ev.Response.Choices {
			c := ev.Response.Choices[i]
			sb.WriteString(c.Delta.Content)
			sb.WriteString(c.Message.Content)
		}
	}
	return sb.String(), nil
}

// Close 释放 Runner 持有的资源（内存 session 等）。
func (e *Engine) Close() error {
	if e.runner != nil {
		return e.runner.Close()
	}
	return nil
}
