// Package promptiter 实现 GEPA 反射式 Prompt/技能优化（M5-06）。
//
// 设计：把原本硬编码的 Agent 系统提示词外置为可持久化、可写回的 AgentInstruction
// （见 model.AgentInstruction），并提供一个「评估 → 定位弱项 → 反射改进 → 应用 →
// 再评估 → 决策接受/回滚」的闭环。所有外部依赖（模型解析、裁判、反射器）均可注入，
// 便于纯 Go 单测（mock）而不真实调 LLM。
package promptiter

import (
	"github.com/ayanmw/multiagent2/server/internal/model"
)

// WeakCase 是一条弱项用例的诊断信息，作为反射器的输入（M5-06）。
type WeakCase struct {
	CaseID   uint
	Input    string
	Output   string // 模型实际输出（取最后一次重复）
	Expected string
	Score    float64 // 该用例平均得分（0~1）
	Passed   bool
}

// Request 是一次优化请求。
type Request struct {
	UserID          uint
	DatasetID       uint
	InstructionName string  // 默认 "default"（单代理指令）
	Role            string  // 默认 "single"
	Repeats         int     // 评估重复次数，默认 1
	Threshold       float64 // 弱项判定阈值（score < threshold 视为弱项），默认 0.5
}

// normalize 补齐默认值。
func (r *Request) normalize() {
	if r.InstructionName == "" {
		r.InstructionName = model.DefaultInstructionName
	}
	if r.Role == "" {
		r.Role = "single"
	}
	if r.Repeats <= 0 {
		r.Repeats = 1
	}
	if r.Threshold <= 0 {
		r.Threshold = 0.5
	}
}
