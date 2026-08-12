package model

import (
	"time"

	"gorm.io/gorm"
)

// PromptIterRun 记录一次 promptiter 优化运行（M5-06 GEPA 反射式 Prompt 优化）。
//
// 一次运行 = baseline 评估 → 定位弱项 → LLM 反射生成改进指令 → 写回 AgentInstruction
// → 用覆盖重评 → 对比 Decide（接受 / 回滚）。BeforeContent/AfterContent 同时落库，
// 使「可回滚」只需把 BeforeContent 再次写回即可（版本自增，留痕）。
type PromptIterRun struct {
	gorm.Model
	UserID          uint    `gorm:"not null;index" json:"user_id"`
	DatasetID       uint    `gorm:"not null;index" json:"dataset_id"`
	InstructionName string  `gorm:"size:128;not null;default:default" json:"instruction_name"`
	Role            string  `gorm:"size:32;not null;default:single" json:"role"`
	Repeats         int     `gorm:"not null;default:1" json:"repeats"`
	Threshold       float64 `gorm:"not null;default:0.5" json:"threshold"`
	// Status 取值见下方 PromptIterStatus* 常量。
	Status string `gorm:"size:32;not null;default:pending" json:"status"`
	Error  string `gorm:"type:text" json:"error"`
	// BaselineScore / CandidateScore 是 baseline 与覆盖重评的 0~1 平均分。
	BaselineScore  float64 `json:"baseline_score"`
	CandidateScore float64 `json:"candidate_score"`
	// BeforeContent / AfterContent 是优化前后指令全文，供「可回滚」与「可读」。
	BeforeContent string `gorm:"type:text" json:"before_content"`
	AfterContent  string `gorm:"type:text" json:"after_content"`
	// Reasoning 是反射式改进的理由（可读性）。
	Reasoning string `gorm:"type:text" json:"reasoning"`
	// WeakCount 是定位到的弱项用例数。
	WeakCount       int        `gorm:"not null;default:0" json:"weak_count"`
	FinishedAt      *time.Time `json:"finished_at"`
}

// TableName 固定表名，避免 GORM 复数化规则变化影响既有库。
func (PromptIterRun) TableName() string { return "promptiter_runs" }

// PromptIter 运行状态常量。
const (
	PromptIterStatusPending       = "pending"        // 已创建、待执行
	PromptIterStatusRunning       = "running"        // 执行中
	PromptIterStatusDone          = "done"           // 完成（含 no_improvement，未改指令）
	PromptIterStatusFailed        = "failed"         // 执行出错
	PromptIterStatusAccepted      = "accepted"       // 候选分 >= 基线分，改进被接受
	PromptIterStatusRolledBack    = "rolled_back"    // 候选分 < 基线分，已回滚
	PromptIterStatusNoImprovement = "no_improvement" // 基线无弱项，无需优化
)
