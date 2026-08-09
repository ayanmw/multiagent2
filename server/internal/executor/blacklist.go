package executor

import "strings"

// Mode 表示执行器的运行模式，决定 ask 类命令如何被处置。
type Mode int

const (
	// ModeUnattended 无人值守：命中 ask 策略的命令按 deny 处置（默认安全）。
	ModeUnattended Mode = iota
	// ModeInteractive 交互模式：命中 ask 策略的命令交由 AskHandler 回调裁决。
	ModeInteractive
)

// Decision 是策略对一条命令的判定。
type Decision int

const (
	// DecisionAllow 放行。
	DecisionAllow Decision = iota
	// DecisionAsk 需要人工确认（无人值守时降级为 deny）。
	DecisionAsk
	// DecisionDeny 直接拒绝。
	DecisionDeny
)

func (d Decision) String() string {
	switch d {
	case DecisionAllow:
		return "allow"
	case DecisionAsk:
		return "ask"
	case DecisionDeny:
		return "deny"
	default:
		return "unknown"
	}
}

// Rule 是黑名单中的一条匹配规则。
type Rule struct {
	// Match 是归一化命令中需要包含的片段（不区分大小写、空白已折叠）。
	Match string
	// Decision 命中后的初步判定（allow/ask/deny）。
	Decision Decision
	// Reason 命中原因，用于审计与错误信息。
	Reason string
}

// normalizeCommand 把命令转小写、去首尾空白、折叠内部连续空白，便于稳定匹配。
// 例如 "   RM   -rf    /  " → "rm -rf /"，使 "sudo rm -rf /" 也能命中 "rm -rf /"。
func normalizeCommand(cmd string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(cmd))), " ")
}

// DangerousCommandPolicy 是基于片段黑名单的默认危险命令策略（M1-05）。
// 规则按严重度分为 deny（致命，始终拒绝）与 ask（高风险，需确认）两类；
// ask 在无人值守模式下降级为 deny。
type DangerousCommandPolicy struct {
	rules []Rule
	mode  Mode
}

// DefaultDangerousRules 返回一组开箱即用的危险命令规则。
// 顺序无关紧要——Evaluate 内部先扫描全部 deny 再扫描全部 ask，保证最严重判定优先。
func DefaultDangerousRules() []Rule {
	return []Rule{
		// —— 致命级（始终 deny）——
		{Match: "rm -rf /", Decision: DecisionDeny, Reason: "递归强制删除根目录"},
		{Match: "rm -rf /*", Decision: DecisionDeny, Reason: "递归强制删除根目录下全部"},
		{Match: "rm -fr /", Decision: DecisionDeny, Reason: "递归强制删除根目录(参数顺序变体)"},
		{Match: "rm -rf ~", Decision: DecisionDeny, Reason: "递归强制删除用户主目录"},
		{Match: "rm -fr ~", Decision: DecisionDeny, Reason: "递归强制删除用户主目录(参数顺序变体)"},
		{Match: ":(){", Decision: DecisionDeny, Reason: "fork 炸弹（:(){ :|:& };:）"},
		{Match: "mkfs", Decision: DecisionDeny, Reason: "格式化文件系统"},
		{Match: "shutdown", Decision: DecisionDeny, Reason: "关机/重启系统"},
		{Match: "reboot", Decision: DecisionDeny, Reason: "重启系统"},
		{Match: "halt", Decision: DecisionDeny, Reason: "关机系统"},
		{Match: "> /dev/sda", Decision: DecisionDeny, Reason: "向原始磁盘设备写入"},
		{Match: "of=/dev/sda", Decision: DecisionDeny, Reason: "向原始磁盘设备写入(dd)"},
		{Match: "dd if=/dev/zero", Decision: DecisionDeny, Reason: "向磁盘清零(dd)"},

		// —— 高风险级（ask，无人值守降级 deny）——
		{Match: "rm -rf", Decision: DecisionAsk, Reason: "递归强制删除（可能误删重要文件）"},
		{Match: "rm -fr", Decision: DecisionAsk, Reason: "递归强制删除（可能误删重要文件）"},
		{Match: "git push --force", Decision: DecisionAsk, Reason: "强制推送（覆盖远端历史）"},
		{Match: "git push -f", Decision: DecisionAsk, Reason: "强制推送（覆盖远端历史）"},
		{Match: "git reset --hard", Decision: DecisionAsk, Reason: "硬重置（丢弃本地修改）"},
		{Match: "git clean -f", Decision: DecisionAsk, Reason: "强制清理未跟踪文件"},
		{Match: "git checkout --", Decision: DecisionAsk, Reason: "丢弃工作区修改"},
		{Match: "chmod -r 000", Decision: DecisionAsk, Reason: "递归剥夺全部权限"},
	}
}

// NewDangerousCommandPolicy 构造一个默认危险命令策略。
// mode 控制 ask 类命令在无人值守场景下的处置（建议生产用 ModeUnattended）。
func NewDangerousCommandPolicy(mode Mode) *DangerousCommandPolicy {
	return &DangerousCommandPolicy{
		rules: DefaultDangerousRules(),
		mode:  mode,
	}
}

// Evaluate 按规则判定命令：致命规则优先返回 deny；否则命中 ask 规则时，
// 无人值守模式返回 deny，交互模式返回 ask。均未命中返回 allow。
func (p *DangerousCommandPolicy) Evaluate(command string) (Decision, string) {
	norm := normalizeCommand(command)
	// 两遍扫描：先 deny（致命）再 ask（高风险），保证最严重判定优先。
	for _, r := range p.rules {
		if r.Decision == DecisionDeny && strings.Contains(norm, r.Match) {
			return DecisionDeny, r.Reason
		}
	}
	for _, r := range p.rules {
		if r.Decision == DecisionAsk && strings.Contains(norm, r.Match) {
			// ask 规则在所有模式下都返回 DecisionAsk；无人值守场景下的具体处置
			// （deny / 生成人工检查点并暂停）由 SafeExecutor 依据是否挂了 checkpointer 决定（M3-05）。
			return DecisionAsk, r.Reason
		}
	}
	return DecisionAllow, ""
}
