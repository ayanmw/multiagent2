// Package cmd 定义 gmctl 的全部 cobra 命令（login/logout/me/sessions/chat/tasks）。
package cmd

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/ayanmw/multiagent2/tool/cli/internal/api"
	"github.com/ayanmw/multiagent2/tool/cli/internal/config"
)

var (
	flagBaseURL string
	flagToken   string
	flagConfig  string

	cfg    *config.Config
	client *api.Client
)

var rootCmd = &cobra.Command{
	Use:   "gmctl",
	Short: "GM-Agent 平台命令行客户端（复用 REST+SSE API）",
	Long: `gmctl 是 GM-Agent 企业级多 Agent 协作平台的命令行客户端。
它直接复用后端 REST+SSE 协议，与 Web 前端共用同一套 API 契约：
  - 登录态保存在本地配置文件（<UserConfigDir>/gm-agent-cli/config.json）
  - 对话经 /api/chat/:session_key/stream 的 AG-UI SSE 流式接收
  - 可用 --base-url / GM_AGENT_BASE_URL 指定后端，--token / GM_AGENT_TOKEN 指定令牌`,
	SilenceUsage: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		c, err := config.LoadPath(flagConfig)
		if err != nil {
			return err
		}
		if v := os.Getenv("GM_AGENT_BASE_URL"); v != "" {
			c.BaseURL = v
		}
		if v := os.Getenv("GM_AGENT_TOKEN"); v != "" {
			c.Token = v
		}
		if cmd.Flags().Changed("base-url") {
			c.BaseURL = flagBaseURL
		}
		if cmd.Flags().Changed("token") {
			c.Token = flagToken
		}
		if c.BaseURL == "" {
			c.BaseURL = config.DefaultBaseURL
		}
		cfg = c
		client = api.NewClient(c.BaseURL, c.Token)
		return nil
	},
}

// Execute 是 CLI 入口。
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().StringVar(&flagBaseURL, "base-url", "", "后端地址（默认 http://localhost:8080，可用 GM_AGENT_BASE_URL 环境变量）")
	rootCmd.PersistentFlags().StringVar(&flagToken, "token", "", "JWT（可用 GM_AGENT_TOKEN 环境变量；否则读配置文件）")
	rootCmd.PersistentFlags().StringVar(&flagConfig, "config", "", "配置文件路径（默认 <UserConfigDir>/gm-agent-cli/config.json）")
	rootCmd.AddCommand(loginCmd, logoutCmd, meCmd, sessionsCmd, chatCmd, tasksCmd)
}
