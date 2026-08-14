package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/ayanmw/multiagent2/server/internal/middleware"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/gin-gonic/gin"
)

// makeSessionCtx 构造带鉴权上下文与 :id 路径参数的测试 gin.Context。
func makeSessionCtx(t *testing.T, uid uint, sessionKey string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set(middleware.CtxUserID, uid)
	c.Params = gin.Params{{Key: "id", Value: sessionKey}}
	return c, w
}

func jsonBody(t *testing.T, v interface{}) *bytes.Buffer {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return bytes.NewBuffer(b)
}

func decodeSessionView(t *testing.T, w *httptest.ResponseRecorder) sessionView {
	t.Helper()
	var sv sessionView
	if err := json.Unmarshal(w.Body.Bytes(), &sv); err != nil {
		t.Fatalf("解析 sessionView 失败: %v (body=%s)", err, w.Body.String())
	}
	return sv
}

// TestBindWorkspaceHandler_BindAndExpose 验收 MX-01 后端：绑定 workspace 后，
// 会话列表/详情均暴露 workspace_key（刷新后保留），且可解除绑定；跨用户绑定返回 404。
func TestBindWorkspaceHandler_BindAndExpose(t *testing.T) {
	db := newAPITestDB(t)
	uid := uint(7)
	wsKey := "ws-bind"
	ws := &model.Workspace{
		UserID:    uid,
		Key:       wsKey,
		Name:      "n",
		LocalPath: filepath.Join(t.TempDir(), "wd"),
		Status:    model.WorkspaceStatusActive,
	}
	if err := repo.CreateWorkspace(db.DB, ws); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	sess := &model.Session{UserID: uid, SessionKey: "sess-bind", Title: "t"}
	if err := db.DB.Create(sess).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}

	// 绑定。
	c, w := makeSessionCtx(t, uid, "sess-bind")
	c.Request, _ = http.NewRequest(http.MethodPatch, "/api/sessions/sess-bind/workspace",
		jsonBody(t, map[string]*string{"workspace_key": &wsKey}))
	BindWorkspaceHandler(db.DB)(c)
	if w.Code != http.StatusOK {
		t.Fatalf("bind 期望 200, 实际 %d body=%s", w.Code, w.Body.String())
	}
	sv := decodeSessionView(t, w)
	if sv.WorkspaceKey == nil || *sv.WorkspaceKey != wsKey {
		t.Fatalf("bind 响应未返回 workspace_key, got %v", sv.WorkspaceKey)
	}

	// GET 详情应暴露绑定（刷新后保留）。
	c2, w2 := makeSessionCtx(t, uid, "sess-bind")
	c2.Request, _ = http.NewRequest(http.MethodGet, "/api/sessions/sess-bind", nil)
	GetSessionHandler(db.DB)(c2)
	if w2.Code != http.StatusOK {
		t.Fatalf("get 期望 200, 实际 %d", w2.Code)
	}
	var detail sessionDetailView
	if err := json.Unmarshal(w2.Body.Bytes(), &detail); err != nil {
		t.Fatalf("解析 detail 失败: %v", err)
	}
	if detail.WorkspaceKey == nil || *detail.WorkspaceKey != wsKey {
		t.Fatalf("detail 未暴露绑定 workspace_key, got %v", detail.WorkspaceKey)
	}

	// 解除绑定。
	c3, w3 := makeSessionCtx(t, uid, "sess-bind")
	c3.Request, _ = http.NewRequest(http.MethodPatch, "/api/sessions/sess-bind/workspace",
		jsonBody(t, map[string]*string{"workspace_key": nil}))
	BindWorkspaceHandler(db.DB)(c3)
	if w3.Code != http.StatusOK {
		t.Fatalf("unbind 期望 200, 实际 %d body=%s", w3.Code, w3.Body.String())
	}
	sv3 := decodeSessionView(t, w3)
	if sv3.WorkspaceKey != nil {
		t.Fatalf("unbind 后 workspace_key 应为 nil, got %v", sv3.WorkspaceKey)
	}

	// 跨用户绑定应 404（越权）。
	c4, w4 := makeSessionCtx(t, 999, "sess-bind")
	c4.Request, _ = http.NewRequest(http.MethodPatch, "/api/sessions/sess-bind/workspace",
		jsonBody(t, map[string]*string{"workspace_key": &wsKey}))
	BindWorkspaceHandler(db.DB)(c4)
	if w4.Code != http.StatusNotFound {
		t.Fatalf("越权绑定应 404, 实际 %d", w4.Code)
	}
}

// TestCreateSessionHandler_NoWorkspace 验收新建会话默认不绑定 workspace（workspace_key 为 null）。
func TestCreateSessionHandler_NoWorkspace(t *testing.T) {
	db := newAPITestDB(t)
	uid := uint(11)
	c, w := makeSessionCtx(t, uid, "")
	c.Request, _ = http.NewRequest(http.MethodPost, "/api/sessions", jsonBody(t, map[string]string{"title": "新对话"}))
	CreateSessionHandler(db.DB)(c)
	if w.Code != http.StatusCreated {
		t.Fatalf("create 期望 201, 实际 %d body=%s", w.Code, w.Body.String())
	}
	sv := decodeSessionView(t, w)
	if sv.WorkspaceKey != nil {
		t.Fatalf("新建会话 workspace_key 应为 nil, got %v", sv.WorkspaceKey)
	}
}
