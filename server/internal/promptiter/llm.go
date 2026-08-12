package promptiter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/engine"
	"github.com/ayanmw/multiagent2/server/internal/eval"
)

// Reflector 把弱项诊断反射为「改进后的 Agent 指令」（M5-06 GEPA 反射）。
// 生产实现走 LLM；测试可注入 mock（不真实调模型）。
type Reflector interface {
	Reflect(ctx context.Context, userID uint, current string, weak []WeakCase) (improved string, reasoning string, err error)
}

// EngineReflector 是 Reflector 的生产实现：把 (当前指令 + 弱项用例) 交给 LLM，
// 要求返回改进后的系统提示词与改进理由。
type EngineReflector struct {
	resolve eval.ModelResolver
	timeout time.Duration
}

// NewEngineReflector 构造生产反射器。resolve 为空 modelID 时取用户默认启用模型。
func NewEngineReflector(resolve eval.ModelResolver, timeout time.Duration) *EngineReflector {
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	return &EngineReflector{resolve: resolve, timeout: timeout}
}

// Reflect 经引擎调 LLM，产出改进后的系统指令与理由。
func (r *EngineReflector) Reflect(ctx context.Context, userID uint, current string, weak []WeakCase) (string, string, error) {
	if r.resolve == nil {
		return "", "", errors.New("promptiter: 未配置模型解析器")
	}
	cfg, err := r.resolve(ctx, userID, "")
	if err != nil {
		return "", "", err
	}
	eng, err := engine.New(cfg)
	if err != nil {
		return "", "", err
	}
	defer eng.Close()

	runCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	out, err := eng.Chat(runCtx, fmt.Sprintf("promptiter-reflect-%d", userID), buildReflectPrompt(current, weak), nil)
	if err != nil {
		return "", "", err
	}
	improved, reasoning := parseReflectOutput(out)
	if improved == "" {
		return "", "", errors.New("promptiter: 反射未能产出改进指令")
	}
	return improved, reasoning, nil
}

// engineRunner 是 eval.CaseRunner 的实现，额外在每个用例上施加指令覆盖（M5-06）。
// 通过把覆盖写进 engine.ModelConfig.InstructionOverride，使评估在「应用改进后提示词」
// 的 Agent 上进行；override 为空时回退引擎内置默认指令。
type engineRunner struct {
	resolve  eval.ModelResolver
	override string
	timeout  time.Duration
}

func newEngineRunner(resolve eval.ModelResolver, override string, timeout time.Duration) *engineRunner {
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	return &engineRunner{resolve: resolve, override: override, timeout: timeout}
}

// RunCase 实现 eval.CaseRunner：解析模型配置 → 施加覆盖 → 建引擎 → 取输出。
func (r *engineRunner) RunCase(ctx context.Context, userID uint, modelID, input string) (string, int64, error) {
	if r.resolve == nil {
		return "", 0, errors.New("promptiter: 未配置模型解析器")
	}
	cfg, err := r.resolve(ctx, userID, modelID)
	if err != nil {
		return "", 0, err
	}
	cfg.InstructionOverride = r.override
	eng, err := engine.New(cfg)
	if err != nil {
		return "", 0, err
	}
	defer eng.Close()

	runCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	start := time.Now()
	out, err := eng.Chat(runCtx, fmt.Sprintf("promptiter-eval-%d", userID), input, nil)
	lat := time.Since(start).Milliseconds()
	if err != nil {
		return "", lat, err
	}
	return out, lat, nil
}

// buildReflectPrompt 构造反射提示词：要求模型严格只输出
// {"improved_instruction": "...", "reasoning": "..."} JSON。
func buildReflectPrompt(current string, weak []WeakCase) string {
	var b strings.Builder
	b.WriteString(`你是一个「提示词优化器」。下面是一个编程助手 Agent 当前的系统指令，以及它在评估集上表现不佳（弱项）的用例。请分析弱项原因，并给出一份改进后的系统指令，使 Agent 在这些用例上表现更好。

要求：
1. 只输出一个 JSON 对象，不要包含任何解释文字、不要使用代码围栏：{"improved_instruction": "<改进后的完整系统指令>", "reasoning": "<为什么这样改，简短>"}
2. improved_instruction 应是可直接作为 Agent 系统提示词的完整文本。

===== 当前系统指令 =====
`)
	b.WriteString(current)
	b.WriteString("\n\n===== 弱项用例（输入 / 模型输出 / 期望输出 / 得分）=====\n")
	for i, w := range weak {
		fmt.Fprintf(&b, "%d. 输入：%s\n   模型输出：%s\n   期望输出：%s\n   得分：%.3f\n",
			i+1, w.Input, w.Output, w.Expected, w.Score)
	}
	b.WriteString("\n===== 请输出改进后的指令 JSON =====")
	return strings.TrimSpace(b.String())
}

// reflectJSONRe 抠出第一个 JSON 对象（匹配最外层 {}）。
var reflectJSONRe = regexp.MustCompile(`(?s)\{.*\}`)

// parseReflectOutput 从 LLM 返回文本解析改进后的指令与理由。
// 兼容 {"improved_instruction":..., "reasoning":...} JSON；若解析失败（模型夹带说明文字
// 但未给 JSON），退化：把整段文本当作 improved_instruction，reasoning 留空。
func parseReflectOutput(raw string) (improved, reasoning string) {
	s := strings.TrimSpace(raw)
	if m := reflectJSONRe.FindString(s); m != "" {
		var obj struct {
			ImprovedInstruction string `json:"improved_instruction"`
			Reasoning           string `json:"reasoning"`
		}
		if err := json.Unmarshal([]byte(m), &obj); err == nil {
			return strings.TrimSpace(obj.ImprovedInstruction), strings.TrimSpace(obj.Reasoning)
		}
	}
	return strings.TrimSpace(s), ""
}
