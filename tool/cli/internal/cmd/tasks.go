package cmd

import (
	"context"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var tasksCmd = &cobra.Command{
	Use:   "tasks",
	Short: "列出当前用户的后台任务（taskrun）",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		runs, err := client.ListTaskRuns(ctx)
		if err != nil {
			return err
		}
		if len(runs) == 0 {
			fmt.Println("（暂无后台任务）")
			return nil
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\t状态\t代理\t任务\t创建时间")
		for i := range runs {
			r := runs[i]
			task := r.Task
			if len(task) > 40 {
				task = task[:40] + "…"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", r.ID, r.Status, r.AgentName, task, r.CreatedAt.Format("2006-01-02 15:04:05"))
		}
		return w.Flush()
	},
}
