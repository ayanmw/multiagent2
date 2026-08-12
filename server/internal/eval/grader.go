// Package eval 实现「评估回归」核心（M5-05）。
//
// 设计目标：把一批用例（EvalCase）组织为评估集（EvalDataset），每次改 Prompt/模型/
// 编排后跑一遍回归，对比「稳定分」（多次采样取均值）判断改动是否退步。
//
// 分层：
//   - grader.go：纯函数评分（exact 精确 / contains 召回），不依赖外部服务，便于单测；
//   - service.go：Service.Run 多次采样取稳定分，聚合 ScoreAvg/PassRate 写回 EvalRun；
//     CaseRunner / Judge / ModelResolver 三类依赖均定义接口，测试可注入 mock；
//   - llm.go：CaseRunner / Judge 的生产实现（经引擎调 LLM），由 main.go 注入。
package eval

import (
	"strings"

	"github.com/ayanmw/multiagent2/server/internal/model"
)

// Grade 对一条模型输出按评分器打分（0~1）并判定是否通过。
//
//   - exact:    输出与期望在忽略首尾空白后完全一致 → 1.0 / 通过
//   - contains: 输出包含期望片段（大小写不敏感，对应「召回/命中」语义）→ 1.0 / 通过
//   - llm:      本纯函数不处理（llm 评分器走 GradeWithJudge，由 Judge 给 0~1 分）
//
// 该函数是纯函数，便于单测覆盖精确/召回两类评分逻辑，无需任何外部依赖。
func Grade(grader model.GraderType, output, expected string) (score float64, passed bool) {
	switch grader {
	case model.GraderExact:
		if strings.TrimSpace(output) == strings.TrimSpace(expected) {
			return 1.0, true
		}
		return 0.0, false
	case model.GraderContains:
		if strings.Contains(strings.ToLower(strings.TrimSpace(output)), strings.ToLower(strings.TrimSpace(expected))) {
			return 1.0, true
		}
		return 0.0, false
	default:
		// 未知评分器或 llm（llm 必须经 GradeWithJudge 处理）→ 不通过。
		return 0.0, false
	}
}
