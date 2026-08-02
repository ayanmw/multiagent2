package api

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"trpc.group/trpc-go/trpc-agent-go/tool"

	codectool "github.com/anmingwei/go-multi-agent-v2/internal/tool"
)

// userWorkspaceDir 返回某用户在 WorkspaceRoot 下的专属工作目录（<root>/<uid>）。
func userWorkspaceDir(workspaceRoot string, uid uint) string {
	return filepath.Join(workspaceRoot, strconv.Itoa(int(uid)))
}

// ensureWorkdir 解析并创建本次对话的受限工作目录。
// wsLocalDir 非空时用它（对话绑定了某个 workspace，见 M1-07）；
// 为空则回退到该用户的默认目录（WorkspaceRoot/<uid>）。
func ensureWorkdir(workspaceRoot string, uid uint, wsLocalDir string) (string, error) {
	workdir := wsLocalDir
	if workdir == "" {
		workdir = userWorkspaceDir(workspaceRoot, uid)
	}
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		return "", fmt.Errorf("创建工作目录失败: %w", err)
	}
	return workdir, nil
}

// buildCodeActTools 为给定用户构造一组经危险命令策略包装的 CodeAct 工具（M1-06）。
// 目录解析规则见 ensureWorkdir；目录自动创建，工具不可越界到目录之外。
// 返回的 []tool.Tool 可直接追加进 engine.ModelConfig.Tools。
//
// 注意（M1-08）：子代理委托模式下不再由本函数装配工具——CodeAct 工具集
// 由 codeagent 工厂装配给 Coder 子代理，Orchestrator 自身不持有写工具。
func buildCodeActTools(workspaceRoot string, uid uint, wsLocalDir string) ([]tool.Tool, error) {
	workdir, err := ensureWorkdir(workspaceRoot, uid, wsLocalDir)
	if err != nil {
		return nil, err
	}
	return codectool.NewCodeAct(workdir)
}
