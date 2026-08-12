package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/ayanmw/multiagent2/tool/cli/internal/api"
)

var (
	piInstruction string
	piRole        string
	piRepeats     int
	piThreshold   float64
	piWait        bool
	piTimeout     int
)

var promptiterCmd = &cobra.Command{
	Use:   "promptiter",
	Short: "GEPA 反射式 Prompt 优化（M5-06）：跑评估→定位弱项→反射改进→应用→再评估→决策",
	Long: `promptiter 子命令与 Web「Prompt 优化」页共用同一后端 API：
  gmctl promptiter optimize <dataset>   触发一次 GEPA 反射优化（--wait 阻塞到收敛）
  gmctl promptiter runs                 列出优化运行历史
  gmctl promptiter run <runID>          查看某次运行详情（含改进前后指令与理由）
  gmctl promptiter rollback <runID>     回滚某次运行到优化前指令
  gmctl promptiter instructions         列出当前可优化指令
  gmctl promptiter set <name>           手动写回一条指令（--content）`,
}

var piOptimizeCmd = &cobra.Command{
	Use:   "optimize <datasetID>",
	Short: "触发一次 GEPA 反射优化（异步；--wait 阻塞到收敛）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		id, err := parseUintArg(args[0])
		if err != nil {
			return err
		}
		run, err := client.OptimizePromptIter(ctx, id, piInstruction, piRole, piRepeats, piThreshold)
		if err != nil {
			return err
		}
		fmt.Printf("已触发优化运行 #%d（指令=%s，阈值=%.2f，重复=%d）\n",
			run.ID, run.InstructionName, run.Threshold, run.Repeats)
		if !piWait {
			fmt.Println("（未指定 --wait，运行将在后台异步完成；用 `gmctl promptiter run <id>` 查看）")
			return nil
		}
		deadline := time.Now().Add(time.Duration(piTimeout) * time.Second)
		for {
			got, gerr := client.GetPromptIterRun(ctx, run.ID)
			if gerr != nil {
				return gerr
			}
			if got.Status != "running" && got.Status != "pending" {
				printPIRun(got)
				return nil
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("等待超时（%ds），运行仍在进行；可稍后用 `gmctl promptiter run %d` 查看", piTimeout, run.ID)
			}
			time.Sleep(2 * time.Second)
		}
	},
}

var piRunsCmd = &cobra.Command{
	Use:   "runs",
	Short: "列出优化运行历史",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		runs, err := client.ListPromptIterRuns(ctx)
		if err != nil {
			return err
		}
		if len(runs) == 0 {
			fmt.Println("（无运行记录）")
			return nil
		}
		fmt.Printf("%-6s %-10s %-9s %-7s %-16s %-16s %s\n", "ID", "指令", "状态", "弱项", "基线分", "候选分", "创建")
		for _, r := range runs {
			fmt.Printf("%-6d %-10s %-9s %-7d %-16.3f %-16.3f %s\n",
				r.ID, truncate(r.InstructionName, 8), r.Status, r.WeakCount,
				r.BaselineScore, r.CandidateScore, r.CreatedAt)
		}
		return nil
	},
}

var piRunCmd = &cobra.Command{
	Use:   "run <runID>",
	Short: "查看某次运行详情（含改进前后指令与理由）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		id, err := parseUintArg(args[0])
		if err != nil {
			return err
		}
		run, err := client.GetPromptIterRun(ctx, id)
		if err != nil {
			return err
		}
		printPIRun(run)
		return nil
	},
}

var piRollbackCmd = &cobra.Command{
	Use:   "rollback <runID>",
	Short: "回滚某次运行到优化前指令",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		id, err := parseUintArg(args[0])
		if err != nil {
			return err
		}
		run, err := client.RollbackPromptIter(ctx, id)
		if err != nil {
			return err
		}
		fmt.Printf("已回滚运行 #%d（状态=%s）\n", run.ID, run.Status)
		return nil
	},
}

var piInstructionsCmd = &cobra.Command{
	Use:   "instructions",
	Short: "列出当前可优化指令",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		list, err := client.ListInstructions(ctx)
		if err != nil {
			return err
		}
		if len(list) == 0 {
			fmt.Println("（无指令记录）")
			return nil
		}
		fmt.Printf("%-6s %-16s %-10s %-7s %s\n", "ID", "名称", "角色", "版本", "内容预览")
		for _, i := range list {
			fmt.Printf("%-6d %-16s %-10s %-7d %s\n", i.ID, i.Name, i.Role, i.Version, truncate(i.Content, 50))
		}
		return nil
	},
}

var piSetCmd = &cobra.Command{
	Use:   "set <name>",
	Short: "手动写回一条指令（--content 必填）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		content, _ := cmd.Flags().GetString("content")
		if content == "" {
			return fmt.Errorf("--content 必填")
		}
		ins, err := client.UpdateInstruction(ctx, args[0], content, piRole)
		if err != nil {
			return err
		}
		fmt.Printf("已写回指令 %s（版本=%d）\n", ins.Name, ins.Version)
		return nil
	},
}

func printPIRun(r *api.PromptIterRun) {
	fmt.Printf("运行 #%d  指令=%s 状态=%s 弱项数=%d\n", r.ID, r.InstructionName, r.Status, r.WeakCount)
	fmt.Printf("  基线分=%.3f  候选分=%.3f  阈值=%.2f  重复=%d\n", r.BaselineScore, r.CandidateScore, r.Threshold, r.Repeats)
	if r.Error != "" {
		fmt.Printf("  错误：%s\n", r.Error)
	}
	fmt.Printf("  改进理由：%s\n", truncate(r.Reasoning, 200))
	fmt.Printf("  ── 优化前指令 ──\n%s\n", truncate(r.BeforeContent, 400))
	fmt.Printf("  ── 优化后指令 ──\n%s\n", truncate(r.AfterContent, 400))
}

func init() {
	piOptimizeCmd.Flags().StringVar(&piInstruction, "instruction", "default", "指令名称（默认 default）")
	piOptimizeCmd.Flags().StringVar(&piRole, "role", "single", "指令角色（single/orchestrator/coder）")
	piOptimizeCmd.Flags().IntVar(&piRepeats, "repeats", 1, "评估重复次数")
	piOptimizeCmd.Flags().Float64Var(&piThreshold, "threshold", 0.5, "弱项判定阈值（得分低于此值视为弱项）")
	piOptimizeCmd.Flags().BoolVar(&piWait, "wait", false, "阻塞等待收敛并打印详情")
	piOptimizeCmd.Flags().IntVar(&piTimeout, "timeout", 300, "与 --wait 配合的最大等待秒数")

	piSetCmd.Flags().String("content", "", "指令全文（必填）")
	piSetCmd.Flags().StringVar(&piRole, "role", "single", "指令角色")

	promptiterCmd.AddCommand(piOptimizeCmd, piRunsCmd, piRunCmd, piRollbackCmd, piInstructionsCmd, piSetCmd)
	rootCmd.AddCommand(promptiterCmd)
}
