package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ayanmw/multiagent2/server/internal/middleware"
	"github.com/gin-gonic/gin"
	taskrunruntime "trpc.group/trpc-go/trpc-agent-go/agent/taskrun"
)

// fakeRunController 是 taskrunruntime.Controller 的内存桩，用于管控 API 单测。
type fakeRunController struct {
	runs map[string]*taskrunruntime.Run
}

func newFakeRunController() *fakeRunController {
	return &fakeRunController{runs: map[string]*taskrunruntime.Run{
		"run-1": {ID: "run-1", OwnerUserID: "42", Status: taskrunruntime.StatusCompleted},
		"run-2": {ID: "run-2", OwnerUserID: "99", Status: taskrunruntime.StatusRunning},
	}}
}

func (f *fakeRunController) Spawn(_ context.Context, _ taskrunruntime.SpawnRequest) (taskrunruntime.Run, error) {
	return taskrunruntime.Run{}, nil
}

func (f *fakeRunController) List(_ context.Context, filter taskrunruntime.ListFilter) ([]taskrunruntime.Run, error) {
	out := []taskrunruntime.Run{}
	for _, r := range f.runs {
		if filter.OwnerUserID != "" && r.OwnerUserID != filter.OwnerUserID {
			continue
		}
		out = append(out, *r)
	}
	return out, nil
}

func (f *fakeRunController) Get(_ context.Context, id string) (*taskrunruntime.Run, error) {
	r, ok := f.runs[id]
	if !ok {
		return nil, taskrunruntime.ErrRunNotFound
	}
	return r, nil
}

func (f *fakeRunController) Cancel(_ context.Context, id string) (*taskrunruntime.Run, bool, error) {
	r, ok := f.runs[id]
	if !ok {
		return nil, false, taskrunruntime.ErrRunNotFound
	}
	r.Status = taskrunruntime.StatusCanceled
	return r, true, nil
}

func (f *fakeRunController) Wait(_ context.Context, id string) (*taskrunruntime.Run, error) {
	return f.Get(context.Background(), id)
}

// ctxWithUser 构造带用户身份的 gin 测试上下文。
func ctxWithUser(uid uint) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set(middleware.CtxUserID, uid)
	return c, w
}

func TestListTaskRunsHandler_OwnerIsolation(t *testing.T) {
	fc := newFakeRunController()
	c, w := ctxWithUser(42)
	c.Request, _ = http.NewRequest(http.MethodGet, "/api/taskruns", nil)
	ListTaskRunsHandler(fc)(c)
	if w.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "run-1") || strings.Contains(body, "run-2") {
		t.Fatalf("owner=42 应只看到 run-1，实际: %s", body)
	}
}

func TestGetTaskRunHandler_ForbiddenForOtherOwner(t *testing.T) {
	fc := newFakeRunController()
	c, w := ctxWithUser(42)
	c.Request, _ = http.NewRequest(http.MethodGet, "/api/taskruns/run-2", nil)
	c.Params = gin.Params{{Key: "id", Value: "run-2"}}
	GetTaskRunHandler(fc)(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("owner=42 访问 run-2 应 403，实际 %d", w.Code)
	}
}

func TestGetTaskRunHandler_OwnerOK(t *testing.T) {
	fc := newFakeRunController()
	c, w := ctxWithUser(42)
	c.Request, _ = http.NewRequest(http.MethodGet, "/api/taskruns/run-1", nil)
	c.Params = gin.Params{{Key: "id", Value: "run-1"}}
	GetTaskRunHandler(fc)(c)
	if w.Code != http.StatusOK {
		t.Fatalf("owner=42 访问 run-1 应 200，实际 %d", w.Code)
	}
}

func TestCancelTaskRunHandler_OwnerOK(t *testing.T) {
	fc := newFakeRunController()
	c, w := ctxWithUser(42)
	c.Request, _ = http.NewRequest(http.MethodPost, "/api/taskruns/run-1/cancel", nil)
	c.Params = gin.Params{{Key: "id", Value: "run-1"}}
	CancelTaskRunHandler(fc)(c)
	if w.Code != http.StatusOK {
		t.Fatalf("owner=42 取消 run-1 应 200，实际 %d", w.Code)
	}
	if fc.runs["run-1"].Status != taskrunruntime.StatusCanceled {
		t.Fatal("取消后状态应为 canceled")
	}
}
