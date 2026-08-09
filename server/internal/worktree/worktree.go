// Package worktree 为 taskrun 后台子任务提供「git worktree 隔离」（M2-05）。
//
// 设计目标（对齐 docs/loop/PLAN.md M2-05 验收标准）：
//   - 每个后台子任务（taskrun）在其独立 worktree（独立分支 taskrun/<name>）内执行，
//     子代理的 Executor 工作目录指向该 worktree，绝不直接写主分支工作区；
//   - 子任务完成后，把隔离分支 merge 回主仓库（仅本地 merge，绝不 push 远程）；
//   - 冲突保留分支交人工/Reviewer 处理，不自动推送、不强行覆盖；
//   - worktree 目录与临时分支在 merge 成功后清理（失败/取消时仅移除检出目录、保留分支供复核）。
//
// 所有 git 操作经 codectool 的 SafeExecutor（无人值守默认 deny），遵守
// 「代码执行唯一出口」约定（见 LEARNINGS.md）。
//
// 关联键：worktree 在子代理工厂（BuildAgentFactory）中按 child session id 创建并登记，
// 在 inprocess.Observer（OnRunUpdate，子任务终态）中按同一 child session id 取回并 merge/清理。
package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ayanmw/multiagent2/server/internal/executor"
	codectool "github.com/ayanmw/multiagent2/server/internal/tool"
)

// Manager 跟踪并管理每次 taskrun 的 worktree 隔离信息。
// 进程内的 sync.Map 提供「工厂创建 ↔ Observer 终态」的关联；
// 跨进程重启的孤立 worktree 由 .taskrun-worktrees 目录 + git 元数据保留，人工可清理。
type Manager struct {
	mu      sync.Mutex
	entries map[string]*Entry // key = childSessionID（与 Observer 的 run.ChildSessionID 一致）
}

// Entry 记录一次 taskrun 的 worktree 隔离信息。
type Entry struct {
	RepoDir     string // 主仓库目录（workspace 本地目录，必须已是 git 仓库）
	WorktreeDir string // worktree 检出目录（位于 RepoDir 之外）
	Branch      string // 隔离分支名 taskrun/<name>
}

// NewManager 构造一个 worktree 管理器。
func NewManager() *Manager {
	return &Manager{entries: make(map[string]*Entry)}
}

// sanitizeName 把任意标识（childSessionID / runID）规整为安全目录与分支名：
// 仅保留 [a-zA-Z0-9_-]，其余字符统一替换为 _，并裁掉首尾 _。
// git 分支名与目录名均不允许 : / \ 等字符，故必须规整。
func sanitizeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		out = "x"
	}
	return out
}

// Create 为主仓库 repoDir 创建一个隔离 worktree：
//   - 在 repoDir 的同级目录 <Dir(repoDir)>/.taskrun-worktrees/<name> 检出新分支 taskrun/<name>；
//   - 返回 worktree 目录，供子代理作为执行工作目录（与 main 工作区完全隔离）。
//
// childSessionID 用于关联（与 Observer 取回用的 run.ChildSessionID 一致）。
// 若 repoDir 非 git 仓库 / 无提交 / 创建失败，返回空串与错误（调用方回退到主目录，不阻断任务）。
func (m *Manager) Create(ctx context.Context, repoDir, childSessionID string) (string, error) {
	if repoDir == "" {
		return "", fmt.Errorf("worktree: repoDir 不能为空")
	}
	name := sanitizeName(childSessionID)
	branch := "taskrun/" + name
	parent := filepath.Join(filepath.Dir(repoDir), ".taskrun-worktrees")
	worktreeDir := filepath.Join(parent, name)

	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", fmt.Errorf("worktree: 创建 worktree 父目录失败: %w", err)
	}

	ex, err := codectool.NewGitExecutor(repoDir, nil)
	if err != nil {
		return "", err
	}
	// git -C <repoDir> worktree add <worktreeDir> -b <branch>
	if _, err := runGit(ctx, ex, "-C", repoDir, "worktree", "add", worktreeDir, "-b", branch); err != nil {
		return "", err
	}

	m.mu.Lock()
	if m.entries == nil {
		m.entries = make(map[string]*Entry)
	}
	m.entries[childSessionID] = &Entry{RepoDir: repoDir, WorktreeDir: worktreeDir, Branch: branch}
	m.mu.Unlock()
	return worktreeDir, nil
}

// Finalize 在 taskrun 终态时调用：
//   - 成功（completed）：把隔离分支 merge --no-ff 回主分支；merge 成功则清理 worktree + 临时分支；
//     若 merge 冲突，保留分支与 worktree 检出（仅清理脏数据），交人工/Reviewer 处理，绝不 push 远程。
//   - 失败/取消：不 merge 半成品，仅 `git worktree remove --force` 移除检出目录并 prune，
//     保留临时分支供人工复核（可 `git worktree add` 重新检出或直接 cherry-pick）。
//
// 返回人类可读的处理说明（用于 run 元数据 / 日志）；无关联 entry 时返回空串。
func (m *Manager) Finalize(ctx context.Context, childSessionID, status string) string {
	m.mu.Lock()
	entry, ok := m.entries[childSessionID]
	if !ok {
		m.mu.Unlock()
		return ""
	}
	delete(m.entries, childSessionID)
	m.mu.Unlock()

	ex, err := codectool.NewGitExecutor(entry.RepoDir, nil)
	if err != nil {
		return fmt.Sprintf("worktree: 构造执行器失败: %v", err)
	}
	var sb strings.Builder

	if status == string(terminalStatusCompleted) {
		out, merr := runGit(ctx, ex, "-C", entry.RepoDir, "merge", "--no-ff",
			"-m", "merge taskrun branch "+entry.Branch, entry.Branch)
		sb.WriteString("merge: " + strings.TrimSpace(out))
		if merr != nil {
			// 合并冲突：保留分支与 worktree，交人工/Reviewer，不删除、不推送远程。
			sb.WriteString(" (冲突，保留分支待人工处理)")
			_, _ = runGit(ctx, ex, "-C", entry.RepoDir, "worktree", "remove", entry.WorktreeDir, "--force")
			_, _ = runGit(ctx, ex, "-C", entry.RepoDir, "worktree", "prune")
			return sb.String()
		}
	}

	// 先移除 worktree 检出目录（解锁被检出的临时分支），再删分支：
	// 若先删分支会因「分支正被 worktree 检出」而失败。
	// 成功 merge 后删除临时分支；失败/取消则保留分支供复核，仅移除 worktree 检出。
	_, _ = runGit(ctx, ex, "-C", entry.RepoDir, "worktree", "remove", entry.WorktreeDir, "--force")
	_, _ = runGit(ctx, ex, "-C", entry.RepoDir, "worktree", "prune")
	if status == string(terminalStatusCompleted) {
		// merge 成功后才删除临时分支（此时 worktree 已移除，分支不再被检出）。
		_, _ = runGit(ctx, ex, "-C", entry.RepoDir, "branch", "-D", entry.Branch)
	}
	_, _ = runGit(ctx, ex, "-C", entry.RepoDir, "gc", "--auto")
	return sb.String()
}

// terminalStatusCompleted 与框架 taskrun.StatusCompleted 对齐（避免 worktree 包直接依赖框架）。
const terminalStatusCompleted = "completed"

// runGit 经 SafeExecutor 以 argv 形式直调 git（不经 shell，规避 Windows 引号转义），
// 非零退出码视为错误并附带输出（供 merge 冲突等场景识别）。
func runGit(ctx context.Context, ex executor.Executor, args ...string) (string, error) {
	res, err := ex.RunCommand(ctx, "git", args...)
	if err != nil {
		if errors.Is(err, executor.ErrCommandDenied) {
			return "", fmt.Errorf("worktree: 命令被安全策略拒绝: %v", err)
		}
		return "", err
	}
	msg := strings.TrimSpace(res.Stdout + "\n" + res.Stderr)
	if res.ExitCode != 0 {
		return msg, fmt.Errorf("worktree: git 返回非零退出码 %d", res.ExitCode)
	}
	return msg, nil
}
