package worktree

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ayanmw/multiagent2/server/internal/executor"
	codectool "github.com/ayanmw/multiagent2/server/internal/tool"
)

// skipUnlessGit 在 git 或 host executor 不可用时跳过依赖 git 的隔离测试，
// 使 `go test` 在缺少 git 的最小/本地环境也能默认通过；CI（已安装 git）则真正执行核心隔离测试。
func skipUnlessGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git 不可用，跳过（CI 应安装 git 以运行核心隔离测试）")
	}
	if _, err := executor.NewHostExecutor("."); err != nil {
		t.Skip("executor 不可用，跳过")
	}
}

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
	// 显式设置本地 git 身份：CI/全新环境无全局配置，git commit 会因缺 user.name/user.email 失败
	//（与 internal/tool/git_test.go 同款处理）。worktree 共享主仓库 config，此处设置即可覆盖全部 commit。
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "ci@test.local"},
		{"config", "user.name", "ci-tester"},
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
	skipUnlessGit(t)
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
	wtEx, err := codectool.NewGitExecutor(wt, nil, nil, executor.ModeUnattended)
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
	// 内容比对前统一行尾：Windows 上 git 若开启 core.autocrlf=true，checkout 会把 LF 还原成 CRLF，
	// 这属于 git 的工作区换行归一化（用户级配置），与 merge 是否成功无关。
	if got := strings.ReplaceAll(readFile(t, filepath.Join(repoDir, "hello.txt")), "\r\n", "\n"); got != "from worktree\n" {
		t.Fatalf("merge 内容不对: %q", got)
	}

	// 临时分支应已被删除。
	ex, _ := codectool.NewGitExecutor(repoDir, nil, nil, executor.ModeUnattended)
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
	skipUnlessGit(t)
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
	wtEx, _ := codectool.NewGitExecutor(wt, nil, nil, executor.ModeUnattended)
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
	ex, _ := codectool.NewGitExecutor(repoDir, nil, nil, executor.ModeUnattended)
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

// TestManager_ConcurrentFinalize 验证 M7.5-03 压测暴露的并发缺陷回归：
// 多个后台任务同时收敛时，Observer 并发触发 Finalize → 多个 git merge --no-ff
// 同时写同一主仓库的 index/HEAD 互相踩踏（index.lock/HEAD 竞态，多数 merge 报
// exit_code=128，子任务产物丢失）。修复：Manager.mergeMu 串行化 merge 段。
// 本测试并发 Finalize 6 个 worktree，断言全部 merge 成功、产物完整、无泄漏。
func TestManager_ConcurrentFinalize(t *testing.T) {
	skipUnlessGit(t)
	base := t.TempDir()
	repoDir := filepath.Join(base, "repo")
	initRepo(t, repoDir)

	m := NewManager()
	ctx := context.Background()
	const n = 6

	// 创建 n 个 worktree，各自写独立文件并 commit（模拟 n 个后台子任务并发产出）。
	for i := 1; i <= n; i++ {
		key := fmt.Sprintf("taskrun:conc:%d", i)
		wt, err := m.Create(ctx, repoDir, key)
		if err != nil {
			t.Fatalf("Create(%d): %v", i, err)
		}
		if err := os.WriteFile(filepath.Join(wt, fmt.Sprintf("task-%d.txt", i)), []byte(fmt.Sprintf("DONE-%d\n", i)), 0o644); err != nil {
			t.Fatalf("write(%d): %v", i, err)
		}
		wtEx, err := codectool.NewGitExecutor(wt, nil, nil, executor.ModeUnattended)
		if err != nil {
			t.Fatalf("wt executor(%d): %v", i, err)
		}
		if _, err := codectool.GitCommit(ctx, wtEx, fmt.Sprintf("add task-%d", i)); err != nil {
			t.Fatalf("commit(%d): %v", i, err)
		}
	}

	// 并发 Finalize（模拟多 run 同时进入终态触发 Observer）。
	var wg sync.WaitGroup
	outs := make([]string, n)
	for i := 1; i <= n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			outs[idx-1] = m.Finalize(ctx, fmt.Sprintf("taskrun:conc:%d", idx), "completed")
		}(i)
	}
	wg.Wait()

	// 断言 1：主分支必须出现全部 n 个文件且内容正确（任何 merge 踩踏都会丢文件）。
	for i := 1; i <= n; i++ {
		got, err := os.ReadFile(filepath.Join(repoDir, fmt.Sprintf("task-%d.txt", i)))
		if err != nil {
			t.Fatalf("主分支缺 task-%d.txt（并发 merge 丢产物）：%v", i, err)
		}
		if want := fmt.Sprintf("DONE-%d", i); strings.TrimSpace(string(got)) != want {
			t.Fatalf("task-%d.txt 内容不符: got=%q want=%q", i, strings.TrimSpace(string(got)), want)
		}
	}
	// 断言 2：worktree 检出目录与临时分支全部清理（Finalize 收尾完整）。
	ex, _ := codectool.NewGitExecutor(repoDir, nil, nil, executor.ModeUnattended)
	branches, err := runGit(ctx, ex, "-C", repoDir, "branch", "--list")
	if err != nil {
		t.Fatalf("list branches: %v", err)
	}
	if strings.Contains(branches, "taskrun/") {
		t.Fatalf("并发 Finalize 后临时分支未清理: %s", branches)
	}
	wtParent := filepath.Join(base, ".taskrun-worktrees")
	if entries, derr := os.ReadDir(wtParent); derr == nil && len(entries) > 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("并发 Finalize 后 worktree 目录未清理: %v", names)
	}
	_ = outs
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
