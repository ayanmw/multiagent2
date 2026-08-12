package model

import (
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
)

// GraderType 是评估集采用的「评分器」类型（M5-05 评估回归）。
//   - exact:   精确匹配（输出与期望完全一致，忽略首尾空白）
//   - contains: 召回式匹配（输出包含期望片段，大小写不敏感）—— 对应「召回/命中」语义
//   - llm:     自定义 LLM 评分（由 LLM 裁判按 0~1 打分，>=0.5 判通过）
type GraderType string

const (
	GraderExact   GraderType = "exact"
	GraderContains GraderType = "contains"
	GraderLLM     GraderType = "llm"
)

// ValidGraderTypes 列出所有合法评分器值（供校验与前端提示）。
var ValidGraderTypes = []GraderType{GraderExact, GraderContains, GraderLLM}

// ParseGraderType 校验并归一化评分器字符串（大小写/空白容错）。
func ParseGraderType(s string) (GraderType, bool) {
	t := GraderType(strings.TrimSpace(strings.ToLower(s)))
	switch t {
	case GraderExact, GraderContains, GraderLLM:
		return t, true
	}
	return "", false
}

// EvalRunStatus 是评估运行的生命周期状态。
const (
	EvalRunStatusRunning = "running"
	EvalRunStatusDone    = "done"
	EvalRunStatusFailed  = "failed"
)

// EvalDataset 是一个用户归属的「评估集」（M5-05 评估回归）。
//
// 评估集是一组测试用例（EvalCase）的集合，用于回归测试 Agent 质量：每次改
// Prompt / 模型 / 编排后，跑一遍评估集，对比跑分（稳定分）判断改动是否退步。
//
// 字段说明：
//   - UserID:       归属用户（owner-scoped CRUD，越权即 404）
//   - Name:         展示名（同一用户内唯一）
//   - DefaultGrader: 该数据集下用例的默认评分器（用例未显式指定时继承）
//   - DefaultModel: 可选：该数据集下用例默认使用的模型 id（用例未指定、运行也未指定时生效）
type EvalDataset struct {
	gorm.Model
	UserID        uint        `gorm:"not null;index" json:"user_id"`
	Name          string      `gorm:"size:128;not null;uniqueIndex:idx_user_eval_dataset,priority:1" json:"name"`
	Description   string      `gorm:"size:512" json:"description"`
	DefaultGrader GraderType `gorm:"size:16;not null;default:exact" json:"default_grader"`
	DefaultModel  string      `gorm:"size:128" json:"default_model"`
}

// TableName 固定表名，避免 GORM 复数化规则变化影响既有库。
func (EvalDataset) TableName() string { return "eval_datasets" }

// Validate 校验数据集自洽性：名称必填且合法、默认评分器合法。
func (d *EvalDataset) Validate() error {
	if strings.TrimSpace(d.Name) == "" {
		return errors.New("评估集名称不能为空")
	}
	if len(d.Name) > 128 {
		return errors.New("评估集名称过长（上限 128）")
	}
	if d.DefaultGrader == "" {
		d.DefaultGrader = GraderExact
	}
	if _, ok := ParseGraderType(string(d.DefaultGrader)); !ok {
		return errors.New("非法的默认评分器（应为 exact / contains / llm）")
	}
	if len(d.Description) > 512 {
		return errors.New("评估集描述过长（上限 512）")
	}
	return nil
}

// EvalCase 是评估集下的一个测试用例（M5-05）。
//
// 一次回归会遍历数据集中全部用例：把 Input 发给模型，得到输出后与 Expected 比对，
// 按 Grader 判定得分（0~1）与是否通过。每个用例可单独指定评分器与模型（覆盖数据集默认值）。
type EvalCase struct {
	gorm.Model
	DatasetID uint        `gorm:"not null;index" json:"dataset_id"`
	Input     string      `gorm:"type:text;not null" json:"input"`
	Expected  string      `gorm:"type:text" json:"expected"`
	Grader    GraderType `gorm:"size:16" json:"grader"` // 为空 → 继承数据集默认
	ModelID   string      `gorm:"size:128" json:"model"` // 可选：该用例指定模型 id
}

// TableName 固定表名。
func (EvalCase) TableName() string { return "eval_cases" }

// Validate 校验用例自洽性：输入必填、评分器（若指定）合法。
func (c *EvalCase) Validate() error {
	if strings.TrimSpace(c.Input) == "" {
		return errors.New("用例输入 input 不能为空")
	}
	if c.Grader != "" {
		if _, ok := ParseGraderType(string(c.Grader)); !ok {
			return errors.New("非法的评分器（应为 exact / contains / llm）")
		}
	}
	if len(c.Input) > 1<<20 {
		return errors.New("用例输入过长")
	}
	if len(c.Expected) > 1<<20 {
		return errors.New("用例期望值过长")
	}
	return nil
}

// EvalRun 是一次评估运行的记录（M5-05）。
//
// 一次运行针对某个数据集：把全部用例各跑 Repeats 次（取稳定分），聚合得分与通过率。
// 状态由 running → done（全部用例跑完）/ failed（整体失败）。
type EvalRun struct {
	gorm.Model
	DatasetID     uint    `gorm:"not null;index" json:"dataset_id"`
	UserID        uint    `gorm:"not null;index" json:"user_id"`
	ModelID       string  `gorm:"size:128" json:"model"` // 运行级模型（per-case 时为 "per-case"）
	Grader        string  `gorm:"size:16" json:"grader"` // 运行级评分器（per-case 时为 "per-case"）
	Repeats       int     `gorm:"not null;default:1" json:"repeats"`
	Status        string  `gorm:"size:16;not null;default:running" json:"status"`
	ScoreAvg      float64 `json:"score_avg"` // 全部尝试的平均分（0~1）
	PassRate      float64 `json:"pass_rate"` // 通过尝试占比（0~1）
	TotalCases    int     `json:"total_cases"`
	TotalAttempts int     `json:"total_attempts"`
	Error         string  `gorm:"size:512" json:"error"`
	StartedAt     *time.Time `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at"`
}

// TableName 固定表名。
func (EvalRun) TableName() string { return "eval_runs" }

// EvalResult 是一次运行中单个用例单次尝试的结果（M5-05）。
//
// 每个用例跑 Repeats 次 → 产生 Repeats 条 EvalResult；ScoreAvg/PassRate 由这些结果聚合。
// 保留每次尝试便于「多次跑取稳定分」透明可追溯。
type EvalResult struct {
	gorm.Model
	RunID     uint        `gorm:"not null;index" json:"run_id"`
	DatasetID uint        `gorm:"not null;index" json:"dataset_id"`
	CaseID    uint        `gorm:"not null;index" json:"case_id"`
	Attempt   int         `gorm:"not null" json:"attempt"`
	Grader    GraderType `gorm:"size:16" json:"grader"`
	Output    string      `gorm:"type:text" json:"output"`
	Score     float64     `json:"score"`
	Passed    bool        `gorm:"not null;default:false" json:"passed"`
	LatencyMs int64       `gorm:"not null;default:0" json:"latency_ms"`
	Error     string      `gorm:"size:512" json:"error"`
}

// TableName 固定表名。
func (EvalResult) TableName() string { return "eval_results" }
