package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/metrics"
	"github.com/ayanmw/multiagent2/server/internal/obslog"
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

// CheckpointRequest 描述一个「待人工审批的危险命令」上下文（M3-05 human-in-the-loop）。
// 当无人值守模式下命中 ask 策略且 SafeExecutor 挂有 Checkpointer 时，由执行层透传此结构
// 落库为一条 checkpoint 记录，使原本被直接 deny 的危险操作转为「生成检查点 + 暂停」。
type CheckpointRequest struct {
	Command   string // 触发检查点的原始命令
	Workdir   string // 命令预期执行的工作目录
	Reason    string // 命中 ask 策略的原因（来自 DangerousCommandPolicy）
	SessionID string // 归属的对话会话（可选，由调用方注入）
	Context   string // 触发上下文（如 agent 角色 / goal，可选）
}

// Checkpointer 在无人值守模式下把一条待审批危险命令落库为 checkpoint，返回其展示 ID（如 "CP-12"）。
// 返回 error 时 SafeExecutor 退化为直接 deny（安全默认）。
type Checkpointer func(req CheckpointRequest) (id string, err error)

// ErrCheckpointCreated 是「已生成人工检查点，等待审批」的哨兵错误（M3-05）。
var ErrCheckpointCreated = errors.New("executor: 已生成人工检查点，等待审批")

// CheckpointError 是 ErrCheckpointCreated 的带 ID 实现，便于工具层回显检查点编号。
type CheckpointError struct {
	ID     string // 展示 ID，如 "CP-12"
	Reason string // 命中原因
}

// Error 实现 error 接口。
func (e *CheckpointError) Error() string {
	return "已生成人工检查点 " + e.ID + "，等待管理员审批后再执行"
}

// Unwrap 使 errors.Is(err, ErrCheckpointCreated) 成立；注意它**不**解包到
// ErrCommandDenied，确保工具层能把「生成检查点」与「被安全策略拒绝」区分开（M3-05）。
func (e *CheckpointError) Unwrap() error { return ErrCheckpointCreated }

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
	inner        Executor
	policy       Policy
	auditor      Auditor
	ask          AskHandler   // 交互模式下的确认回调（可为 nil）
	checkpointer Checkpointer // 无人值守下 ask 的落库回调（可为 nil，nil 时退化 deny）
}

// NewSafeExecutor 构造策略执行器。
// policy 为 nil 时等价于放行一切（不推荐用于生产）；
// auditor 为 nil 时使用空操作审计器（仍记录但无副作用）；
// ask 为交互模式的确认回调，无人值守场景可传 nil；
// cp 为无人值守模式下 ask 命令的「人工检查点」落库回调（M3-05）：
// 传入后，无人值守下命中 ask 的命令将生成 checkpoint 并暂停，而非直接拒绝；nil 时回退旧行为（直接 deny）。
func NewSafeExecutor(inner Executor, policy Policy, auditor Auditor, ask AskHandler, cp Checkpointer) *SafeExecutor {
	if auditor == nil {
		auditor = discardAuditor{}
	}
	return &SafeExecutor{inner: inner, policy: policy, auditor: auditor, ask: ask, checkpointer: cp}
}

// Run 先经策略评估：allow 直接执行并审计放行；deny 拒绝并审计；
// ask 在交互模式交回调裁决、无人值守下若挂有 checkpointer 则生成检查点并暂停、
// 否则（无 checkpointer）直接拒绝并审计（安全默认）。
func (s *SafeExecutor) Run(ctx context.Context, command string) (res *Result, err error) {
	if s.policy == nil {
		// 无策略：放行（调用方明确知情，仅用于测试/受信任环境）。
		return s.inner.Run(ctx, command)
	}
	// M3-09：记录工具（代码执行）调用数与失败数（reason=allowed/denied/checkpoint/failed）。
	defer func() { metrics.RecordToolCall(ctx, toolCallReason(err), err) }()
	// M7-06：命令执行级 trace span（父 span 为 engine.llm_run / gateway.run）——
	// 命令、决策、退出码全部挂在同一 trace 下，日志按 trace_id 过滤即可
	// 「从一次对话下钻到具体哪条命令被拒/失败/生成检查点」。
	execCtx, end := obslog.StartSpan(ctx, "executor.run",
		"command", truncateCommand(command), "workdir", s.inner.Workdir())
	defer func() {
		if res != nil {
			end(err, "decision", toolCallReason(err), "exit_code", res.ExitCode)
		} else {
			end(err, "decision", toolCallReason(err))
		}
	}()
	decision, reason := s.policy.Evaluate(command)
	switch decision {
	case DecisionAllow:
		s.auditor.Record(AuditEntry{
			Timestamp: time.Now(), Command: command, Workdir: s.inner.Workdir(),
			Decision: DecisionAllow, Reason: "策略放行", Allowed: true,
		})
		res, err = s.inner.Run(execCtx, command)
		return
	case DecisionDeny:
		s.auditor.Record(AuditEntry{
			Timestamp: time.Now(), Command: command, Workdir: s.inner.Workdir(),
			Decision: DecisionDeny, Reason: reason, Allowed: false,
		})
		err = fmt.Errorf("%w: %s", ErrCommandDenied, reason)
		return
	case DecisionAsk:
		outcome, cpID := s.classifyAsk(command, reason)
		switch outcome {
		case askAllow:
			res, err = s.inner.Run(execCtx, command)
			return
		case askCheckpoint:
			err = &CheckpointError{ID: cpID, Reason: reason}
			return
		default:
			err = fmt.Errorf("%w: %s", ErrCommandDenied, reason)
			return
		}
	default:
		res, err = s.inner.Run(execCtx, command)
		return
	}
}

// askOutcome 是 ask 决策在 SafeExecutor 层的最终处置（M3-05）。
type askOutcome int

const (
	askDeny       askOutcome = iota // 拒绝（交互模式用户拒绝 / 无人值守无 checkpointer / 落库失败）
	askAllow                        // 放行（交互模式用户确认）
	askCheckpoint                   // 生成人工检查点并暂停（无人值守 + 挂有 checkpointer）
)

// classifyAsk 处理 ask 决策的审计与落库（副作用），返回处置结果。**不直接执行命令**——
// 执行由 Run / RunCommand 各自按入口形式完成，避免重复执行。
//  1. 交互模式（ask 回调非空）：交 AskHandler 裁决；
//  2. 无人值守 + 挂有 checkpointer：落库为 checkpoint，返回 askCheckpoint + 展示 ID；
//  3. 无人值守且无 checkpointer（或落库失败）：安全默认，返回 askDeny。
func (s *SafeExecutor) classifyAsk(command, reason string) (askOutcome, string) {
	if s.ask != nil {
		// 交互模式：交由 AskHandler 裁决。
		allow := s.ask(command, reason)
		s.auditor.Record(AuditEntry{
			Timestamp: time.Now(), Command: command, Workdir: s.inner.Workdir(),
			Decision: DecisionAsk, Reason: reason, Allowed: allow, Note: "交互模式确认",
		})
		if !allow {
			return askDeny, ""
		}
		return askAllow, ""
	}
	if s.checkpointer != nil {
		// 无人值守 + 挂有 checkpointer：生成 checkpoint 并暂停。
		id, cerr := s.checkpointer(CheckpointRequest{
			Command: command,
			Workdir: s.inner.Workdir(),
			Reason:  reason,
		})
		s.auditor.Record(AuditEntry{
			Timestamp: time.Now(), Command: command, Workdir: s.inner.Workdir(),
			Decision: DecisionAsk, Reason: reason, Allowed: false, Note: "checkpoint created id=" + id,
		})
		if cerr != nil {
			// 落库失败：安全退化 deny。
			return askDeny, ""
		}
		return askCheckpoint, id
	}
	// 无人值守且无 checkpointer：安全默认，直接 deny（与旧行为一致）。
	s.auditor.Record(AuditEntry{
		Timestamp: time.Now(), Command: command, Workdir: s.inner.Workdir(),
		Decision: DecisionAsk, Reason: reason, Allowed: false, Note: "无人值守无确认机制，按 deny 处置",
	})
	return askDeny, ""
}

// WorkCommand 以 argv 形式执行命令（透传给底层 Executor.RunCommand），
// 策略评估与审计逻辑与 Run 完全一致，仅把 argv 拼成命令字符串用于评估。
func (s *SafeExecutor) RunCommand(ctx context.Context, name string, args ...string) (res *Result, err error) {
	command := name
	if len(args) > 0 {
		command += " " + strings.Join(args, " ")
	}
	if s.policy == nil {
		// 无策略：放行（调用方明确知情，仅用于测试/受信任环境）。
		return s.inner.RunCommand(ctx, name, args...)
	}
	// M3-09：记录工具（代码执行）调用数与失败数（reason=allowed/denied/checkpoint/failed）。
	defer func() { metrics.RecordToolCall(ctx, toolCallReason(err), err) }()
	// M7-06：与 Run 一致的命令执行级 trace span（见 Run 注释）。
	execCtx, end := obslog.StartSpan(ctx, "executor.run",
		"command", truncateCommand(command), "workdir", s.inner.Workdir())
	defer func() {
		if res != nil {
			end(err, "decision", toolCallReason(err), "exit_code", res.ExitCode)
		} else {
			end(err, "decision", toolCallReason(err))
		}
	}()
	decision, reason := s.policy.Evaluate(command)
	switch decision {
	case DecisionAllow:
		s.auditor.Record(AuditEntry{
			Timestamp: time.Now(), Command: command, Workdir: s.inner.Workdir(),
			Decision: DecisionAllow, Reason: "策略放行", Allowed: true,
		})
		res, err = s.inner.RunCommand(execCtx, name, args...)
		return
	case DecisionDeny:
		s.auditor.Record(AuditEntry{
			Timestamp: time.Now(), Command: command, Workdir: s.inner.Workdir(),
			Decision: DecisionDeny, Reason: reason, Allowed: false,
		})
		err = fmt.Errorf("%w: %s", ErrCommandDenied, reason)
		return
	case DecisionAsk:
		// 与 Run 共用 ask 处置逻辑（交互确认 / 生成检查点 / 退化 deny），
		// 但按 argv 入口形式真正执行命令。
		outcome, cpID := s.classifyAsk(command, reason)
		switch outcome {
		case askAllow:
			res, err = s.inner.RunCommand(execCtx, name, args...)
			return
		case askCheckpoint:
			err = &CheckpointError{ID: cpID, Reason: reason}
			return
		default:
			err = fmt.Errorf("%w: %s", ErrCommandDenied, reason)
			return
		}
	default:
		res, err = s.inner.RunCommand(execCtx, name, args...)
		return
	}
}

// truncateCommand 截断过长的命令字符串（避免把整段脚本塞进日志/指标标签）。
func truncateCommand(s string) string {
	const maxLen = 300
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "...(truncated)"
}

// toolCallReason 把一次受策略保护的执行结果归类为可观测性指标的 reason 维度（M3-09）：
//   - allowed：正常完成（命令自身非零退出码不算失败，属有效结果）
//   - denied：被危险命令策略拒绝（ErrCommandDenied）
//   - checkpoint：转入人工检查点并暂停（ErrCheckpointCreated，M3-05）
//   - failed：其余真实失败（超时、底层执行错误等）
//
// 注意判定顺序：CheckpointError 只 Unwrap 到 ErrCheckpointCreated、不解到
// ErrCommandDenied（见 M3-05），故两者互斥，但仍先判 checkpoint 语义更清晰。
func toolCallReason(err error) string {
	switch {
	case err == nil:
		return "allowed"
	case errors.Is(err, ErrCheckpointCreated):
		return "checkpoint"
	case errors.Is(err, ErrCommandDenied):
		return "denied"
	default:
		return "failed"
	}
}

// Workdir 返回底层执行器的工作目录。
func (s *SafeExecutor) Workdir() string { return s.inner.Workdir() }
