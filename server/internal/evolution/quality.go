package evolution

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// 质量门控约束（M5-03）：扫描提取出的候选技能必须满足这些下限，否则被拦截
// （不写入 skill_candidates），从源头避免「空泛候选」污染审批队列。
const (
	// MinDescriptionLen 是描述的最小长度（太短视为无信息量）。
	MinDescriptionLen = 10
	// MaxDescriptionLen 与 model.SkillCandidate.Description 的 size:512 对齐。
	MaxDescriptionLen = 512
	// MinBodyLen 是 SKILL.md 正文的最小长度（少于此值视为空泛/占位）。
	MinBodyLen = 200
	// MaxBodyLen 是正文的可接受上限（超过则截断或拒绝，避免超大噪声）。
	MaxBodyLen = 8000
	// MaxNameLen 与 model 层 name 约束对齐。
	MaxNameLen = 128
)

// nameRe 限制技能名仅含 [A-Za-z0-9_-]，与 skillrepo / model 约束一致。
var nameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// vaguePhrases 是「空泛/占位」描述或正文中出现即判低质的短语（大小写不敏感）。
// 命中任一即视为不合格，避免把「TODO / 占位 / 待补充」这类草稿塞进审批队列。
var vaguePhrases = []string{
	"todo", "tbd", "占位", "待补充", "待填写", "占位符",
	"请在此", "示例占位", "this is a placeholder", "lorem ipsum",
}

// QualityResult 是质量门控的判定结果。
type QualityResult struct {
	Passed bool     // 是否通过门控
	Notes  []string // 改进建议 / 未通过原因（供候选 QualityNotes 或日志）
}

// ValidateCandidateName 校验候选技能名是否合法（仅 [A-Za-z0-9_-]，长度 <= MaxNameLen）。
func ValidateCandidateName(name string) bool {
	return name != "" && len(name) <= MaxNameLen && nameRe.MatchString(name)
}

// ContentHash 计算候选技能的规范化内容哈希（name + body 的 sha256 十六进制）。
// 用于跨会话去重：同一套技能的提取只保留一条候选。
func ContentHash(name, body string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(name) + "\n\n" + strings.TrimSpace(body)))
	return hex.EncodeToString(sum[:])
}

// normalizeBody 对正文做轻量归一化（折叠多余空白），用于长度/结构判定前。
// 不改变语义，仅提升判定稳定性。
func normalizeBody(body string) string {
	return strings.TrimSpace(body)
}

// hasStructure 判定正文是否具备「技能文档」最基本的内部结构：
// 至少一个 Markdown 标题（#），或包含「步骤/step」字样，或存在编号/无序列表项。
// 这是「空泛候选」的核心拦截点——纯散文、无操作步骤的内容没有复用价值。
func hasStructure(body string) bool {
	if strings.Contains(body, "#") {
		return true
	}
	low := strings.ToLower(body)
	if strings.Contains(low, "步骤") || strings.Contains(low, "step") {
		return true
	}
	// 编号列表（"1." / "2."）或有序无序列表项（"- " / "* "）
	if numberedListRe.MatchString(body) || bulletListRe.MatchString(body) {
		return true
	}
	return false
}

// numberedListRe / bulletListRe 匹配常见的列表写法。
var (
	numberedListRe = regexp.MustCompile(`(?m)^\s*\d+\.\s+\S`)
	bulletListRe   = regexp.MustCompile(`(?m)^\s*[-*]\s+\S`)
)

// containsVague 判定文本是否含空泛/占位短语（任一即命中）。
func containsVague(text string) bool {
	low := strings.ToLower(text)
	for _, p := range vaguePhrases {
		if strings.Contains(low, p) {
			return true
		}
	}
	return false
}

// QualityGate 对一条提取出的候选技能做质量门控，返回是否通过及改进建议。
// 纯函数（不依赖 DB / LLM），可独立单测；扫描器据此决定「拦截」还是「写入待审批」。
func QualityGate(name, description, body string) QualityResult {
	var notes []string

	if !ValidateCandidateName(name) {
		notes = append(notes, "技能名非法：仅允许字母数字下划线连字符且不能为空")
	}
	dlen := len([]rune(description))
	if dlen < MinDescriptionLen {
		notes = append(notes, "描述过短（少于最小长度），缺乏信息量")
	}
	if dlen > MaxDescriptionLen {
		notes = append(notes, "描述过长（超过最大长度）")
	}
	b := normalizeBody(body)
	blen := len([]rune(b))
	if blen < MinBodyLen {
		notes = append(notes, "正文过短（少于最小长度），视为空泛内容")
	}
	if blen > MaxBodyLen {
		notes = append(notes, "正文过长（超过最大长度），请收敛范围")
	}
	if b != "" && !hasStructure(b) {
		notes = append(notes, "正文缺乏基本结构（标题/步骤/列表），不具备可复用技能文档形态")
	}
	if containsVague(name+" "+description+" "+b) {
		notes = append(notes, "内容含占位/空泛短语（如 TODO/占位/待补充），视为草稿")
	}

	if len(notes) > 0 {
		return QualityResult{Passed: false, Notes: notes}
	}
	return QualityResult{Passed: true, Notes: nil}
}
