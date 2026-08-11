package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ayanmw/multiagent2/tool/cli/internal/api"
	"github.com/ayanmw/multiagent2/tool/cli/internal/tui"
)

var (
	chatSession   string
	chatModelID   uint
	chatWorkspace string
	chatTitle     string
	chatRepl      bool
)

var chatCmd = &cobra.Command{
	Use:   "chat [message]",
	Short: "发送消息并流式接收回复（一次性模式）；--repl 进入交互模式",
	Long: `发送一条消息到指定会话（或新建会话）并以 AG-UI SSE 流式打印助手回复。

一次性模式：gmctl chat "帮我写个 hello.go" --session <key>
交互模式：  gmctl chat --repl（需 TTY；Ctrl+C/Esc 退出）

标准输出仅含助手文本，便于管道；工具调用/错误等信息输出到标准错误。`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if chatRepl {
			return runRepl()
		}
		if len(args) == 0 {
			return fmt.Errorf("请提供消息内容，或使用 --repl 进入交互模式")
		}
		message := strings.Join(args, " ")
		ctx := context.Background()

		sessionKey := chatSession
		if sessionKey == "" {
			s, err := client.CreateSession(ctx, chatTitle)
			if err != nil {
				return err
			}
			sessionKey = s.SessionKey
		}

		fmt.Printf("Session: %s\n", sessionKey)
		err := client.StreamChat(ctx, sessionKey, message, chatModelID, chatWorkspace, func(ev api.AGUIEvent) {
			switch ev.Type {
			case "TEXT_MESSAGE_CONTENT":
				if ev.Delta != "" {
					fmt.Print(ev.Delta)
				}
			case "TOOL_CALL_START":
				if ev.ToolCallName != "" {
					fmt.Fprintf(os.Stderr, "\n[工具] %s\n", ev.ToolCallName)
				}
			case "RUN_ERROR":
				if ev.Message != "" {
					fmt.Fprintf(os.Stderr, "\n[错误] %s\n", ev.Message)
				}
			}
		})
		fmt.Println()
		return err
	},
}

// runRepl 进入 bubbletea 交互对话（仅在 TTY 下可用）。
func runRepl() error {
	if !isTerminal(os.Stdin) {
		return fmt.Errorf("交互模式需要 TTY 终端；请使用一次性模式：gmctl chat \"消息\"")
	}
	ctx := context.Background()
	sessionKey := chatSession
	if sessionKey == "" {
		s, err := client.CreateSession(ctx, chatTitle)
		if err != nil {
			return err
		}
		sessionKey = s.SessionKey
	}
	m := tui.NewChatModel(client, sessionKey, chatModelID, chatWorkspace)
	p := tui.NewProgram(m)
	_, err := p.Run()
	return err
}

func init() {
	chatCmd.Flags().StringVar(&chatSession, "session", "", "会话 key（缺省自动新建会话）")
	chatCmd.Flags().UintVar(&chatModelID, "model-id", 0, "指定托管模型 id（0=后端默认启用模型）")
	chatCmd.Flags().StringVar(&chatWorkspace, "workspace", "", "绑定工作区 key（缺省后端默认目录）")
	chatCmd.Flags().StringVar(&chatTitle, "title", "CLI 会话", "新建会话的标题")
	chatCmd.Flags().BoolVar(&chatRepl, "repl", false, "进入交互式对话（需 TTY）")
}
