package toolsearch

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"

	"github.com/ayanmw/multiagent2/server/internal/model"
)

// TestLoadMCPServerTools_Integration 是 M2-06 的「真实连接」集成测试：
// 此前 LoadMCPServerTools 仅覆盖 ValidateFail 路径，真实 stdio 服务器连接/列举/调用靠手工 verify。
// 本测试编译并启动同模块的 testmcp 服务器（与框架 e2e 同款做法），证明：
//  1. LoadMCPServerTools 能真正连接服务器并预取工具，注册进 Toolbox（命名空间 mcp__testmcp__demo_echo）；
//  2. 经 call_tool 控制工具穿透到真实 MCP 工具，拿到回显结果（端到端可达）。
//
// 这是「延迟工具箱」能力的核心验收：工具不经此路径就永远进不了 Agent 上下文。
func TestLoadMCPServerTools_Integration(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go 不可用，跳过 MCP 集成测试（环境限制，非缺陷）")
	}

	// 定位并预编译 testmcp 服务器为临时可执行文件（避免每次调用重新编译）。
	serverSrc, err := filepath.Abs(filepath.Join("testmcp", "main.go"))
	if err != nil {
		t.Fatalf("服务器源码路径错误: %v", err)
	}
	if _, err := os.Stat(serverSrc); err != nil {
		t.Fatalf("testmcp 服务器源码缺失: %v", err)
	}
	tmpDir := t.TempDir()
	exePath := filepath.Join(tmpDir, "testmcp"+exeSuffix())
	build := exec.Command("go", "build", "-o", exePath, serverSrc)
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, berr := build.CombinedOutput(); berr != nil {
		t.Fatalf("编译 testmcp 服务器失败: %v\n%s", berr, out)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	m := model.MCPServer{
		Name:      "testmcp",
		Transport: model.MCPTransportStdio,
		Command:   exePath,
	}
	box, err := LoadMCPServerTools(ctx, m)
	if err != nil {
		t.Fatalf("LoadMCPServerTools 连接真实 stdio 服务器失败: %v", err)
	}
	defer box.Close()

	// 1) 工具被真正列举并注册进 toolbox，命名空间正确。
	const toolName = "mcp__testmcp__demo_echo"
	if _, ok := box.Get(toolName); !ok {
		t.Fatalf("toolbox 未包含 %q，实际条目: %#v", toolName, box.List())
	}

	// 2) 经 call_tool 控制工具穿透到真实 MCP 工具，验证端到端可达。
	call := NewCallTool(box)
	ct, ok := call.(tool.CallableTool)
	if !ok {
		t.Fatalf("call_tool 必须实现 CallableTool")
	}
	args := `{"name":"` + toolName + `","arguments":"{\"message\":\"hello-integration\"}"}`
	res, err := ct.Call(ctx, []byte(args))
	if err != nil {
		t.Fatalf("call_tool 调用真实 MCP 工具失败: %v", err)
	}
	got := resultToString(res)
	if !strings.Contains(got, "hello-integration") {
		t.Fatalf("call_tool 未拿到真实回显结果，got=%q", got)
	}
	if !strings.Contains(got, "echo:") {
		t.Fatalf("call_tool 结果格式异常，got=%q", got)
	}
}

// exeSuffix 返回当前平台的二进制后缀（Windows 为 .exe）。
func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}
