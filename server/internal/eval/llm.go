package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/engine"
)

// LLMRunner 是 CaseRunner 的生产实现：经引擎调 LLM，把用例 input 发给指定模型取输出。
// 每个用例调用创建独立引擎实例并 Close，避免跨用例状态串扰。
type LLMRunner struct {
	resolve ModelResolver
	timeout time.Duration
}

// NewLLMRunner 构造 LLM 用例运行器。resolve 按用户+模型 id 取引擎配置；timeout 为
// 单次用例调用上限（建议与引擎超时一致）。
func NewLLMRunner(resolve ModelResolver, timeout time.Duration) *LLMRunner {
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	return &LLMRunner{resolve: resolve, timeout: timeout}
}

// RunCase 调 LLM 取模型输出；失败或配置缺失返回错误。
func (r *LLMRunner) RunCase(ctx context.Context, userID uint, modelID, input string) (string, int64, error) {
	if r.resolve == nil {
		return "", 0, errors.New("eval: 未配置模型解析器")
	}
	cfg, err := r.resolve(ctx, userID, modelID)
	if err != nil {
		return "", 0, err
	}
	eng, err := engine.New(cfg)
	if err != nil {
		return "", 0, err
	}
	defer eng.Close()

	runCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	start := time.Now()
	out, err := eng.Chat(runCtx, fmt.Sprintf("eval-%d", userID), input, nil)
	lat := time.Since(start).Milliseconds()
	if err != nil {
		return "", lat, err
	}
	return out, lat, nil
}

// LLMJudge 是 Judge 的生产实现：把 (input, output, expected) 交给 LLM 裁判，要求输出
// 0~1 之间的分数（JSON 或裸数字），解析为分值（>=0.5 判通过，由 GradeWithJudge 负责）。
type LLMJudge struct {
	resolve ModelResolver
	timeout time.Duration
}

// NewLLMJudge 构造 LLM 裁判。模型解析为空 modelID 时取用户默认启用模型作为裁判模型。
func NewLLMJudge(resolve ModelResolver, timeout time.Duration) *LLMJudge {
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	return &LLMJudge{resolve: resolve, timeout: timeout}
}

// Judge 调 LLM 评 0~1 分；解析失败返回错误。
func (j *LLMJudge) Judge(ctx context.Context, userID uint, input, output, expected string) (float64, error) {
	if j.resolve == nil {
		return 0, errors.New("eval: 未配置模型解析器")
	}
	cfg, err := j.resolve(ctx, userID, "")
	if err != nil {
		return 0, err
	}
	eng, err := engine.New(cfg)
	if err != nil {
		return 0, err
	}
	defer eng.Close()

	runCtx, cancel := context.WithTimeout(ctx, j.timeout)
	defer cancel()
	out, err := eng.Chat(runCtx, fmt.Sprintf("eval-judge-%d", userID), buildJudgePrompt(input, output, expected), nil)
	if err != nil {
		return 0, err
	}
	return parseJudgeScore(out)
}

// buildJudgePrompt 构造裁判提示词：要求模型严格按 (输入, 实际输出, 期望输出) 给 0~1 分，
// 只输出 {"score": <浮点>} JSON（不要解释文字、不要代码围栏）。
func buildJudgePrompt(input, output, expected string) string {
	return strings.TrimSpace(`
你是一个严格的「评分裁判」。给定一条测试用例的输入、模型实际输出、以及期望输出，请评估模型输出对期望输出的满足程度，给出一个 0 到 1 之间的分数（1 表示完全满足，0 表示完全不满足）。

要求：
1. 只输出一个 JSON 对象，不要包含任何解释文字、不要使用代码围栏：{"score": <0到1之间的小数>}
2. score 必须在区间 [0,1] 内。

===== 用例输入 =====
` + input + `
===== 模型实际输出 =====
` + output + `
===== 期望输出 =====
` + expected + `
===== 请评分 =====`)
}

// judgeJSONRe 用于从模型可能夹带的冗余文字中抠出第一个 JSON 对象（匹配最外层 {}）。
var judgeJSONRe = regexp.MustCompile(`(?s)\{.*\}`)

// floatRe 匹配一个浮点数（含整数、正负号、小数）。
var floatRe = regexp.MustCompile(`[-+]?\d*\.?\d+`)

// parseJudgeScore 从 LLM 裁判返回的文本中提取 0~1 分数。兼容纯数字、包裹在解释文字中
// 的数字、以及 {"score": 0.8} JSON。解析失败返回错误。
func parseJudgeScore(raw string) (float64, error) {
	s := strings.TrimSpace(raw)
	// 先尝试抠 JSON 对象（如 {"score": 0.8}）。
	if m := judgeJSONRe.FindString(s); m != "" {
		var obj struct {
			Score float64 `json:"score"`
		}
		if err := json.Unmarshal([]byte(m), &obj); err == nil {
			return clamp01(obj.Score), nil
		}
	}
	// 退化：用正则抓第一个浮点数。
	if f := floatRe.FindString(s); f != "" {
		var v float64
		if _, err := fmt.Sscanf(f, "%f", &v); err == nil {
			return clamp01(v), nil
		}
	}
	return 0, fmt.Errorf("eval: 无法解析裁判分数: %q", s)
}

// clamp01 把分数夹到 [0,1]。
func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
