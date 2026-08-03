package taskrun

import (
	"context"
	"testing"

	"github.com/ayanmw/multiagent2/server/internal/sessionstore"
	taskrunruntime "trpc.group/trpc-go/trpc-agent-go/agent/taskrun"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

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
