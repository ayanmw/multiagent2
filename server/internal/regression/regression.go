// Package regression 实现「进化飞轮 × 评估回归」联动（M5-08）。
//
// 设计目标：候选技能审批发布时，先把技能登记进评估集（自动进 eval 集），再在发布前
// 跑一次回归自测；若自测未通过，回滚发布并拦截，提示人工修订，避免「会破坏现有能力
// 的技能」被发布进共享技能库。
//
// 分层：
//   - Checker 接口：Register（登记进 eval 集）+ Check（跑回归），调用方据 Check 结果
//     决定是否放行发布；
//   - EvalChecker：生产实现，基于 eval.Service 运行回归；通过 ResolverWithSkill 把被测
//     技能名作为 warm-start 关键词注入引擎，使自测真正用到该技能；
//   - 测试可注入 mock Checker，隔离 LLM 依赖。
package regression

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/eval"
	"github.com/ayanmw/multiagent2/server/internal/engine"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"gorm.io/gorm"
)

// Report 是回归评估的结果报告。
type Report struct {
	DatasetID   uint    `json:"dataset_id"`
	CaseID      uint    `json:"case_id"`
	Passed      bool    `json:"passed"`
	ScoreAvg    float64 `json:"score_avg"`
	PassRate    float64 `json:"pass_rate"`
	TotalCases  int     `json:"total_cases"`
	FailedCases int     `json:"failed_cases"`
	Detail      string  `json:"detail"`
}

// Checker 决定一个候选技能是否可以安全发布：
//   - Register：先把候选技能登记进评估集（新发布技能自动进 eval 集）；
//   - Check：运行其回归自测，返回是否通过（通过才允许发布）。
type Checker interface {
	// Register 确保候选技能在评估集中有一条对应的「评估集+用例」（自动进 eval 集）。
	// 已存在则安全复用（幂等），返回评估集 id 与自动创建的用例 id。
	Register(ctx context.Context, userID uint, cand *model.SkillCandidate) (datasetID, caseID uint, err error)
	// Check 运行指定评估集的回归自测，返回是否通过。
	Check(ctx context.Context, userID, datasetID uint) (Report, error)
}

// ResolverWithSkill 是带「技能关键词」的模型解析器：生产实现在注入 warm-start 时把被测
// 技能经关键词命中，使其进入系统上下文，让自测真正用到该技能（M5-08）。
type ResolverWithSkill func(ctx context.Context, userID uint, modelID, skillKeyword string) (engine.ModelConfig, error)

// EvalChecker 是基于评估回归服务的生产 Checker（M5-08）。
type EvalChecker struct {
	db          *gorm.DB
	minPassRate float64
	timeout     time.Duration
	resolve     ResolverWithSkill
	keyword     string // 当前正在评估的技能名（adapter 读取，Check 内设置）
	svc         *eval.Service
}

// NewEvalChecker 构造评估回归检查器。
//   - minPassRate：通过阈值（默认 1.0，即全部用例通过才算过）；
//   - timeout：单次用例调用上限；
//   - resolve：须能按技能关键词注入 warm-start，使被测技能进入上下文。
func NewEvalChecker(db *gorm.DB, resolve ResolverWithSkill, timeout time.Duration, minPassRate float64) *EvalChecker {
	if minPassRate <= 0 {
		minPassRate = 1.0
	}
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	c := &EvalChecker{db: db, resolve: resolve, timeout: timeout, minPassRate: minPassRate}
	c.svc = eval.NewService(db, c.adapter, eval.NewLLMRunner(c.adapter, timeout), eval.NewLLMJudge(c.adapter, timeout))
	return c
}

// adapter 把 4 参 ResolverWithSkill 适配成 eval.ModelResolver（读取当前 keyword）。
func (c *EvalChecker) adapter(ctx context.Context, userID uint, modelID string) (engine.ModelConfig, error) {
	return c.resolve(ctx, userID, modelID, c.keyword)
}

// datasetName 把技能名映射为评估集名（带 skill: 前缀，便于反解关键词与去重）。
func (c *EvalChecker) datasetName(name string) string { return "skill:" + name }

// Register 确保候选技能在评估集中有一条对应的「评估集+用例」（新发布技能自动进 eval 集）。
// 已存在则安全复用（幂等），不重复建。
func (c *EvalChecker) Register(ctx context.Context, userID uint, cand *model.SkillCandidate) (uint, uint, error) {
	name := c.datasetName(cand.Name)
	ds, err := repo.GetEvalDatasetByName(c.db, userID, name)
	if err == repo.ErrEvalDatasetNotFound {
		ds = &model.EvalDataset{
			UserID:        userID,
			Name:          name,
			Description:   fmt.Sprintf("候选技能「%s」回归自测集（进化飞轮审批发布自动生成）", cand.Name),
			DefaultGrader: model.GraderContains,
		}
		if verr := ds.Validate(); verr != nil {
			return 0, 0, verr
		}
		if cerr := repo.CreateEvalDataset(c.db, ds); cerr != nil {
			return 0, 0, cerr
		}
	} else if err != nil {
		return 0, 0, err
	}
	cases, err := repo.ListEvalCases(c.db, ds.ID)
	if err != nil {
		return 0, 0, err
	}
	var caseID uint
	if len(cases) == 0 {
		cs := &model.EvalCase{
			DatasetID: ds.ID,
			Input: fmt.Sprintf("请运用你可用的名为「%s」的技能来完成以下任务：%s。请先说明你将如何应用该技能，再给出具体执行步骤。",
				cand.Name, cand.Description),
			Expected: cand.Name,
			Grader:   model.GraderContains,
		}
		if verr := cs.Validate(); verr != nil {
			return 0, 0, verr
		}
		if cerr := repo.CreateEvalCase(c.db, cs); cerr != nil {
			return 0, 0, cerr
		}
		caseID = cs.ID
	} else {
		caseID = cases[0].ID
	}
	return ds.ID, caseID, nil
}

// Check 运行指定评估集的回归自测，返回是否通过。
func (c *EvalChecker) Check(ctx context.Context, userID, datasetID uint) (Report, error) {
	ds, err := repo.GetEvalDataset(c.db, userID, datasetID)
	if err != nil {
		return Report{DatasetID: datasetID}, err
	}
	// 从评估集名反解技能名作为 warm-start 关键词，确保被测技能进入上下文。
	c.keyword = strings.TrimPrefix(ds.Name, "skill:")
	run, results, rerr := c.svc.RunSync(ctx, userID, datasetID, "", "", 1)
	if rerr != nil {
		return Report{DatasetID: datasetID}, rerr
	}
	rep := Report{
		DatasetID:  datasetID,
		Passed:     run.Status == model.EvalRunStatusDone && run.PassRate >= c.minPassRate,
		ScoreAvg:   run.ScoreAvg,
		PassRate:   run.PassRate,
		TotalCases: run.TotalCases,
	}
	for _, r := range results {
		if !r.Passed {
			rep.FailedCases++
			rep.Detail += fmt.Sprintf("用例#%d 未通过（得分%.2f）：实际输出「%s」\n", r.CaseID, r.Score, truncate(r.Output, 200))
		}
	}
	if !rep.Passed && rep.Detail == "" {
		rep.Detail = fmt.Sprintf("回归通过率 %.0f%% 低于阈值 %.0f%%，请修订技能后重试", rep.PassRate*100, c.minPassRate*100)
	}
	return rep, nil
}

// truncate 截断字符串到 max 字节（细节字段防爆）。
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
