// Package tui 提供基于 bubbletea 的交互式对话界面（gmctl chat --repl）。
// 标准输入/输出为 TTY 时可用；流式回复经后台 goroutine 推入 channel，
// 由 bubbletea 的 Cmd 循环逐段渲染，与前端 SSE 消费等价。
package tui

import (
	"context"
	"os"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	"github.com/ayanmw/multiagent2/tool/cli/internal/api"
)

// 颜色转义（仅在 TTY 下有意义）。
const (
	colReset  = "\x1b[0m"
	colCyan   = "\x1b[36m"
	colGreen  = "\x1b[32m"
	colYellow = "\x1b[33m"
	colRed    = "\x1b[31m"
	colBold   = "\x1b[1m"
)

type streamMsg struct{ text string }
type streamDoneMsg struct{}

// chatModel 是 bubbletea 的 Model。
type chatModel struct {
	client     *api.Client
	sessionKey string
	modelID    uint
	workspace  string

	output    string // 已完成的对话 + 历史流式文本
	input     string // 当前正在输入的行
	streaming bool
	ch        chan string
	quit      bool
}

// NewChatModel 构造交互模型。
func NewChatModel(c *api.Client, sessionKey string, modelID uint, workspace string) chatModel {
	return chatModel{
		client:     c,
		sessionKey: sessionKey,
		modelID:    modelID,
		workspace:  workspace,
		output:     colBold + "GM-Agent 交互对话" + colReset + "  (session: " + sessionKey + ")\n输入消息后回车发送；Ctrl+C / Esc 退出。\n\n",
	}
}

// NewProgram 创建并配置 bubbletea 程序（绑定标准输入输出）。
func NewProgram(m chatModel) *bubbletea.Program {
	return bubbletea.NewProgram(m, bubbletea.WithInput(os.Stdin), bubbletea.WithOutput(os.Stdout))
}

// Init 无初始命令。
func (m chatModel) Init() bubbletea.Cmd { return nil }

// Update 处理按键与流式消息。
func (m chatModel) Update(msg bubbletea.Msg) (bubbletea.Model, bubbletea.Cmd) {
	switch msg := msg.(type) {
	case bubbletea.KeyMsg:
		// 退出优先。
		switch msg.String() {
		case "ctrl+c", "esc":
			m.quit = true
			return m, bubbletea.Quit
		}
		// 流式进行中忽略其它输入。
		if m.streaming {
			return m, nil
		}
		switch msg.String() {
		case "enter":
			text := strings.TrimSpace(m.input)
			if text == "" {
				return m, nil
			}
			m.input = ""
			m.output += colCyan + "> " + colReset + text + "\n"
			ch := make(chan string, 64)
			m.ch = ch
			m.streaming = true
			go func() {
				defer close(ch)
				_ = m.client.StreamChat(context.Background(), m.sessionKey, text, m.modelID, m.workspace, func(ev api.AGUIEvent) {
					switch ev.Type {
					case "TEXT_MESSAGE_CONTENT":
						if ev.Delta != "" {
							ch <- ev.Delta
						}
					case "TOOL_CALL_START":
						if ev.ToolCallName != "" {
							ch <- "\n" + colYellow + "[工具] " + ev.ToolCallName + colReset + "\n"
						}
					case "RUN_ERROR":
						if ev.Message != "" {
							ch <- "\n" + colRed + "[错误] " + ev.Message + colReset + "\n"
						}
					}
				})
			}()
			return m, listenStream(ch)
		case "backspace":
			if len(m.input) > 0 {
				m.input = m.input[:len(m.input)-1]
			}
			return m, nil
		default:
			// 可打印单字符。
			if len(msg.String()) == 1 {
				m.input += msg.String()
			}
			return m, nil
		}
	case streamMsg:
		m.output += msg.text
		if m.ch != nil {
			return m, listenStream(m.ch)
		}
		return m, nil
	case streamDoneMsg:
		m.streaming = false
		m.ch = nil
		m.output += "\n"
		return m, nil
	}
	return m, nil
}

// View 渲染当前界面。
func (m chatModel) View() string {
	if m.quit {
		return "再见。\n"
	}
	prompt := colGreen + "❯" + colReset + " " + m.input
	if m.streaming {
		prompt = colYellow + "…" + colReset + " " + m.input
	}
	return m.output + "\n" + prompt
}

// listenStream 持续从 channel 读取流式片段，直到 channel 关闭。
func listenStream(ch chan string) bubbletea.Cmd {
	return func() bubbletea.Msg {
		text, ok := <-ch
		if !ok {
			return streamDoneMsg{}
		}
		return streamMsg{text: text}
	}
}
