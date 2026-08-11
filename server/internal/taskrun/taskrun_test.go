package taskrun

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ayanmw/multiagent2/server/internal/executor"
	"github.com/ayanmw/multiagent2/server/internal/sessionstore"
	codectool "github.com/ayanmw/multiagent2/server/internal/tool"
	"github.com/ayanmw/multiagent2/server/internal/worktree"
	taskrunruntime "trpc.group/trpc-go/trpc-agent-go/agent/taskrun"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// initRepo 初始化一个含初始提交的主仓库（worktree add 必须有可检出的提交）。
func initRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# main\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	ex, err := codectool.NewGitExecutor(dir, nil, nil, executor.ModeUnattended)
	if err != nil {
		t.Fatalf("git executor: %v", err)
	}
	ctx := context.Background()
	if _, err := codectool.GitInit(ctx, ex); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if _, err := codectool.GitCommit(ctx, ex, "init"); err != nil {
		t.Fatalf("git commit: %v", err)
	}
}

// fakeController 是 taskrunruntime.Controller 的最小桩，仅用于验证 Tools() 装配。
type fakeController struct{}

func (fakeController) Spawn(context.Context, taskrunruntime.SpawnRequest) (taskrunruntime.Run, error) {
	return taskrunruntime.Run{}, nil
}
func (fakeController) List(context.Context, taskrunruntime.ListFilter) ([]taskrunruntime.Run, error) {
	return nil, nil
}
func (fakeController) Get(context.Context, string) (*taskrunruntime.Run, error) { return nil, nil }
func (fakeController) Cancel(context.Context, string) (*taskrunruntime.Run, bool, error) {
	return nil, false, nil
}
func (fakeController) Wait(context.Context, string) (*taskrunruntime.Run, error) { return nil, nil }

func hasTool(tools []tool.Tool, name string) bool {
	for _, tl := range tools {
		if tl.Declaration().Name == name {
			return true
		}
	}
	return false
}

// TestTools_WithoutSessionService 验证：未提供 session service 时只有 5 个工具，
// 且不包含 read_task_run_transcript（框架契约）。
func TestTools_WithoutSessionService(t *testing.T) {
	tools := Tools(fakeController{}, nil, "coder")
	if len(tools) != 5 {
		t.Fatalf("无 session service 时应有 5 个工具，实际 %d", len(tools))
	}
	if hasTool(tools, "read_task_run_transcript") {
		t.Fatal("无 session service 时不应包含 read_task_run_transcript")
	}
}

// TestTools_WithSessionService 验证：提供 session service 时共 6 个工具，
// 且包含 read_task_run_transcript（M2-04 ① 持久化 transcript 的关键）。
func TestTools_WithSessionService(t *testing.T) {
	// sessionstore.New(nil) 退化为内存实现，足以作为接口桩。
	svc := sessionstore.New(nil)
	tools := Tools(fakeController{}, svc, "coder")
	if len(tools) != 6 {
		t.Fatalf("有 session service 时应有 6 个工具，实际 %d", len(tools))
	}
	if !hasTool(tools, "read_task_run_transcript") {
		t.Fatal("有 session service 时必须包含 read_task_run_transcript")
	}
	if !hasTool(tools, "start_task_run") {
		t.Fatal("必须包含 start_task_run")
	}
}

// TestWorktreeHook_CreateAndFinalize 验证 M2-05 钩子胶水：
// Create 创建隔离 worktree；OnRunUpdate(completed) 触发 merge 回主分支 + 清理。
func TestWorktreeHook_CreateAndFinalize(t *testing.T) {
	base := t.TempDir()
	repoDir := filepath.Join(base, "repo")
	initRepo(t, repoDir)

	hook := &WorktreeHook{Enabled: true, Manager: worktree.NewManager()}
	ctx := context.Background()

	wt, err := hook.Create(ctx, repoDir, "taskrun:sess-hook:1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if wt == "" || strings.Contains(wt, repoDir) {
		t.Fatalf("worktree 必须在主仓库外，实际 %s", wt)
	}
	// 在 worktree 内提交改动。
	if err := os.WriteFile(filepath.Join(wt, "hook.txt"), []byte("ok\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	wtEx, _ := codectool.NewGitExecutor(wt, nil, nil, executor.ModeUnattended)
	if _, err := codectool.GitCommit(ctx, wtEx, "add hook.txt"); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// 非终态不应触发 merge/清理（entry 仍在）。
	hook.OnRunUpdate(ctx, taskrunruntime.Run{ChildSessionID: "taskrun:sess-hook:1", Status: taskrunruntime.StatusRunning})
	if _, err := os.Stat(filepath.Join(repoDir, "hook.txt")); !os.IsNotExist(err) {
		t.Fatal("running 状态不应 merge")
	}

	// 终态 completed → 应 merge 回主分支并清理分支。
	hook.OnRunUpdate(ctx, taskrunruntime.Run{ChildSessionID: "taskrun:sess-hook:1", Status: taskrunruntime.StatusCompleted})
	if _, err := os.Stat(filepath.Join(repoDir, "hook.txt")); err != nil {
		t.Fatalf("completed 后主仓库缺少 hook.txt: %v", err)
	}
	ex, _ := codectool.NewGitExecutor(repoDir, nil, nil, executor.ModeUnattended)
	branches, _ := codectool.GitBranch(ctx, ex, "", false)
	if strings.Contains(branches, "taskrun/") {
		t.Fatalf("临时分支未清理: %s", branches)
	}
}

// TestWorktreeHook_DisabledIsNoop 验证关闭隔离时钩子为空操作（向后兼容 M2-04）。
func TestWorktreeHook_DisabledIsNoop(t *testing.T) {
	hook := &WorktreeHook{Enabled: false, Manager: worktree.NewManager()}
	if _, err := hook.Create(context.Background(), "/tmp/x", "sess"); err != nil {
		t.Fatalf("disabled Create 应无错误，实际 %v", err)
	}
	// 不应 panic，也不应触发任何 git 操作。
	hook.OnRunUpdate(context.Background(), taskrunruntime.Run{ChildSessionID: "sess", Status: taskrunruntime.StatusCompleted})
}
