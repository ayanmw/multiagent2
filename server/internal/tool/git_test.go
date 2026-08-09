package codectool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// callGitTool 把入参 JSON 序列化后直驱工具（走真实 CallableTool.Call 路径，无需真实 LLM），
// 断言返回文本或 error。与 codeact_test.go 的驱动方式一致。
func callGitTool(t *testing.T, tl tool.Tool, in any) (any, error) {
	t.Helper()
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	ct, ok := tl.(tool.CallableTool)
	if !ok {
		t.Fatalf("tool %s 不是 CallableTool", tl.Declaration().Name)
	}
	return ct.Call(context.Background(), b)
}

func gitToolByName(t *testing.T, tools []tool.Tool) map[string]tool.Tool {
	t.Helper()
	m := map[string]tool.Tool{}
	for _, tl := range tools {
		m[tl.Declaration().Name] = tl
	}
	return m
}

// TestGitTools_FullChain 验证 M2-01 的 Git 工具链（不依赖真实 LLM，直接驱动工具）：
// 在临时目录 git init → 写文件 → git_commit 初始提交 → git_status 干净 / git_diff 为空 →
// 修改已跟踪文件 → git_status 显示改动 / git_diff 显示改动内容 → git_log 含提交说明 /
// git_branch 列出默认分支。同时证明「正常 git 子命令不被危险命令策略误伤」
// （commit/status/diff/log/branch 均正常执行，无高危子命令被拒）。
func TestGitTools_FullChain(t *testing.T) {
	workdir := t.TempDir()

	// 经 SafeExecutor 初始化仓库（模拟 workspace 自动 git init 的执行通道）。
	ex, err := NewGitExecutor(workdir, nil)
	if err != nil {
		t.Fatalf("NewGitExecutor: %v", err)
	}
	// 测试环境显式设置本地身份，使 git commit 不依赖机器全局配置。
	if _, err := ex.RunCommand(context.Background(), "git", "config", "user.email", "test@test.local"); err != nil {
		t.Fatalf("git config email: %v", err)
	}
	if _, err := ex.RunCommand(context.Background(), "git", "config", "user.name", "tester"); err != nil {
		t.Fatalf("git config name: %v", err)
	}
	if _, err := GitInit(context.Background(), ex); err != nil {
		t.Fatalf("GitInit: %v", err)
	}

	tools, err := NewGitTools(workdir, nil)
	if err != nil {
		t.Fatalf("NewGitTools: %v", err)
	}
	byName := gitToolByName(t, tools)

	// 五个 git 工具都应存在。
	for _, n := range GitToolNames() {
		if _, ok := byName[n]; !ok {
			t.Fatalf("NewGitTools 缺少工具 %s", n)
		}
	}

	// 初始仓库状态应干净。
	out, err := callGitTool(t, byName[ToolGitStatus], gitStatusInput{})
	if err != nil {
		t.Fatalf("git_status(初始): %v", err)
	}
	if strings.TrimSpace(out.(string)) != "" {
		t.Fatalf("初始 git_status 应为空（干净），got=%q", out)
	}

	// 写入一个文件并作为初始提交（使其成为已跟踪文件，供后续 git_diff 验证）。
	if err := os.WriteFile(filepath.Join(workdir, "hello.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}
	out, err = callGitTool(t, byName[ToolGitCommit], gitCommitInput{Message: "add hello.txt"})
	if err != nil {
		t.Fatalf("git_commit(初始): %v", err)
	}
	t.Logf("git_commit(初始) 输出: %q", out)

	// 初始提交后状态应干净、diff 应空。
	out, _ = callGitTool(t, byName[ToolGitStatus], gitStatusInput{})
	if strings.TrimSpace(out.(string)) != "" {
		t.Fatalf("初始提交后 git_status 应为空（干净），got=%q", out)
	}
	out, _ = callGitTool(t, byName[ToolGitDiff], gitDiffInput{})
	if strings.TrimSpace(out.(string)) != "" {
		t.Fatalf("初始提交后 git_diff 应为空，got=%q", out)
	}

	// 修改已跟踪文件的内容（git diff 仅显示已跟踪文件的改动，不显示未跟踪文件，
	// 故必须先把文件纳入版本控制再修改，diff 才能看到变化）。
	if err := os.WriteFile(filepath.Join(workdir, "hello.txt"), []byte("hello world\n"), 0o644); err != nil {
		t.Fatalf("修改文件失败: %v", err)
	}
	// 改动后 git_status 应显示该文件已修改（M 标记）。
	out, _ = callGitTool(t, byName[ToolGitStatus], gitStatusInput{})
	if !strings.Contains(out.(string), "hello.txt") {
		t.Fatalf("git_status 应显示已修改的 hello.txt，got=%q", out)
	}
	// git_diff 应显示改动内容（含新增的 hello world）。
	out, _ = callGitTool(t, byName[ToolGitDiff], gitDiffInput{})
	if !strings.Contains(out.(string), "hello") {
		t.Fatalf("git_diff 应显示 hello.txt 的改动，got=%q", out)
	}

	// git_log 应含本次提交说明。
	out, _ = callGitTool(t, byName[ToolGitLog], gitLogInput{Limit: 10})
	if !strings.Contains(out.(string), "add hello.txt") {
		t.Fatalf("git_log 应含提交说明 add hello.txt，got=%q", out)
	}

	// git_branch 应列出默认分支（main 或 master）。
	out, _ = callGitTool(t, byName[ToolGitBranch], gitBranchInput{})
	if !strings.Contains(out.(string), "main") && !strings.Contains(out.(string), "master") {
		t.Fatalf("git_branch 应含默认分支，got=%q", out)
	}

	// git_commit 缺 message 应报错（参数校验）。
	if _, err := callGitTool(t, byName[ToolGitCommit], gitCommitInput{Message: "  "}); err == nil {
		t.Fatalf("空 message 的 git_commit 应返回错误")
	}
}

// TestNewCodeActWithGit_ToolSet 验证单代理模式装配的工具集恰好为
// 4 个 CodeAct + 5 个 Git 工具（M2-01），无遗漏无重复。
func TestNewCodeActWithGit_ToolSet(t *testing.T) {
	workdir := t.TempDir()
	tools, err := NewCodeActWithGit(workdir, nil)
	if err != nil {
		t.Fatalf("NewCodeActWithGit: %v", err)
	}
	want := map[string]bool{
		ToolShellExec: true, ToolFileRead: true, ToolFileWrite: true, ToolFileEdit: true,
		ToolGitStatus: true, ToolGitDiff: true, ToolGitCommit: true, ToolGitLog: true, ToolGitBranch: true,
	}
	got := map[string]bool{}
	for _, tl := range tools {
		name := tl.Declaration().Name
		if got[name] {
			t.Fatalf("工具名重复: %s", name)
		}
		got[name] = true
	}
	for n := range want {
		if !got[n] {
			t.Fatalf("NewCodeActWithGit 缺少工具 %s", n)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("工具数量不符: got=%d want=%d", len(got), len(want))
	}
}

// TestNewGitTools_RequiresWorkdir 验证空工作目录被拒绝（与 NewCodeAct 行为一致）。
func TestNewGitTools_RequiresWorkdir(t *testing.T) {
	if _, err := NewGitTools("", nil); err == nil {
		t.Fatalf("空 workdir 应返回错误")
	}
}
