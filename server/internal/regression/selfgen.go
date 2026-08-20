package regression

// 本文件实现 M8-05「评估集自举」：把 evolution 飞轮提取出的技能正文（SKILL.md）
// 反向生成一批可验证的 eval 用例，使「新技能自动进评估集」，回归分数可对比。
//
// 生成策略（全部用 contains 召回式评分，避免精确匹配 flaky）：
//  1. 保底知识用例：问技能用途/适用场景，Expected=技能名——模型遵循技能时必提名字，
//     是最稳的保底断言；
//  2. 标题用例：正文的 `## / ###` 小节即技能的子能力/步骤，让模型按该小节作答，
//     Expected=标题关键词（CJK 最长片段或首英文词），模型复述规范名即命中；
//  3. 命令用例：正文行内代码中的命令词干（如 `git worktree`、`go build`），
//     Expected=命令前两个词——模型遵循技能时大概率提及该命令。
//
// 数量上限 MaxCasesPerSkill 防止超长技能生成过多用例撑爆回归时长；每条用例
// Grader=contains（大小写不敏感子串匹配），ModelID 留空继承评估集默认。

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/ayanmw/multiagent2/server/internal/model"
)

// MaxCasesPerSkill 是单个技能最多生成的用例数（保底 1 + 标题 ≤4 + 命令 ≤3）。
const MaxCasesPerSkill = 8

// maxHeadingCases / maxCommandCases 是标题/命令用例各自的上限。
const (
	maxHeadingCases = 4
	maxCommandCases = 3
)

var (
	// headingRe 匹配 `## / ###` 标题行（Markdown 二级/三级小节；一级是文档名不生成用例）。
	headingRe = regexp.MustCompile(`(?m)^#{2,3}\s+(.+)$`)
	// inlineCodeRe 匹配行内代码 `` `...` ``（命令/参数/标识符通常以此书写）。
	inlineCodeRe = regexp.MustCompile("`([^`]+)`")
	// angleParamRe 去掉尖括号参数占位（如 <dir> <id>），保留命令词干。
	angleParamRe = regexp.MustCompile(`<[^>]*>`)
	// cjkRunRe 匹配连续 CJK 片段（用于从标题提取最长的中文关键词）。
	cjkRunRe = regexp.MustCompile(`[\p{Han}]{2,}`)
	// alphaWordRe 匹配「字母开头的词」（命令/关键词的合法形态，含 - _ 与数字）。
	alphaWordRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)
)

// GenerateCases 从候选技能的 SKILL.md 正文反向生成 eval 用例（M8-05 评估集自举）。
// 纯函数：不落库、不调 LLM，只做结构化解析与断言构造，可独立单测。
// 返回的用例均未填 DatasetID（由调用方 Register 在落库时补齐）。
// 解析失败不返回 error：任何技能至少产出保底知识用例，保证「自动进评估集」不因
// 正文格式异常而中断。
func GenerateCases(cand *model.SkillCandidate) ([]model.EvalCase, error) {
	if cand == nil || strings.TrimSpace(cand.Name) == "" {
		return nil, fmt.Errorf("regression: 候选技能缺失（name 为空）")
	}
	name := strings.TrimSpace(cand.Name)

	cases := make([]model.EvalCase, 0, MaxCasesPerSkill)

	// ① 保底知识用例。
	cases = append(cases, model.EvalCase{
		Input: fmt.Sprintf(
			"请说明技能「%s」的用途、适用场景与核心步骤（%s）。",
			name, strings.TrimSpace(cand.Description)),
		Expected: name,
		Grader:   model.GraderContains,
	})

	// ② 标题用例：正文小节即子能力/步骤，逐条生成（去重 + 上限）。
	seenHeading := make(map[string]bool, maxHeadingCases)
	for _, h := range extractHeadings(cand.Body) {
		if len(cases) >= MaxCasesPerSkill {
			break
		}
		if seenHeading[h] {
			continue
		}
		seenHeading[h] = true
		if len(seenHeading) > maxHeadingCases {
			continue
		}
		key := headingKey(h)
		if key == "" {
			continue
		}
		cases = append(cases, model.EvalCase{
			Input: fmt.Sprintf(
				"应用技能「%s」，请按其中「%s」部分的要求说明应如何操作（需结合该技能完整规范回答）。",
				name, h),
			Expected: key,
			Grader:   model.GraderContains,
		})
	}

	// ③ 命令用例：行内代码里的命令词干（去重 + 上限）。
	seenCmd := make(map[string]bool, maxCommandCases)
	for _, code := range extractInlineCodes(cand.Body) {
		if len(cases) >= MaxCasesPerSkill {
			break
		}
		stem := commandStem(code)
		if stem == "" || seenCmd[stem] {
			continue
		}
		seenCmd[stem] = true
		if len(seenCmd) > maxCommandCases {
			continue
		}
		cases = append(cases, model.EvalCase{
			Input: fmt.Sprintf(
				"应用技能「%s」时，说明命令 `%s` 在什么场景下使用、如何正确使用。",
				name, stem),
			Expected: stem,
			Grader:   model.GraderContains,
		})
	}

	return cases, nil
}

// extractHeadings 提取正文中全部 `## / ###` 标题（去 Markdown 标记并 trim）。
func extractHeadings(body string) []string {
	matches := headingRe.FindAllStringSubmatch(body, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		h := strings.TrimSpace(m[1])
		if h != "" {
			out = append(out, h)
		}
	}
	return out
}

// extractInlineCodes 提取正文中全部行内代码片段（trim）。
func extractInlineCodes(body string) []string {
	matches := inlineCodeRe.FindAllStringSubmatch(body, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		code := strings.TrimSpace(m[1])
		if code != "" {
			out = append(out, code)
		}
	}
	return out
}

// headingKey 从标题提取「模型回复中最可能出现的」关键词：优先最长 CJK 片段
// （如「PR / 合并流程」→「合并流程」），否则取首个 ≥3 字母英文词，兜底标题原文。
func headingKey(h string) string {
	h = strings.TrimSpace(h)
	if k := longestCJK(h); k != "" {
		return k
	}
	for _, w := range strings.Fields(h) {
		w = strings.Trim(w, "`\"'()[]{}:：/、,，;；")
		if len(w) >= 3 && alphaWordRe.MatchString(w) {
			return w
		}
	}
	return h
}

// commandStem 从行内代码提取命令词干：去掉尖括号参数后取前两个「字母开头的词」
// （如 `git worktree add <dir>` → `git worktree`）。单命令词（如 `git`、`main`）
// 太泛不做断言，返回空串。
func commandStem(code string) string {
	code = strings.TrimSpace(code)
	code = angleParamRe.ReplaceAllString(code, " ")
	fields := strings.Fields(code)
	if len(fields) == 0 || !alphaWordRe.MatchString(fields[0]) {
		return ""
	}
	if len(fields) >= 2 && alphaWordRe.MatchString(fields[1]) {
		return fields[0] + " " + fields[1]
	}
	return ""
}

// longestCJK 返回字符串中最长的连续 CJK 片段（≥2 字）；无则返回空串。
func longestCJK(s string) string {
	best := ""
	for _, m := range cjkRunRe.FindAllString(s, -1) {
		if len([]rune(m)) > len([]rune(best)) {
			best = m
		}
	}
	return best
}
