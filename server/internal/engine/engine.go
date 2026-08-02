// Package engine 封装 trpc-agent-go 的 Runner/LLMAgent，作为 Agent 对话引擎层。
// 设计目标：把框架 API 收敛在此包内，框架版本升级时只需改动本层（见 LEARNINGS）。
package engine

import (
	"context"
	"errors"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	codeagent "github.com/ayanmw/multiagent2/server/internal/agent"
)

// defaultInstruction 是 Agent 的系统提示词（中文优先，编程助手定位）。
const defaultInstruction = "你是一个有用的编程助手，基于 trpc-agent-go 运行。" +
	"请用简洁、准确的中文回答用户的编程与技术问题；当需要使用工具时优先调用可用工具。"

// ModelConfig 描述一次对话所需的模型连接信息。
// BaseURL 应为 OpenAI 兼容端点（含 /v1，例如 http://localhost:8080/v1）。
type ModelConfig struct {
	ModelID  string        // 上游模型 id（如 gpt-4o、qwen2.5）
	BaseURL  string        // OpenAI 兼容 base URL（含 /v1）
	APIKey   string        // 上游 API Key（本地无鉴权代理可留空）
	Protocol string        // openai / anthropic / gemini（M0-10 仅实现 openai 兼容）
	Timeout  time.Duration // 单次对话（流式）超时；<=0 时取默认 90s（由配置注入，M0.5-05）
	// Tools 是可选的额外工具集（如 M1-06 CodeAct 工具），会被追加到基础工具之后。
	// 不传则仅使用 echo/get_time 基础工具。
	Tools []tool.Tool
	// Team 是多代理编排（CodeTeam）配置（M1-08/M1-09）：
	//   - EnableSubAgents=true 时根 Agent 换成 Orchestrator，代码落地能力收敛到
	//     Coder 子代理，由框架 agenttool 委托调用；此时 Workdir 必填，Tools 仅作为
	//     Orchestrator 的附加工具（CodeAct 工具集由 codeagent 工厂装配给 Coder）；
	//   - EnableReviewer=true 时额外加入只读 Reviewer 子代理，形成
	//     「实现→审阅→修复」回环，轮数上限由 MaxReviewRounds 控制。
	// 零值 = 单代理模式（与 M1-06/07 行为一致）。
	Team TeamConfig
	// Workdir 是子代理代码工具集的受限工作目录（Team.EnableSubAgents=true 时必填）。
	Workdir string
}

// TeamConfig 是 CodeTeam 编排配置（M1-09），定义在 internal/agent 包内，
// 此处以类型别名再导出，使 api/cmd 层无需直连 codeagent 包即可配置团队。
type TeamConfig = codeagent.TeamConfig

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

	// 基础工具集（echo/get_time）+ 可选额外工具（如 M1-06 CodeAct）。
	allTools := []tool.Tool{echoTool(), getTimeTool()}
	if len(cfg.Tools) > 0 {
		allTools = append(allTools, cfg.Tools...)
	}

	// 根 Agent：
	//   - 默认（单代理）：codeagent 直接持有全部工具，行为与 M1-07 一致；
	//   - Team.EnableSubAgents（M1-08）：换成 Orchestrator，代码落地委托给 Coder 子代理；
	//   - 再叠加 Team.EnableReviewer（M1-09）：加入只读 Reviewer，形成审阅回环。
	var root agent.Agent
	if cfg.Team.EnableSubAgents {
		orchestrator, oerr := codeagent.NewTeam(codeagent.Deps{
			Model:      m,
			Workdir:    cfg.Workdir,
			ExtraTools: allTools,
		}, cfg.Team)
		if oerr != nil {
			return nil, oerr
		}
		root = orchestrator
	} else {
		root = llmagent.New("codeagent",
			llmagent.WithModel(m),
			llmagent.WithInstruction(defaultInstruction),
			llmagent.WithTools(allTools),
		)
	}

	// Runner：未显式提供 session service 时框架会自动创建内存版会话服务。
	r := runner.NewRunner("go-multi-agent-v2", root)

	// 单次对话超时：<=0 时回退默认 90s（由配置 ENGINE_TIMEOUT_SECONDS 注入，M0.5-05）。
	if cfg.Timeout <= 0 {
		cfg.Timeout = 90 * time.Second
	}
	return &Engine{cfg: cfg, runner: r}, nil
}

// Stream 发送一条用户消息并返回 Agent 事件流（channel）。
// 调用方负责消费 channel，并在结束后调用 Close 释放 Runner 资源；
// 当 ctx 被取消（如客户端断开）或 90s 超时后，底层 channel 会自动关闭。
//
// history 为可选的对话历史（按时间正序），用于多轮记忆回灌：框架无 SQLite
// 会话后端（v1.10.0 仅 inmemory/noop），这里把 DB 加载的历史作为初始消息
// seed 进本次 Run 的会话，使模型能看到前文（见 M0.5-01）。为空则单轮。
func (e *Engine) Stream(ctx context.Context, sessionID, userMessage string, history []model.Message) (<-chan *event.Event, error) {
	if sessionID == "" {
		sessionID = "default"
	}
	runCtx, cancel := context.WithTimeout(ctx, e.cfg.Timeout)
	// 显式开启流式：llmagent 默认是非流式（返回整块 Message.Content），
	// 不开启则上游被当作非流式 JSON 请求，无法产生 token 级增量。
	// M0 出口标准要求真正的流式对话，故始终以流式模式运行。
	runOpts := []agent.RunOption{agent.WithStream(true)}
	if len(history) > 0 {
		// 把 DB 历史作为初始消息 seed 进本次会话（fresh inmemory service），
		// runner 会先落库历史事件再追加本轮 user 消息，模型即拥有多轮上下文。
		runOpts = append(runOpts, agent.WithMessages(history))
	}
	ch, err := e.runner.Run(runCtx, "user", sessionID, model.NewUserMessage(userMessage), runOpts...)
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
// sessionID 用于多轮隔离（M0-11 起可配合 DB 持久化的 Session）；
// history 为可选的对话历史，用于多轮记忆回灌（见 M0.5-01）。
func (e *Engine) Chat(ctx context.Context, sessionID, userMessage string, history []model.Message) (string, error) {
	ch, err := e.Stream(ctx, sessionID, userMessage, history)
	if err != nil {
		return "", err
	}

	// 累加所有事件中的文本片段。
	// 通过 DeltaState 实现「优先 Delta 增量，未出现增量才回退 Message」的去重规则，
	// 与 AG-UI converter 共用同一逻辑（见 internal/engine/delta.go / M0.5-04）。
	var sb strings.Builder
	ds := NewDeltaState()
	for ev := range ch {
		if ev == nil || ev.Response == nil {
			continue
		}
		for i := range ev.Response.Choices {
			c := ev.Response.Choices[i]
			if t := ds.Text(c.Delta.Content, c.Message.Content); t != "" {
				sb.WriteString(t)
			}
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
