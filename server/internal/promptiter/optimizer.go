package promptiter

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/eval"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"gorm.io/gorm"
)

// promptIterRunTimeout 单次优化整体超时上限（避免异常反射/评估永远卡死）。
const promptIterRunTimeout = 30 * time.Minute

// Service 是 GEPA 反射式优化的核心服务（M5-06）。
type Service struct {
	db        *gorm.DB
	resolve   eval.ModelResolver
	judge     eval.Judge
	reflector Reflector
	timeout   time.Duration
	// runnerFactory 构造单次评估的用例运行器（应用指定指令覆盖）。
	// 默认 newEngineRunner（生产：经引擎调 LLM）；测试可注入 mock 避免真实调模型。
	runnerFactory func(resolve eval.ModelResolver, override string, timeout time.Duration) eval.CaseRunner
}

// NewService 构造优化服务。
//   - resolve：模型配置解析器（与 eval 共用 evalModelResolver）
//   - judge：LLM 裁判（仅 llm 评分器需要，可为 nil）
//   - reflector：反射器（生产用 EngineReflector，测试可注入 mock）
func NewService(db *gorm.DB, resolve eval.ModelResolver, judge eval.Judge, reflector Reflector) *Service {
	return &Service{
		db:        db,
		resolve:   resolve,
		judge:     judge,
		reflector: reflector,
		timeout:   90 * time.Second,
		runnerFactory: func(resolve eval.ModelResolver, override string, timeout time.Duration) eval.CaseRunner {
			return newEngineRunner(resolve, override, timeout)
		},
	}
}

// Optimize 同步执行一次完整优化（baseline→弱项→反射→应用→重评→决策），
// 创建运行记录并在同一调用内更新到终态。供单测与需要阻塞语义的调用方使用。
func (s *Service) Optimize(ctx context.Context, req Request) (*model.PromptIterRun, error) {
	req.normalize()
	run := newRun(req)
	if err := repo.CreatePromptIterRun(s.db, run); err != nil {
		return nil, err
	}
	if err := s.optimize(ctx, run, req); err != nil {
		// optimize 已负责把 run 标记为 failed；此处仅记录日志。
		log.Printf("[promptiter] run %d error: %v", run.ID, err)
	}
	return run, nil
}

// StartOptimize 异步执行优化：立即返回运行记录（pending/running），实际工作在后台
// goroutine 完成，供 API 异步触发、前端轮询。与 eval.StartRun 同思路。
func (s *Service) StartOptimize(ctx context.Context, req Request) (*model.PromptIterRun, error) {
	req.normalize()
	run := newRun(req)
	if err := repo.CreatePromptIterRun(s.db, run); err != nil {
		return nil, err
	}
	go func() {
		rctx, cancel := context.WithTimeout(context.Background(), promptIterRunTimeout)
		defer cancel()
		if err := s.optimize(rctx, run, req); err != nil {
			log.Printf("[promptiter] run %d error: %v", run.ID, err)
		}
	}()
	return run, nil
}

// Rollback 把某次「已接受/已回滚」运行回滚到其 BeforeContent（再次写回 AgentInstruction，
// 版本自增留痕）。满足「建议可读、可回滚」中的回滚能力。
func (s *Service) Rollback(ctx context.Context, userID, runID uint) (*model.PromptIterRun, error) {
	run, err := repo.GetPromptIterRun(s.db, userID, runID)
	if err != nil {
		return nil, err
	}
	switch run.Status {
	case model.PromptIterStatusAccepted, model.PromptIterStatusRolledBack:
		// 允许回滚（已回滚的运行再回滚等价于恢复 before，安全）。
	default:
		return nil, fmt.Errorf("仅已接受/已回滚的运行可回滚（当前状态 %s）", run.Status)
	}
	if _, cerr := repo.CreateOrUpdateInstruction(s.db, userID, run.InstructionName, run.Role, run.BeforeContent); cerr != nil {
		return nil, cerr
	}
	run.Status = model.PromptIterStatusRolledBack
	_ = repo.UpdatePromptIterRun(s.db, run)
	return run, nil
}

// newRun 由请求构造初始运行记录。
func newRun(req Request) *model.PromptIterRun {
	return &model.PromptIterRun{
		UserID:          req.UserID,
		DatasetID:       req.DatasetID,
		InstructionName: req.InstructionName,
		Role:            req.Role,
		Repeats:         req.Repeats,
		Threshold:       req.Threshold,
		Status:          model.PromptIterStatusRunning,
	}
}

// optimize 是优化的实际工作函数：执行 GEPA 反射闭环并写回运行记录终态。
func (s *Service) optimize(ctx context.Context, run *model.PromptIterRun, req Request) error {
	defer func() {
		fin := time.Now()
		run.FinishedAt = &fin
		_ = repo.UpdatePromptIterRun(s.db, run)
	}()

	// 1. baseline 评估（不施加覆盖）。
	baseline, err := s.runEval(ctx, req, "")
	if err != nil {
		return s.fail(run, err)
	}
	run.BaselineScore = baseline.avg

	// 2. 定位弱项（得分 < 阈值）。
	var weak []WeakCase
	for _, c := range baseline.cases {
		if c.Score < req.Threshold {
			weak = append(weak, WeakCase{
				CaseID:   c.CaseID,
				Input:    c.Input,
				Output:   c.Output,
				Expected: c.Expected,
				Score:    c.Score,
				Passed:   c.Passed,
			})
		}
	}
	run.WeakCount = len(weak)
	if len(weak) == 0 {
		run.Status = model.PromptIterStatusNoImprovement
		run.Reasoning = "基线评估无弱项用例，无需优化"
		return nil
	}

	// 3. 反射生成改进指令（GEPA 的「反思」步）。
	current, _ := repo.GetInstructionContent(s.db, req.UserID, req.InstructionName)
	improved, reasoning, rerr := s.reflector.Reflect(ctx, req.UserID, current, weak)
	if rerr != nil {
		return s.fail(run, rerr)
	}
	run.Reasoning = reasoning
	run.BeforeContent = current
	run.AfterContent = improved

	// 4. 应用：写回 AgentInstruction（版本自增），生产对话经 InstructionOverride 生效。
	if _, cerr := repo.CreateOrUpdateInstruction(s.db, req.UserID, req.InstructionName, req.Role, improved); cerr != nil {
		return s.fail(run, cerr)
	}

	// 5. 用覆盖重评。
	candidate, cerr := s.runEval(ctx, req, improved)
	if cerr != nil {
		// 重评失败 → 回滚到 before，保证不留下无法验证的改动。
		_ = s.rollbackContent(req, current)
		run.Status = model.PromptIterStatusRolledBack
		run.Error = truncate(cerr.Error(), 500)
		return nil
	}
	run.CandidateScore = candidate.avg

	// 6. 决策：候选分 >= 基线分 → 接受；否则回滚。
	// 浮点容差：相等（持平）也应接受，满足验收「提升或持平」。
	if candidate.avg+1e-9 >= baseline.avg {
		run.Status = model.PromptIterStatusAccepted
	} else {
		_ = s.rollbackContent(req, current)
		run.Status = model.PromptIterStatusRolledBack
		run.Reasoning = reasoning + "\n[回滚] 候选分低于基线，已恢复原指令"
	}
	return nil
}

// runEval 在指定指令覆盖下跑一遍评估集，返回平均分与逐用例诊断。
func (s *Service) runEval(ctx context.Context, req Request, override string) (*evalResult, error) {
	ds, err := repo.GetEvalDataset(s.db, req.UserID, req.DatasetID)
	if err != nil {
		return nil, err
	}
	cases, err := repo.ListEvalCases(s.db, req.DatasetID)
	if err != nil {
		return nil, err
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("promptiter: 数据集 %d 无用例", req.DatasetID)
	}
	runner := s.runnerFactory(s.resolve, override, s.timeout)

	var totalScore float64
	var totalAttempts int
	cevals := make([]caseEval, 0, len(cases))
	for i := range cases {
		c := cases[i]
		caseModel := pick(c.ModelID, ds.DefaultModel)
		caseGrader := pick(string(c.Grader), string(ds.DefaultGrader))
		gt, ok := model.ParseGraderType(caseGrader)
		if !ok {
			gt = model.GraderExact
		}
		var caseScore float64
		var casePassed bool
		var lastOut string
		for attempt := 1; attempt <= req.Repeats; attempt++ {
			out, _, rerr := runner.RunCase(ctx, req.UserID, caseModel, c.Input)
			lastOut = out
			if rerr != nil {
				return nil, fmt.Errorf("用例 %d 运行失败: %w", c.ID, rerr)
			}
			score, passed, gerr := eval.GradeWithJudge(s.judge, ctx, req.UserID, gt, c.Input, out, c.Expected)
			if gerr != nil {
				return nil, fmt.Errorf("用例 %d 评分失败: %w", c.ID, gerr)
			}
			caseScore += score
			casePassed = casePassed || passed
			totalScore += score
			totalAttempts++
		}
		ca := caseScore
		if req.Repeats > 0 {
			ca /= float64(req.Repeats)
		}
		cevals = append(cevals, caseEval{
			CaseID:   c.ID,
			Input:    c.Input,
			Output:   lastOut,
			Expected: c.Expected,
			Score:    ca,
			Passed:   casePassed,
		})
	}
	avg := 0.0
	if totalAttempts > 0 {
		avg = totalScore / float64(totalAttempts)
	}
	return &evalResult{avg: avg, cases: cevals}, nil
}

// rollbackContent 把指令内容写回（CreateOrUpdateInstruction 再次自增版本，形成回滚留痕）。
func (s *Service) rollbackContent(req Request, before string) error {
	_, err := repo.CreateOrUpdateInstruction(s.db, req.UserID, req.InstructionName, req.Role, before)
	return err
}

// fail 把运行标记为 failed 并记录错误（不立即写 FinishedAt，由 optimize 的 defer 统一写）。
func (s *Service) fail(run *model.PromptIterRun, err error) error {
	run.Status = model.PromptIterStatusFailed
	run.Error = truncate(err.Error(), 500)
	return err
}

// evalResult 是一次评估的聚合结果。
type evalResult struct {
	avg   float64
	cases []caseEval
}

// caseEval 是单用例的评估诊断。
type caseEval struct {
	CaseID   uint
	Input    string
	Output   string
	Expected string
	Score    float64
	Passed   bool
}

// pick 返回第一个非空字符串（「用例 > 数据集默认」优先级）。
func pick(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// truncate 截断字符串到 max 字节（避免超长错误信息撑爆列宽）。
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
