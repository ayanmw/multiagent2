package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/ayanmw/multiagent2/tool/cli/internal/api"
)

var (
	evalModel   string
	evalGrader  string
	evalRepeats int
	evalWait    bool
	evalTimeout int
)

var evalCmd = &cobra.Command{
	Use:   "eval",
	Short: "评估回归（M5-05）：管理评估集、运行回归、查看分数报告",
	Long: `eval 子命令与 Web「评估回归」页共用同一后端 API：
  gmctl eval list            列出评估集
  gmctl eval cases <dataset>  列出某评估集的用例
  gmctl eval run <dataset>    运行回归（--model/--grader/--repeats 可选，--wait 阻塞到收敛）
  gmctl eval runs <dataset>   列出某评估集的运行历史
  gmctl eval results <run>    查看某次运行的逐条结果`,
}

var evalListCmd = &cobra.Command{
	Use:   "list",
	Short: "列出评估集",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		datasets, err := client.ListEvalDatasets(ctx)
		if err != nil {
			return err
		}
		if len(datasets) == 0 {
			fmt.Println("（无评估集）")
			return nil
		}
		fmt.Printf("%-6s %-24s %-10s %-16s %s\n", "ID", "名称", "评分器", "默认模型", "描述")
		for _, d := range datasets {
			fmt.Printf("%-6d %-24s %-10s %-16s %s\n", d.ID, d.Name, d.DefaultGrader, d.DefaultModel, d.Description)
		}
		return nil
	},
}

var evalCasesCmd = &cobra.Command{
	Use:   "cases <datasetID>",
	Short: "列出评估集用例",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		id, err := parseUintArg(args[0])
		if err != nil {
			return err
		}
		cases, err := client.ListEvalCases(ctx, id)
		if err != nil {
			return err
		}
		if len(cases) == 0 {
			fmt.Println("（无用例）")
			return nil
		}
		fmt.Printf("%-6s %-40s %-30s %-8s\n", "ID", "输入", "期望", "评分器")
		for _, c := range cases {
			in := truncate(c.Input, 38)
			ex := truncate(c.Expected, 28)
			g := c.Grader
			if g == "" {
				g = "默认"
			}
			fmt.Printf("%-6d %-40s %-30s %-8s\n", c.ID, in, ex, g)
		}
		return nil
	},
}

var evalRunCmd = &cobra.Command{
	Use:   "run <datasetID>",
	Short: "运行回归（异步；--wait 阻塞到收敛）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		id, err := parseUintArg(args[0])
		if err != nil {
			return err
		}
		run, err := client.RunEval(ctx, id, evalModel, evalGrader, evalRepeats)
		if err != nil {
			return err
		}
		fmt.Printf("已触发运行 #%d（状态：%s，重复 %d 次）\n", run.ID, run.Status, run.Repeats)
		if !evalWait {
			fmt.Println("（未指定 --wait，运行将在后台异步完成；用 `gmctl eval results <runID>` 查看）")
			return nil
		}
		// 阻塞轮询直到收敛。
		deadline := time.Now().Add(time.Duration(evalTimeout) * time.Second)
		for {
			got, gerr := client.GetEvalRun(ctx, run.ID)
			if gerr != nil {
				return gerr
			}
			if got.Status != "running" {
				printRunSummary(got)
				if got.Status == "done" {
					return printResults(ctx, run.ID)
				}
				return fmt.Errorf("运行失败：%s", got.Error)
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("等待超时（%ds），运行仍在进行；可稍后用 `gmctl eval results %d` 查看", evalTimeout, run.ID)
			}
			time.Sleep(2 * time.Second)
		}
	},
}

var evalRunsCmd = &cobra.Command{
	Use:   "runs <datasetID>",
	Short: "列出评估集的运行历史",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		id, err := parseUintArg(args[0])
		if err != nil {
			return err
		}
		runs, err := client.ListEvalRuns(ctx, id)
		if err != nil {
			return err
		}
		if len(runs) == 0 {
			fmt.Println("（无运行记录）")
			return nil
		}
		fmt.Printf("%-6s %-9s %-12s %-8s %-7s %-9s %-8s %s\n", "ID", "状态", "模型", "评分器", "平均分", "通过率", "用例/试", "创建")
		for _, r := range runs {
			fmt.Printf("%-6d %-9s %-12s %-8s %-7.2f %-8.0f%% %-8s %s\n",
				r.ID, r.Status, truncate(r.Model, 10), r.Grader, r.ScoreAvg, r.PassRate*100,
				fmt.Sprintf("%d/%d", r.TotalCases, r.TotalAttempts), r.CreatedAt)
		}
		return nil
	},
}

var evalResultsCmd = &cobra.Command{
	Use:   "results <runID>",
	Short: "查看某次运行的逐条结果",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		id, err := parseUintArg(args[0])
		if err != nil {
			return err
		}
		return printResults(ctx, id)
	},
}

func printResults(ctx context.Context, runID uint) error {
	results, err := client.ListEvalResults(ctx, runID)
	if err != nil {
		return err
	}
	if len(results) == 0 {
		fmt.Println("（无结果）")
		return nil
	}
	fmt.Printf("运行 #%d 结果：\n", runID)
	fmt.Printf("%-7s %-6s %-7s %-40s %-6s %-6s\n", "用例", "尝试", "评分器", "输出", "得分", "通过")
	for _, r := range results {
		status := "通过"
		if !r.Passed {
			status = "未过"
		}
		fmt.Printf("%-7d %-6d %-7s %-40s %-6.2f %-6s\n", r.CaseID, r.Attempt, r.Grader, truncate(r.Output, 38), r.Score, status)
	}
	return nil
}

func printRunSummary(r *api.EvalRun) {
	fmt.Printf("运行 #%d 收敛：状态=%s 平均分=%.2f 通过率=%.0f%% 用例/尝试=%d/%d\n",
		r.ID, r.Status, r.ScoreAvg, r.PassRate*100, r.TotalCases, r.TotalAttempts)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func parseUintArg(s string) (uint, error) {
	var v uint
	_, err := fmt.Sscanf(s, "%d", &v)
	if err != nil || v == 0 {
		return 0, fmt.Errorf("无效的 ID：%q", s)
	}
	return v, nil
}

func init() {
	evalRunCmd.Flags().StringVar(&evalModel, "model", "", "模型覆盖（留空用评估集默认）")
	evalRunCmd.Flags().StringVar(&evalGrader, "grader", "", "评分器覆盖（exact/contains/llm，留空用默认）")
	evalRunCmd.Flags().IntVar(&evalRepeats, "repeats", 1, "每个用例重复运行次数（取稳定分）")
	evalRunCmd.Flags().BoolVar(&evalWait, "wait", false, "阻塞等待运行收敛并打印分数报告")
	evalRunCmd.Flags().IntVar(&evalTimeout, "timeout", 120, "与 --wait 配合的最大等待秒数")

	evalCmd.AddCommand(evalListCmd, evalCasesCmd, evalRunCmd, evalRunsCmd, evalResultsCmd)
	rootCmd.AddCommand(evalCmd)
}
