package codectool

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedReadOnlyWorkspace 在临时目录里铺一份小型「代码库」，供 grep / 只读工具测试使用。
func seedReadOnlyWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"main.go":                 "package main\n\n// TODO: 补充错误处理\nfunc main() {}\n",
		"pkg/util.go":             "package pkg\n\nfunc Helper() string { return \"ok\" }\n",
		"README.md":               "# demo\n\ntodo: 写文档\n",
		".git/config":             "[core]\n\tTODO = should-be-skipped\n",
		"node_modules/dep/dep.js": "// TODO: 依赖里的内容不应被检索\n",
	}
	for rel, content := range files {
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("MkdirAll %s: %v", abs, err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", abs, err)
		}
	}
	return dir
}

// TestReadOnlyTools_OnlyReadAndGrep 验证 M1-10 的核心约束：
// 只读工具集恰好是 file_read + grep，绝不包含任何写/执行工具。
func TestReadOnlyTools_OnlyReadAndGrep(t *testing.T) {
	dir := seedReadOnlyWorkspace(t)
	tools, err := ReadOnlyTools(dir)
	if err != nil {
		t.Fatalf("ReadOnlyTools: %v", err)
	}
	got := make([]string, 0, len(tools))
	for _, tl := range tools {
		got = append(got, tl.Declaration().Name)
	}
	want := ReadOnlyToolNames()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("只读工具集不符：got=%v want=%v", got, want)
	}
	for _, forbidden := range []string{ToolFileWrite, ToolFileEdit, ToolShellExec} {
		for _, name := range got {
			if name == forbidden {
				t.Fatalf("只读工具集中出现写/执行工具 %q（全部工具=%v）", forbidden, got)
			}
		}
	}
	// 只读工具集必须通过兜底断言。
	if err := EnsureReadOnly(tools); err != nil {
		t.Fatalf("EnsureReadOnly 误报: %v", err)
	}
}

// TestEnsureReadOnly_RejectsMutatingTool 验证兜底断言能拦住「漏进来的写工具」：
// 一旦有人把 CodeAct 全量工具集当成只读集使用，构造期就会失败。
func TestEnsureReadOnly_RejectsMutatingTool(t *testing.T) {
	dir := t.TempDir()
	all, err := NewCodeAct(dir)
	if err != nil {
		t.Fatalf("NewCodeAct: %v", err)
	}
	err = EnsureReadOnly(all)
	if err == nil {
		t.Fatal("EnsureReadOnly 应拒绝含写/执行工具的集合")
	}
	if !errors.Is(err, ErrMutatingTool) {
		t.Fatalf("错误类型不符：%v", err)
	}
	if !strings.Contains(err.Error(), ToolShellExec) &&
		!strings.Contains(err.Error(), ToolFileWrite) &&
		!strings.Contains(err.Error(), ToolFileEdit) {
		t.Fatalf("错误信息未指出具体工具：%v", err)
	}
}

// TestReadOnlyTools_ReviewerCannotWrite 验证 Reviewer 语义下的「调 write 被拒」：
// 只读工具集中不存在 file_write / file_edit / shell_exec，任何写入尝试都无从发起，
// 且读取路径仍受工作目录边界约束。
func TestReadOnlyTools_ReviewerCannotWrite(t *testing.T) {
	dir := seedReadOnlyWorkspace(t)
	tools, err := ReadOnlyTools(dir)
	if err != nil {
		t.Fatalf("ReadOnlyTools: %v", err)
	}
	for _, name := range []string{ToolFileWrite, ToolFileEdit, ToolShellExec} {
		for _, tl := range tools {
			if tl.Declaration().Name == name {
				t.Fatalf("只读工具集中意外存在 %s", name)
			}
		}
	}
	// 只读工具可正常读取。
	out := callTool(t, tools, ToolFileRead, `{"path":"main.go"}`)
	if !strings.Contains(out, "package main") {
		t.Fatalf("file_read 未返回内容：%q", out)
	}
	// 越界读取仍被拒绝（继承 resolveSafePath 约束）。
	out = callTool(t, tools, ToolFileRead, `{"path":"../outside.txt"}`)
	if !strings.HasPrefix(out, "ERROR:") || !strings.Contains(out, "越界") {
		t.Fatalf("越界读取未被拒绝：%q", out)
	}
}

func TestGrep_MatchesAcrossDirectory(t *testing.T) {
	dir := seedReadOnlyWorkspace(t)

	out, err := Grep(dir, GrepOptions{Pattern: "TODO"})
	if err != nil {
		t.Fatalf("Grep: %v", err)
	}
	if !strings.Contains(out, "main.go:3:") {
		t.Fatalf("未命中 main.go 的 TODO：%q", out)
	}
	// .git / node_modules 必须被跳过。
	if strings.Contains(out, "node_modules") || strings.Contains(out, ".git") {
		t.Fatalf("检索未跳过依赖/版本库目录：%q", out)
	}
}

func TestGrep_IgnoreCaseAndScopedPath(t *testing.T) {
	dir := seedReadOnlyWorkspace(t)

	// 大小写敏感时 README.md 里的 "todo" 不应命中 "TODO"。
	out, err := Grep(dir, GrepOptions{Pattern: "TODO", Path: "README.md"})
	if err != nil {
		t.Fatalf("Grep: %v", err)
	}
	if !strings.Contains(out, "未找到匹配") {
		t.Fatalf("大小写敏感检索不应命中：%q", out)
	}
	// 忽略大小写后命中。
	out, err = Grep(dir, GrepOptions{Pattern: "TODO", Path: "README.md", IgnoreCase: true})
	if err != nil {
		t.Fatalf("Grep(ignore_case): %v", err)
	}
	if !strings.Contains(out, "README.md:3:") {
		t.Fatalf("忽略大小写检索未命中：%q", out)
	}
	// 限定子目录：只应出现 pkg 下的结果。
	out, err = Grep(dir, GrepOptions{Pattern: "func", Path: "pkg"})
	if err != nil {
		t.Fatalf("Grep(scoped): %v", err)
	}
	if !strings.Contains(out, "pkg/util.go") || strings.Contains(out, "main.go") {
		t.Fatalf("限定目录检索范围不正确：%q", out)
	}
}

func TestGrep_PathTraversalRejected(t *testing.T) {
	dir := seedReadOnlyWorkspace(t)
	if _, err := Grep(dir, GrepOptions{Pattern: "x", Path: "../"}); err == nil {
		t.Fatal("越界检索应被拒绝")
	}
}

func TestGrep_InvalidInput(t *testing.T) {
	dir := seedReadOnlyWorkspace(t)
	if _, err := Grep(dir, GrepOptions{Pattern: ""}); err == nil {
		t.Fatal("空 pattern 应报错")
	}
	if _, err := Grep(dir, GrepOptions{Pattern: "("}); err == nil {
		t.Fatal("非法正则应报错")
	}
	if _, err := Grep("", GrepOptions{Pattern: "x"}); err == nil {
		t.Fatal("空 workdir 应报错")
	}
}

func TestGrep_MaxResultsTruncates(t *testing.T) {
	dir := t.TempDir()
	var sb strings.Builder
	for i := 0; i < 50; i++ {
		sb.WriteString("hit line\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "many.txt"), []byte(sb.String()), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	out, err := Grep(dir, GrepOptions{Pattern: "hit", MaxResults: 5})
	if err != nil {
		t.Fatalf("Grep: %v", err)
	}
	if n := strings.Count(out, "many.txt:"); n != 5 {
		t.Fatalf("max_results 未生效：命中 %d 行\n%s", n, out)
	}
}

// TestGrep_ViaToolCall 走真实的框架工具调用路径（JSON 入参 → CallableTool.Call）。
func TestGrep_ViaToolCall(t *testing.T) {
	dir := seedReadOnlyWorkspace(t)
	tools, err := ReadOnlyTools(dir)
	if err != nil {
		t.Fatalf("ReadOnlyTools: %v", err)
	}
	out := callTool(t, tools, ToolGrep, `{"pattern":"Helper","path":"pkg"}`)
	if !strings.Contains(out, "pkg/util.go") {
		t.Fatalf("grep 工具调用未返回预期结果：%q", out)
	}
}

func TestReadOnlyTools_InvalidWorkdir(t *testing.T) {
	if _, err := ReadOnlyTools(""); err == nil {
		t.Fatal("空 workdir 应报错")
	}
	if _, err := ReadOnlyTools(filepath.Join(t.TempDir(), "not-exist")); err == nil {
		t.Fatal("不存在的 workdir 应报错")
	}
}
