package worktree

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ayanmw/multiagent2/server/internal/executor"
	codectool "github.com/ayanmw/multiagent2/server/internal/tool"
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
	ex, err := codectool.NewGitExecutor(dir)
	if err != nil {
		t.Fatalf("git executor: %v", err)
	}
	ctx := context.Background()
	for _, args := range [][]string{
		{"init"},
		{"add", "-A"},
		{"commit", "-m", "init"},
	} {
		if _, err := runGit(ctx, ex, append([]string{"-C", dir}, args...)...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	// 清理初始提交产生的分支引用快照，确保后续 worktree 干净。
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// TestManager_CreateAndMerge 验证核心验收：子任务在 worktree 提交 → 终态 merge 回主分支 → 主分支含改动 → 临时分支已清理。
func TestManager_CreateAndMerge(t *testing.T) {
	if _, err := executor.NewHostExecutor("."); err != nil {
		t.Skip("executor 不可用，跳过")
	}
	base := t.TempDir()
	repoDir := filepath.Join(base, "repo")
	initRepo(t, repoDir)

	m := NewManager()
	ctx := context.Background()

	wt, err := m.Create(ctx, repoDir, "taskrun:sess-abc:123")
	if err != nil {
		t.Fatalf("Create worktree: %v", err)
	}
	if wt == "" {
		t.Fatal("worktree 目录不应为空")
	}
	if strings.Contains(wt, repoDir) {
		t.Fatalf("worktree 必须在主仓库之外，实际 %s", wt)
	}

	// 在 worktree 内写入并 commit（模拟子代理落盘）。
	if err := os.WriteFile(filepath.Join(wt, "hello.txt"), []byte("from worktree\n"), 0o644); err != nil {
		t.Fatalf("write in worktree: %v", err)
	}
	wtEx, err := codectool.NewGitExecutor(wt)
	if err != nil {
		t.Fatalf("wt executor: %v", err)
	}
	if _, err := codectool.GitCommit(ctx, wtEx, "add hello"); err != nil {
		t.Fatalf("commit in worktree: %v", err)
	}

	// 终态：completed → 应 merge 回主分支并清理分支。
	out := m.Finalize(ctx, "taskrun:sess-abc:123", "completed")
	t.Logf("finalize output: %s", out)

	// 主分支现在应含有 hello.txt。
	if _, err := os.Stat(filepath.Join(repoDir, "hello.txt")); err != nil {
		t.Fatalf("merge 后主仓库缺少 hello.txt: %v", err)
	}
	if got := readFile(t, filepath.Join(repoDir, "hello.txt")); got != "from worktree\n" {
		t.Fatalf("merge 内容不对: %q", got)
	}

	// 临时分支应已被删除。
	ex, _ := codectool.NewGitExecutor(repoDir)
	branches, err := runGit(ctx, ex, "-C", repoDir, "branch", "--list")
	if err != nil {
		t.Fatalf("list branches: %v", err)
	}
	if strings.Contains(branches, "taskrun/") {
		t.Fatalf("临时分支未被清理: %s", branches)
	}

	// worktree 检出目录应已被移除。
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("worktree 目录未被清理: %v", err)
	}
}

// TestManager_FinalizeFailureKeepsBranch 验证：失败任务不 merge 半成品，但保留分支供复核，仅移除检出目录。
func TestManager_FinalizeFailureKeepsBranch(t *testing.T) {
	if _, err := executor.NewHostExecutor("."); err != nil {
		t.Skip("executor 不可用，跳过")
	}
	base := t.TempDir()
	repoDir := filepath.Join(base, "repo")
	initRepo(t, repoDir)

	m := NewManager()
	ctx := context.Background()

	wt, err := m.Create(ctx, repoDir, "taskrun:sess-fail:999")
	if err != nil {
		t.Fatalf("Create worktree: %v", err)
	}
	// 在 worktree 内提交半成品。
	if err := os.WriteFile(filepath.Join(wt, "half.txt"), []byte("partial\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	wtEx, _ := codectool.NewGitExecutor(wt)
	if _, err := codectool.GitCommit(ctx, wtEx, "partial work"); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// 终态：failed → 不 merge，保留分支。
	out := m.Finalize(ctx, "taskrun:sess-fail:999", "failed")
	t.Logf("finalize output: %s", out)

	// 主分支不应含有 half.txt（未 merge）。
	if _, err := os.Stat(filepath.Join(repoDir, "half.txt")); !os.IsNotExist(err) {
		t.Fatal("失败任务不应 merge 进主分支")
	}
	// 临时分支应保留供复核。
	ex, _ := codectool.NewGitExecutor(repoDir)
	branches, err := runGit(ctx, ex, "-C", repoDir, "branch", "--list")
	if err != nil {
		t.Fatalf("list branches: %v", err)
	}
	if !strings.Contains(branches, "taskrun/") {
		t.Fatalf("失败任务应保留分支供复核: %s", branches)
	}
	// 检出目录应被移除。
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatal("worktree 检出目录应被清理")
	}
	// 清理保留分支，避免干扰。
	_, _ = runGit(ctx, ex, "-C", repoDir, "branch", "-D", "taskrun/sess_fail_999")
}

// TestManager_UnknownSession 验证：终态时若无可关联 entry（重复 Finalize / 未知 id），安全返回空。
func TestManager_UnknownSession(t *testing.T) {
	m := NewManager()
	if got := m.Finalize(context.Background(), "nope", "completed"); got != "" {
		t.Fatalf("未知 session 应返回空，实际 %q", got)
	}
}

// TestSanitizeName 验证分支/目录名规整。
func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"taskrun:sess-abc:123": "taskrun_sess-abc_123",
		"a/b\\c d":             "a_b_c_d",
		"///":                  "x",
		"__ok__":               "ok",
	}
	for in, want := range cases {
		if got := sanitizeName(in); got != want {
			t.Fatalf("sanitizeName(%q)=%q, want %q", in, got, want)
		}
	}
}
