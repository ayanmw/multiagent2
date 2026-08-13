package eval

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/engine"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"gorm.io/gorm"
)

// 评估运行整体超时：单次评估可能跨「用例数 × 重复次数」多次调 LLM，故异步 goroutine
// 不绑定 HTTP 请求生命周期（StartRun 用 context.Background 派生），但设一个硬上限避免
// 永远跑不完的回归卡死进程。达到上限时运行被标记 failed，已跑出的结果保留。
const evalRunTimeout = 30 * time.Minute

// CaseRunner 把一条用例的 input 发给指定模型，返回模型输出与耗时（毫秒）。
// 生产实现走引擎（LLM 调用），测试可注入 mock（不真实调模型，避免依赖外部服务）。
type CaseRunner interface {
	RunCase(ctx context.Context, userID uint, modelID, input string) (output string, latencyMs int64, err error)
}

// Judge 是 LLM 裁判：针对 (input, output, expected) 给 0~1 分（分数越高越好）。
// 仅 llm 评分器需要；生产实现走 LLM，测试可注入 mock。
type Judge interface {
	Judge(ctx context.Context, userID uint, input, output, expected string) (score float64, err error)
}

// ModelResolver 按用户与模型 id 解析出一次引擎调用所需的完整配置
// （模型 id / baseURL / apiKey / 协议）。modelID 为空时回退「默认启用模型」。
// 由 main.go 注入，复用与对话端点一致的「默认启用模型 + Provider 解密」逻辑。
type ModelResolver func(ctx context.Context, userID uint, modelID string) (engine.ModelConfig, error)

// Service 是评估回归的核心服务（M5-05）。
type Service struct {
	db      *gorm.DB
	resolve ModelResolver
	runner  CaseRunner
	judge   Judge
}

// NewService 构造评估服务。
//   - resolve：模型配置解析器（生产注入 evalModelResolver）
//   - runner：用例运行器（生产用 LLMRunner，测试用 mock）
//   - judge：LLM 裁判（仅 llm 评分器需要，可为 nil；判分时按需返回配置缺失错误）
func NewService(db *gorm.DB, resolve ModelResolver, runner CaseRunner, judge Judge) *Service {
	return &Service{db: db, resolve: resolve, runner: runner, judge: judge}
}

// StartRun 校验并创建一条运行记录（running），随后在后台 goroutine 异步执行实际评估，
// 立即返回 run（含 id）供前端轮询进度。异步执行不绑定 HTTP 请求生命周期：即使客户端
// 断开，评估仍跑到底并落库，保证「跑回归 → 出分数报告」的完整与可追溯。
// modelOverride / graderOverride 为运行级覆盖（空则不覆盖，继承数据集/用例默认值）。
func (s *Service) StartRun(ctx context.Context, userID, datasetID uint, modelOverride, graderOverride string, repeats int) (*model.EvalRun, error) {
	ds, err := repo.GetEvalDataset(s.db, userID, datasetID)
	if err != nil {
		return nil, err
	}
	cases, err := repo.ListEvalCases(s.db, datasetID)
	if err != nil {
		return nil, err
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("eval: 数据集 %d 没有用例", datasetID)
	}
	if repeats <= 0 {
		repeats = 1
	}

	now := time.Now()
	run := &model.EvalRun{
		DatasetID:  ds.ID,
		UserID:     userID,
		ModelID:    orPerCase(modelOverride),
		Grader:     orPerCase(graderOverride),
		Repeats:    repeats,
		Status:     model.EvalRunStatusRunning,
		TotalCases: len(cases),
		StartedAt:  &now,
	}
	if err := repo.CreateEvalRun(s.db, run); err != nil {
		return nil, err
	}

	go func() {
		// 派生独立 ctx（context.Background），避免请求取消中断评估；
		// 但加整体超时硬上限，防止异常回归永远卡死。
		rctx, cancel := context.WithTimeout(context.Background(), evalRunTimeout)
		defer cancel()
		if eerr := s.execute(rctx, run, ds, cases, userID, modelOverride, graderOverride, repeats); eerr != nil {
			// execute 已负责把 run 标记为 failed 并写错误；此处仅记录日志。
			log.Printf("[eval] 运行 %d 执行出错: %v", run.ID, eerr)
		}
	}()
	return run, nil
}

// RunSync 是 StartRun 的同步变体：在同一调用内（受整体超时约束）跑完整个评估，
// 立即返回 EvalRun 与逐条结果。用于需要同步拿到结论的场景——典型如 M5-08 飞轮×回归
// 联动：发布技能前必须确认其自测通过，不能异步轮询。
// 与 StartRun 共享 execute 的聚合与落库逻辑；runs/repeats 语义一致。
func (s *Service) RunSync(ctx context.Context, userID, datasetID uint, modelOverride, graderOverride string, repeats int) (*model.EvalRun, []*model.EvalResult, error) {
	ds, err := repo.GetEvalDataset(s.db, userID, datasetID)
	if err != nil {
		return nil, nil, err
	}
	cases, err := repo.ListEvalCases(s.db, datasetID)
	if err != nil {
		return nil, nil, err
	}
	if len(cases) == 0 {
		return nil, nil, fmt.Errorf("eval: 数据集 %d 没有用例", datasetID)
	}
	if repeats <= 0 {
		repeats = 1
	}
	now := time.Now()
	run := &model.EvalRun{
		DatasetID:  ds.ID,
		UserID:     userID,
		ModelID:    orPerCase(modelOverride),
		Grader:     orPerCase(graderOverride),
		Repeats:    repeats,
		Status:     model.EvalRunStatusRunning,
		TotalCases: len(cases),
		StartedAt:  &now,
	}
	if err := repo.CreateEvalRun(s.db, run); err != nil {
		return nil, nil, err
	}
	rctx, cancel := context.WithTimeout(ctx, evalRunTimeout)
	defer cancel()
	if eerr := s.execute(rctx, run, ds, cases, userID, modelOverride, graderOverride, repeats); eerr != nil {
		vals, _ := repo.ListEvalResults(s.db, run.ID)
		return run, toResultPtrs(vals), eerr
	}
	vals, err := repo.ListEvalResults(s.db, run.ID)
	if err != nil {
		return run, nil, err
	}
	return run, toResultPtrs(vals), nil
}

// toResultPtrs 把值切片转为指针切片（RunSync 对外统一返回 []*model.EvalResult）。
func toResultPtrs(vals []model.EvalResult) []*model.EvalResult {
	pts := make([]*model.EvalResult, 0, len(vals))
	for i := range vals {
		pts = append(pts, &vals[i])
	}
	return pts
}

// execute 是评估的实际工作函数（在后台 goroutine 中运行）。遍历全部用例，每个用例跑
// repeats 次，逐次打分并落库 EvalResult，最后聚合 ScoreAvg/PassRate 写回 EvalRun。
// 任何「结果落库」失败属严重错误，直接终止整个运行并返回（由调用方记录日志）。
// 任何「用例运行/评分」错误会终止该用例剩余尝试，并标记整体 failed（已跑结果保留）。
func (s *Service) execute(ctx context.Context, run *model.EvalRun, ds *model.EvalDataset, cases []model.EvalCase, userID uint, modelOverride, graderOverride string, repeats int) error {
	var totalScore float64
	var totalPassed int
	var totalAttempts int
	var runErr string

	for i := range cases {
		c := cases[i]
		caseModel := pick(c.ModelID, modelOverride, ds.DefaultModel)
		caseGrader := pick(string(c.Grader), graderOverride, string(ds.DefaultGrader))
		gt, ok := model.ParseGraderType(caseGrader)
		if !ok {
			gt = model.GraderExact
		}
		for attempt := 1; attempt <= repeats; attempt++ {
			res := &model.EvalResult{
				RunID:     run.ID,
				DatasetID: ds.ID,
				CaseID:    c.ID,
				Attempt:   attempt,
				Grader:    gt,
			}
			out, lat, rerr := s.runner.RunCase(ctx, userID, caseModel, c.Input)
			res.LatencyMs = lat
			if rerr != nil {
				res.Error = truncate(rerr.Error(), 500)
				runErr = fmt.Sprintf("用例 %d 第 %d 次运行失败: %v", c.ID, attempt, rerr)
				log.Printf("[eval] %s", runErr)
			} else {
				res.Output = out
				score, passed, gerr := GradeWithJudge(s.judge, ctx, userID, gt, c.Input, out, c.Expected)
				if gerr != nil {
					res.Error = truncate(gerr.Error(), 500)
					runErr = fmt.Sprintf("用例 %d 评分失败: %v", c.ID, gerr)
					log.Printf("[eval] %s", runErr)
				} else {
					res.Score = score
					res.Passed = passed
					totalScore += score
					if passed {
						totalPassed++
					}
				}
			}
			totalAttempts++
			if werr := repo.CreateEvalResult(s.db, res); werr != nil {
				// 结果落库失败属严重错误：标记 failed 并终止。
				fin := time.Now()
				run.Status = model.EvalRunStatusFailed
				run.Error = truncate(fmt.Sprintf("写入结果失败: %v", werr), 500)
				run.FinishedAt = &fin
				_ = repo.UpdateEvalRun(s.db, run)
				return werr
			}
			if runErr != "" {
				// 运行/评分错误：终止该用例剩余尝试。
				break
			}
		}
		if runErr != "" {
			break
		}
	}

	// 聚合最终指标（无论成功/失败，已跑出的结果都参与统计）。
	if totalAttempts > 0 {
		run.ScoreAvg = totalScore / float64(totalAttempts)
		run.PassRate = float64(totalPassed) / float64(totalAttempts)
	}
	run.TotalAttempts = totalAttempts
	if runErr != "" {
		run.Status = model.EvalRunStatusFailed
		run.Error = truncate(runErr, 500)
	} else {
		run.Status = model.EvalRunStatusDone
	}
	fin := time.Now()
	run.FinishedAt = &fin
	return repo.UpdateEvalRun(s.db, run)
}

// GradeWithJudge 对单一结果评分：非 llm 评分器走纯函数 Grade；llm 评分器交给 Judge
// 评 0~1 分（>=0.5 通过）。Judge 为 nil 且需要 llm 时返回错误，暴露配置缺失。
func GradeWithJudge(judge Judge, ctx context.Context, userID uint, grader model.GraderType, input, output, expected string) (score float64, passed bool, err error) {
	if grader != model.GraderLLM {
		s, p := Grade(grader, output, expected)
		return s, p, nil
	}
	if judge == nil {
		return 0, false, errors.New("eval: LLM 评分器未配置 Judge")
	}
	s, jerr := judge.Judge(ctx, userID, input, output, expected)
	if jerr != nil {
		return 0, false, jerr
	}
	if s < 0 {
		s = 0
	}
	if s > 1 {
		s = 1
	}
	return s, s >= 0.5, nil
}

// pick 返回第一个非空字符串（「用例 > 运行级覆盖 > 数据集默认」优先级）。
func pick(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// orPerCase 把运行级覆盖为空时记为 "per-case"（表示该运行按各用例/数据集默认值分派），
// 用于 EvalRun 的 Model/Grader 字段可读展示。
func orPerCase(v string) string {
	if v == "" {
		return "per-case"
	}
	return v
}

// truncate 截断字符串到 max 字节（避免超长错误信息撑爆列宽）。
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
