package cmd

import (
	"context"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var sessionsLimit int

var sessionsCmd = &cobra.Command{
	Use:   "sessions",
	Short: "列出当前用户的会话",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		list, err := client.ListSessions(ctx)
		if err != nil {
			return err
		}
		if len(list) == 0 {
			fmt.Println("（暂无会话，先用 `gmctl chat \"你好\"` 发起一次对话）")
			return nil
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "KEY\t标题\t更新时间")
		n := len(list)
		if sessionsLimit > 0 && sessionsLimit < n {
			n = sessionsLimit
		}
		for i := 0; i < n; i++ {
			s := list[i]
			fmt.Fprintf(w, "%s\t%s\t%s\n", s.SessionKey, s.Title, s.UpdatedAt)
		}
		return w.Flush()
	},
}

func init() {
	sessionsCmd.Flags().IntVar(&sessionsLimit, "limit", 20, "最多显示条数（0 表示不限制）")
}
