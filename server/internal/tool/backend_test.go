package codectool

import (
	"context"
	"errors"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"

	"github.com/ayanmw/multiagent2/server/internal/executor"
)

// TestNewCodeActWithBackend_DockerUnavailable 验收 M8-02：docker 后端装配的 CodeAct
// 工具集与 host 版结构一致（工具名齐全）；在本机无 docker 时，shell 工具真实调用
// 返回可读的 ErrDockerUnavailable 错误（而非静默失败）——提示运维改用 host 或装 docker。
func TestNewCodeActWithBackend_DockerUnavailable(t *testing.T) {
	dir := t.TempDir()
	all, err := NewCodeActWithBackend(dir, nil, nil, executor.ModeUnattended, executor.BackendDocker, executor.DockerOptions{}, 0)
	if err != nil {
		t.Fatalf("NewCodeActWithBackend(docker): %v（构造不应依赖 docker 可用）", err)
	}
	// 工具名齐全（与 host 版一致：shell_exec/file_read/file_write/file_edit）。
	names := map[string]bool{}
	for _, tl := range all {
		names[tl.Declaration().Name] = true
	}
	for _, want := range []string{ToolShellExec, ToolFileRead, ToolFileWrite, ToolFileEdit} {
		if !names[want] {
			t.Errorf("docker 后端工具集缺少 %s（got %v）", want, names)
		}
	}

	// shell_exec 真实调用：无 docker 环境 → 返回可读错误（含 ErrDockerUnavailable）。
	shell := findTool(t, all, ToolShellExec)
	_, err = shell.Call(context.Background(), []byte(`{"command":"echo hi"}`))
	if err == nil {
		t.Fatal("无 docker 环境时 shell_exec 应返回错误")
	}
	if !strings.Contains(err.Error(), "docker") && !errors.Is(err, executor.ErrDockerUnavailable) {
		t.Fatalf("错误应提示 docker 后端不可用：%v", err)
	}
}

// TestNewCodeActWithBackend_Host 验收 M8-02：host 后端行为与旧版 NewCodeAct 完全一致
// （工具可真实执行——host 无 docker 依赖）。
func TestNewCodeActWithBackend_Host(t *testing.T) {
	dir := t.TempDir()
	all, err := NewCodeActWithBackend(dir, nil, nil, executor.ModeUnattended, executor.BackendHost, executor.DockerOptions{}, 0)
	if err != nil {
		t.Fatalf("NewCodeActWithBackend(host): %v", err)
	}
	legacy, err := NewCodeAct(dir, nil, nil, executor.ModeUnattended)
	if err != nil {
		t.Fatalf("NewCodeAct: %v", err)
	}
	if len(all) != len(legacy) {
		t.Fatalf("host 后端工具数 = %d, want %d（与旧版一致）", len(all), len(legacy))
	}
	shell := findTool(t, all, ToolShellExec)
	out, err := shell.Call(context.Background(), []byte(`{"command":"echo host-ok"}`))
	if err != nil {
		t.Fatalf("host shell_exec: %v", err)
	}
	if !strings.Contains(out.(string), "host-ok") {
		t.Fatalf("host shell_exec 输出缺少 host-ok：%v", out)
	}
}

// TestNewGitToolsWithBackend_Docker 验收 M8-02：git 工具集在 docker 后端下装配正常，
// 无 docker 环境时 git_status 调用返回可读错误。
func TestNewGitToolsWithBackend_Docker(t *testing.T) {
	dir := t.TempDir()
	gitTools, err := NewGitToolsWithBackend(dir, nil, nil, executor.ModeUnattended, executor.BackendDocker, executor.DockerOptions{})
	if err != nil {
		t.Fatalf("NewGitToolsWithBackend(docker): %v", err)
	}
	names := map[string]bool{}
	for _, tl := range gitTools {
		names[tl.Declaration().Name] = true
	}
	for _, want := range GitToolNames() {
		if !names[want] {
			t.Errorf("docker 后端 git 工具集缺少 %s（got %v）", want, names)
		}
	}
}

func findTool(t *testing.T, tools []tool.Tool, name string) tool.CallableTool {
	t.Helper()
	for _, tl := range tools {
		if tl.Declaration().Name == name {
			ct, ok := tl.(tool.CallableTool)
			if !ok {
				t.Fatalf("工具 %s 未实现 CallableTool", name)
			}
			return ct
		}
	}
	t.Fatalf("未找到工具 %s", name)
	return nil
}
