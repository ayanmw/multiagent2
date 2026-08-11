package codectool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"

	"github.com/ayanmw/multiagent2/server/internal/executor"
)

// newTestExecutor 构造一个经危险命令策略包装的执行器（无人值守 + 内存审计），供工具测试复用。
func newTestExecutor(t *testing.T, workdir string) (executor.Executor, *executor.MemoryAuditor) {
	t.Helper()
	host, err := executor.NewHostExecutor(workdir)
	if err != nil {
		t.Fatalf("NewHostExecutor(%s): %v", workdir, err)
	}
	aud := executor.NewMemoryAuditor()
	ex := executor.NewSafeExecutor(
		host,
		executor.NewDangerousCommandPolicy(executor.ModeUnattended),
		aud,
		nil,
		nil,
	)
	return ex, aud
}

// callTool 通过框架 CallableTool.Call 直接驱动工具（与 Agent 调用路径一致），
// 输入/输出均为 JSON 字符串，便于断言。
func callTool(t *testing.T, tools []tool.Tool, name, inputJSON string) string {
	t.Helper()
	var ct tool.CallableTool
	for _, tl := range tools {
		if tl.Declaration().Name == name {
			c, ok := tl.(tool.CallableTool)
			if !ok {
				t.Fatalf("工具 %s 不可调用", name)
			}
			ct = c
			break
		}
	}
	if ct == nil {
		t.Fatalf("未找到工具 %s", name)
	}
	out, err := ct.Call(context.Background(), []byte(inputJSON))
	if err != nil {
		return "ERROR: " + err.Error()
	}
	if s, ok := out.(string); ok {
		return s
	}
	b, _ := json.Marshal(out)
	return string(b)
}

func TestShellExec_RunsCommand(t *testing.T) {
	dir := t.TempDir()
	ex, _ := newTestExecutor(t, dir)
	tools := CodeActTools(dir, ex)

	out := callTool(t, tools, "shell_exec", `{"command":"echo hello-m1"}`)
	if !strings.Contains(out, "hello-m1") {
		t.Fatalf("shell_exec 未返回命令输出: %q", out)
	}
	if !strings.Contains(out, "exit_code: 0") {
		t.Fatalf("shell_exec 未报告正确退出码: %q", out)
	}
	t.Logf("✅ shell_exec 正常执行: %q", out)
}

func TestShellExec_DangerousCommandDenied(t *testing.T) {
	dir := t.TempDir()
	ex, aud := newTestExecutor(t, dir)
	tools := CodeActTools(dir, ex)

	out := callTool(t, tools, "shell_exec", `{"command":"rm -rf /"}`)
	if !strings.Contains(out, "拒绝") {
		t.Fatalf("危险命令未被拒绝: %q", out)
	}
	// 审计应记录一条 denied 条目。
	if len(aud.All()) == 0 {
		t.Fatal("危险命令未写审计记录")
	}
	if aud.All()[0].Allowed {
		t.Fatal("被拒命令的审计 Allowed 应为 false")
	}
	t.Logf("✅ 危险命令被拒并审计: %q", out)
}

func TestFileWriteReadEdit(t *testing.T) {
	dir := t.TempDir()
	ex, _ := newTestExecutor(t, dir)
	tools := CodeActTools(dir, ex)

	// 写
	writeOut := callTool(t, tools, "file_write", `{"path":"src/main.go","content":"package main\n"}`)
	if !strings.Contains(writeOut, "已写入") {
		t.Fatalf("file_write 未成功: %q", writeOut)
	}
	// 读
	readOut := callTool(t, tools, "file_read", `{"path":"src/main.go"}`)
	if !strings.Contains(readOut, "package main") {
		t.Fatalf("file_read 内容不符: %q", readOut)
	}
	// 改
	editOut := callTool(t, tools, "file_edit",
		`{"path":"src/main.go","old_string":"package main","new_string":"package app","expected_replacements":1}`)
	if !strings.Contains(editOut, "替换 1 处") {
		t.Fatalf("file_edit 未成功: %q", editOut)
	}
	// 改后再读
	readOut2 := callTool(t, tools, "file_read", `{"path":"src/main.go"}`)
	if !strings.Contains(readOut2, "package app") {
		t.Fatalf("file_edit 后内容未变: %q", readOut2)
	}
	// 确认文件确实落盘（不依赖工具返回值）
	got, err := os.ReadFile(filepath.Join(dir, "src", "main.go"))
	if err != nil {
		t.Fatalf("读取落盘文件失败: %v", err)
	}
	if !strings.Contains(string(got), "package app") {
		t.Fatalf("落盘内容不符: %q", string(got))
	}
	t.Logf("✅ file_write/read/edit 全链路通过")
}

func TestFileRead_TraversalBlocked(t *testing.T) {
	dir := t.TempDir()
	ex, _ := newTestExecutor(t, dir)
	tools := CodeActTools(dir, ex)

	// 试图越出工作目录读到仓库根（用绝对路径指向父级）。
	target := filepath.Join(dir, "..", "escape.txt")
	_ = os.WriteFile(target, []byte("secret"), 0o644)
	out := callTool(t, tools, "file_read", `{"path":"../escape.txt"}`)
	if !strings.Contains(out, "ERROR") || !strings.Contains(out, "越界") {
		t.Fatalf("路径越界未被拦截: %q", out)
	}
	t.Logf("✅ 路径越界被拦截: %q", out)
}

func TestNewCodeAct_WorkdirMissing(t *testing.T) {
	if _, err := NewCodeAct("", nil, nil, executor.ModeUnattended); err == nil {
		t.Fatal("空 workdir 应报错")
	}
}
