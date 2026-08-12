package evolution

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/engine"
)

// RawCandidate 是 Extractor 从一次会话 transcript 中提取出的「候选技能」原始字段。
// 不含归属/状态/哈希等持久化元数据——那些由 Service 在落库时补齐。
type RawCandidate struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
}

// Extractor 把一段会话 transcript 提炼成候选技能（name/description/body）。
// 默认实现 LLMExtractor 走真实 LLM；测试可注入 mock 实现，避免依赖外部模型。
type Extractor interface {
	// Extract 针对指定用户（用于解析其模型配置）与 transcript 提取候选技能。
	Extract(ctx context.Context, userID uint, transcript string) (*RawCandidate, error)
}

// ModelResolver 按用户解析出一次 LLM 调用的引擎配置（模型 id / baseURL / apiKey / 协议）。
// 由 main.go 注入，复用与对话端点一致的「默认启用模型 + Provider 解密」逻辑。
type ModelResolver func(ctx context.Context, userID uint) (engine.ModelConfig, error)

// LLMExtractor 是 Extractor 的生产实现：经 engine 调 LLM，把 transcript 提炼成
// 结构化候选技能（JSON）。任意环节失败都返回 error，由 Service 计入扫描错误并跳过该会话。
type LLMExtractor struct {
	resolve ModelResolver
	timeout time.Duration
}

// NewLLMExtractor 构造 LLM 提取器。resolve 负责按用户取模型配置；timeout 为单次
// 提取调用上限（建议与引擎超时一致）。
func NewLLMExtractor(resolve ModelResolver, timeout time.Duration) *LLMExtractor {
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	return &LLMExtractor{resolve: resolve, timeout: timeout}
}

// Extract 调 LLM 提取候选技能；失败或解析不出合法 JSON 时返回错误。
func (e *LLMExtractor) Extract(ctx context.Context, userID uint, transcript string) (*RawCandidate, error) {
	if e.resolve == nil {
		return nil, errors.New("evolution: 未配置模型解析器")
	}
	cfg, err := e.resolve(ctx, userID)
	if err != nil {
		return nil, err
	}
	eng, err := engine.New(cfg)
	if err != nil {
		return nil, err
	}
	defer eng.Close()

	runCtx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	out, err := eng.Chat(runCtx, "evo-extract", buildExtractionPrompt(transcript), nil)
	if err != nil {
		return nil, err
	}
	return parseExtraction(out)
}

// buildExtractionPrompt 构造提取提示词：要求模型从 transcript 中识别「可复用的方法论/
// 技能」，并以 JSON 返回 name/description/body（body 为完整 SKILL.md 内容，含 Markdown
// 标题与操作步骤）。明确「无价值则不返回」以避免空泛候选。
func buildExtractionPrompt(transcript string) string {
	return strings.TrimSpace(`
你是一个「技能提炼器」。下面是一段 AI 编程助手与用户的会话记录（transcript）。
请判断其中是否沉淀出了**可复用的技能/方法论**（例如：某种固定的调试流程、某种代码重构套路、某个工具的约定用法、某个常见任务的标准步骤）。

要求：
1. 如果会话中**没有**可复用、值得沉淀成技能的内容，只回复一行：NO_SKILL（不带任何引号与多余文字）。
2. 如果有，严格只输出一个 JSON 对象（不要包含任何解释文字、不要使用代码围栏），结构如下：
{
  "name": "技能名（仅字母数字下划线连字符，简洁表意）",
  "description": "一句话描述这个技能解决什么问题（10~120 字）",
  "body": "完整的 SKILL.md 内容：以 Markdown 标题组织，包含『适用场景』『操作步骤/要点』『注意事项』，要有可操作的步骤与列表，正文不少于 200 字"
}
3. body 必须是真实、具体、可操作的内容，不要写占位符（如 TODO / 待补充）。

===== TRANSCRIPT START =====
` + transcript + `
===== TRANSCRIPT END =====`)
}

// jsonObjRe 用于从模型可能夹带的冗余文字中抠出第一个 JSON 对象（匹配最外层 {}）。
var jsonObjRe = regexp.MustCompile(`(?s)\{.*\}`)

// parseExtraction 解析 LLM 返回的字符串为 RawCandidate。
// 兼容两种情形：① 纯 JSON；② JSON 被包裹在解释文字（甚至代码围栏）中——先剥离
// 围栏再抠第一个 {...} 子串。解析失败返回错误（交由 Service 跳过该会话）。
func parseExtraction(raw string) (*RawCandidate, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, errors.New("evolution: 模型返回为空")
	}
	// 模型可能回复 NO_SKILL（无价值内容）——不视为错误，调用方据此跳过。
	if strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(s, "```")), "NO_SKILL") ||
		strings.Contains(strings.ToUpper(s), "NO_SKILL") {
		return nil, ErrNoSkill
	}
	// 剥离可能的 ```json ... ``` 代码围栏。
	s = stripCodeFence(s)
	// 抠出第一个 JSON 对象。
	if m := jsonObjRe.FindString(s); m != "" {
		s = m
	}
	var c RawCandidate
	if err := json.Unmarshal([]byte(s), &c); err != nil {
		return nil, errors.New("evolution: 无法解析模型返回的 JSON: " + err.Error())
	}
	c.Name = strings.TrimSpace(c.Name)
	c.Description = strings.TrimSpace(c.Description)
	c.Body = strings.TrimSpace(c.Body)
	if c.Name == "" || c.Body == "" {
		return nil, errors.New("evolution: 模型返回的候选缺少 name 或 body")
	}
	return &c, nil
}

// ErrNoSkill 是「会话无可复用技能」的哨兵错误（非异常，扫描器据此静默跳过）。
var ErrNoSkill = errors.New("evolution: 会话无可复用技能")

// IsNoSkill 判断错误是否为「无技能可提取」（用于扫描器区分「跳过」与「真实错误」）。
func IsNoSkill(err error) bool {
	return errors.Is(err, ErrNoSkill)
}

// stripCodeFence 去掉字符串首尾的 ``` 代码围栏标记。
func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		// 去掉起始 ``` 及其后的语言标识行。
		if idx := strings.Index(s, "\n"); idx >= 0 {
			s = s[idx+1:]
		} else {
			s = s[3:]
		}
	}
	if strings.HasSuffix(s, "```") {
		s = s[:len(s)-3]
	}
	return strings.TrimSpace(s)
}
