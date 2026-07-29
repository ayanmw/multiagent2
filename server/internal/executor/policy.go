package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"time"
)

// Policy 危险命令策略接口：对命令给出 allow/ask/deny 判定与原因。
// 业务可注入自定义实现（如基于白名单、基于 LLM 风险评分）。
type Policy interface {
	Evaluate(command string) (Decision, string)
}

// ErrCommandDenied 是命令被危险命令策略拒绝时的哨兵错误。
var ErrCommandDenied = errors.New("executor: 命令被危险命令策略拒绝")

// AuditEntry 是一条命令审计记录，用于事后追溯被放行/拒绝的执行。
type AuditEntry struct {
	Timestamp time.Time
	Command   string
	Workdir   string
	Decision  Decision // 策略初步判定
	Reason    string   // 命中原因
	Allowed   bool     // 最终是否真正执行
	Note      string   // 补充信息（如交互模式确认来源）
}

// Auditor 记录命令的审计条目（落盘/日志/内存由实现决定）。
type Auditor interface {
	Record(entry AuditEntry)
}

// MemoryAuditor 把审计条目收集到内存切片，便于测试与内省。
type MemoryAuditor struct {
	mu      sync.Mutex
	Entries []AuditEntry
}

// NewMemoryAuditor 构造一个内存审计器。
func NewMemoryAuditor() *MemoryAuditor { return &MemoryAuditor{} }

// Record 追加一条审计记录。
func (a *MemoryAuditor) Record(e AuditEntry) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Entries = append(a.Entries, e)
}

// All 返回当前所有审计记录的副本。
func (a *MemoryAuditor) All() []AuditEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]AuditEntry, len(a.Entries))
	copy(out, a.Entries)
	return out
}

// LogAuditor 把审计条目写到 io.Writer（生产默认用 log.Default()）。
type LogAuditor struct {
	w io.Writer
}

// NewLogAuditor 构造日志审计器；w 为 nil 时回落到标准日志输出。
func NewLogAuditor(w io.Writer) *LogAuditor {
	if w == nil {
		w = log.Default().Writer()
	}
	return &LogAuditor{w: w}
}

// Record 把审计条目格式化为一行日志写出。
func (a *LogAuditor) Record(e AuditEntry) {
	decision := e.Decision.String()
	if !e.Allowed {
		decision = "denied"
	}
	fmt.Fprintf(a.w, "[AUDIT %s] command=%q workdir=%q decision=%s reason=%q\n",
		e.Timestamp.Format(time.RFC3339), e.Command, e.Workdir, decision, e.Reason)
}

// discardAuditor 是空操作审计器，仅在未显式提供 Auditor 时使用。
type discardAuditor struct{}

func (discardAuditor) Record(AuditEntry) {}

// AskHandler 在交互模式下对 ask 类命令做出裁决，返回是否放行。
// 返回 false 表示拒绝执行。
type AskHandler func(command string, reason string) bool

// SafeExecutor 在底层 Executor 之上叠加危险命令策略（M1-05）。
// 它实现 Executor 接口，业务层（CodeAct 工具、子代理）可无缝替换底层执行器。
// 设计原则：危险命令策略是包装层，不修改 Executor 接口本身，便于后续替换执行后端。
type SafeExecutor struct {
	inner   Executor
	policy  Policy
	auditor Auditor
	ask     AskHandler // 交互模式下的确认回调（可为 nil）
}

// NewSafeExecutor 构造策略执行器。
// policy 为 nil 时等价于放行一切（不推荐用于生产）；
// auditor 为 nil 时使用空操作审计器（仍记录但无副作用）；
// ask 为交互模式的确认回调，无人值守场景可传 nil。
func NewSafeExecutor(inner Executor, policy Policy, auditor Auditor, ask AskHandler) *SafeExecutor {
	if auditor == nil {
		auditor = discardAuditor{}
	}
	return &SafeExecutor{inner: inner, policy: policy, auditor: auditor, ask: ask}
}

// Run 先经策略评估：allow 直接执行并审计放行；deny 拒绝并审计；
// ask 在交互模式交回调裁决、无人值守（无回调）直接拒绝并审计。
func (s *SafeExecutor) Run(ctx context.Context, command string) (*Result, error) {
	if s.policy == nil {
		// 无策略：放行（调用方明确知情，仅用于测试/受信任环境）。
		return s.inner.Run(ctx, command)
	}
	decision, reason := s.policy.Evaluate(command)
	switch decision {
	case DecisionAllow:
		s.auditor.Record(AuditEntry{
			Timestamp: time.Now(), Command: command, Workdir: s.inner.Workdir(),
			Decision: DecisionAllow, Reason: "策略放行", Allowed: true,
		})
		return s.inner.Run(ctx, command)
	case DecisionDeny:
		s.auditor.Record(AuditEntry{
			Timestamp: time.Now(), Command: command, Workdir: s.inner.Workdir(),
			Decision: DecisionDeny, Reason: reason, Allowed: false,
		})
		return nil, fmt.Errorf("%w: %s", ErrCommandDenied, reason)
	case DecisionAsk:
		allow := s.ask != nil && s.ask(command, reason)
		s.auditor.Record(AuditEntry{
			Timestamp: time.Now(), Command: command, Workdir: s.inner.Workdir(),
			Decision: DecisionAsk, Reason: reason, Allowed: allow, Note: "交互模式确认",
		})
		if !allow {
			return nil, fmt.Errorf("%w: %s", ErrCommandDenied, reason)
		}
		return s.inner.Run(ctx, command)
	default:
		return s.inner.Run(ctx, command)
	}
}

// Workdir 返回底层执行器的工作目录。
func (s *SafeExecutor) Workdir() string { return s.inner.Workdir() }
