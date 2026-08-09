package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ayanmw/multiagent2/server/internal/artifact"
	"github.com/ayanmw/multiagent2/server/internal/middleware"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// newArtifactAPITestDB 构造内存 SQLite（纯 Go，无需 CGO）并迁移 sessions 表，
// 供 Artifact 浏览器接口测试使用（owner 隔离依赖 repo.GetSessionByKey 查 sessions 表）。
func newArtifactAPITestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.Session{}); err != nil {
		t.Fatalf("migrate sessions: %v", err)
	}
	return db
}

// seedArtifactSession 落一条会话记录（owner 隔离校验用）。
func seedArtifactSession(t *testing.T, db *gorm.DB, uid uint, key string) {
	t.Helper()
	s := &model.Session{UserID: uid, SessionKey: key, Title: "artifact-test"}
	if err := db.Create(s).Error; err != nil {
		t.Fatalf("seed session %s: %v", key, err)
	}
}

// artifactListResp 是 GET /api/sessions/:id/artifacts 的响应契约。
type artifactListResp struct {
	SessionKey string `json:"session_key"`
	Enabled    bool   `json:"enabled"`
	Total      int    `json:"total"`
	Artifacts  []struct {
		Name       string `json:"name"`
		Size       int64  `json:"size"`
		ModifiedAt string `json:"modified_at"`
		IsState    bool   `json:"is_state"`
	} `json:"artifacts"`
}

// artifactContentResp 是 GET /api/sessions/:id/artifacts/:name 的响应契约。
type artifactContentResp struct {
	SessionKey string `json:"session_key"`
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	IsState    bool   `json:"is_state"`
	Binary     bool   `json:"binary"`
	Truncated  bool   `json:"truncated"`
	Content    string `json:"content"`
}

func callListArtifacts(t *testing.T, db *gorm.DB, store artifact.Store, enable bool, uid uint, key string) (int, artifactListResp) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set(middleware.CtxUserID, uid)
	c.Params = gin.Params{{Key: "id", Value: key}}
	c.Request, _ = http.NewRequest(http.MethodGet, "/api/sessions/"+key+"/artifacts", nil)
	ListSessionArtifactsHandler(db, store, enable)(c)
	var out artifactListResp
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("解析列表响应失败: %v (body=%s)", err, w.Body.String())
		}
	}
	return w.Code, out
}

func callGetArtifact(t *testing.T, db *gorm.DB, store artifact.Store, enable bool, uid uint, key, name, query string) (*httptest.ResponseRecorder, artifactContentResp) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set(middleware.CtxUserID, uid)
	c.Params = gin.Params{{Key: "id", Value: key}, {Key: "name", Value: name}}
	c.Request, _ = http.NewRequest(http.MethodGet, "/api/sessions/"+key+"/artifacts/"+name+query, nil)
	GetSessionArtifactHandler(db, store, enable)(c)
	var out artifactContentResp
	// 下载模式（?download=1）返回的是原始字节（非 JSON），不应尝试 JSON 反序列化。
	if w.Code == http.StatusOK && w.Body.Len() > 0 && w.Body.Bytes()[0] == '{' {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("解析内容响应失败: %v (body=%s)", err, w.Body.String())
		}
	}
	return w, out
}

// TestListArtifactsHandler_Disabled 验证未启用状态外置时返回 Enabled=false 且列表为空。
func TestListArtifactsHandler_Disabled(t *testing.T) {
	db := newArtifactAPITestDB(t)
	seedArtifactSession(t, db, 42, "sess_abc")
	store := artifact.NewMemoryStore()
	code, res := callListArtifacts(t, db, store, false, 42, "sess_abc")
	if code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d", code)
	}
	if res.Enabled {
		t.Fatal("未启用时 Enabled 应为 false")
	}
	if len(res.Artifacts) != 0 {
		t.Fatalf("未启用时 artifacts 应空，实际 %d", len(res.Artifacts))
	}
}

// TestListArtifactsHandler_WithContent 验证列表返回元信息，且核心状态文件排在前面。
func TestListArtifactsHandler_WithContent(t *testing.T) {
	db := newArtifactAPITestDB(t)
	seedArtifactSession(t, db, 42, "sess_abc")
	store := artifact.NewMemoryStore()
	scope := "sess:sess_abc"
	if err := store.Write(scope, "report.txt", "hello world"); err != nil {
		t.Fatal(err)
	}
	if err := store.Write(scope, artifact.PlanArtifact, "# Plan\n- step1"); err != nil {
		t.Fatal(err)
	}

	code, res := callListArtifacts(t, db, store, true, 42, "sess_abc")
	if code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d", code)
	}
	if !res.Enabled {
		t.Fatal("启用时 Enabled 应为 true")
	}
	if res.Total != 2 {
		t.Fatalf("期望 2 条，实际 %d", res.Total)
	}
	// 排序：第一个应是 PLAN.md（核心状态文件优先）。
	if res.Artifacts[0].Name != artifact.PlanArtifact {
		t.Fatalf("核心状态文件应排首位，实际 %s", res.Artifacts[0].Name)
	}
	if !res.Artifacts[0].IsState {
		t.Fatal("PLAN.md 的 is_state 应为 true")
	}
	if res.Artifacts[1].IsState {
		t.Fatal("report.txt 的 is_state 应为 false")
	}
}

// TestGetArtifactHandler_Inline 验证查看模式返回内容。
func TestGetArtifactHandler_Inline(t *testing.T) {
	db := newArtifactAPITestDB(t)
	seedArtifactSession(t, db, 42, "sess_abc")
	store := artifact.NewMemoryStore()
	scope := "sess:sess_abc"
	_ = store.Write(scope, artifact.PlanArtifact, "# Plan content")

	_, res := callGetArtifact(t, db, store, true, 42, "sess_abc", artifact.PlanArtifact, "")
	if res.Name != artifact.PlanArtifact {
		t.Fatalf("name 回显异常: %s", res.Name)
	}
	if !res.IsState {
		t.Fatal("is_state 应为 true")
	}
	if res.Binary {
		t.Fatal("纯文本不应标 binary")
	}
	if res.Truncated {
		t.Fatal("短内容不应 truncated")
	}
	if res.Content != "# Plan content" {
		t.Fatalf("content 异常: %q", res.Content)
	}
}

// TestGetArtifactHandler_Download 验证 ?download=1 以附件形式返回原始字节。
func TestGetArtifactHandler_Download(t *testing.T) {
	db := newArtifactAPITestDB(t)
	seedArtifactSession(t, db, 42, "sess_abc")
	store := artifact.NewMemoryStore()
	scope := "sess:sess_abc"
	payload := "raw-bytes-content"
	_ = store.Write(scope, "report.txt", payload)

	w, _ := callGetArtifact(t, db, store, true, 42, "sess_abc", "report.txt", "?download=1")
	if w.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("下载响应应带 Content-Disposition: attachment，实际 %q", w.Header().Get("Content-Disposition"))
	}
	if w.Body.String() != payload {
		t.Fatalf("下载内容应等于原始字节，实际 %q", w.Body.String())
	}
}

// TestGetArtifactHandler_IllegalName 验证非法文件名返回 400。
func TestGetArtifactHandler_IllegalName(t *testing.T) {
	db := newArtifactAPITestDB(t)
	seedArtifactSession(t, db, 42, "sess_abc")
	store := artifact.NewMemoryStore()
	w, _ := callGetArtifact(t, db, store, true, 42, "sess_abc", "../escape.txt", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("非法文件名应 400，实际 %d", w.Code)
	}
}

// TestGetArtifactHandler_NotFound 验证不存在的文件返回 404。
func TestGetArtifactHandler_NotFound(t *testing.T) {
	db := newArtifactAPITestDB(t)
	seedArtifactSession(t, db, 42, "sess_abc")
	store := artifact.NewMemoryStore()
	w, _ := callGetArtifact(t, db, store, true, 42, "sess_abc", "nope.txt", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("不存在应 404，实际 %d", w.Code)
	}
}

// TestGetArtifactHandler_Disabled 验证未启用时读取返回 404（而非内容）。
func TestGetArtifactHandler_Disabled(t *testing.T) {
	db := newArtifactAPITestDB(t)
	seedArtifactSession(t, db, 42, "sess_abc")
	store := artifact.NewMemoryStore()
	_ = store.Write("sess:sess_abc", artifact.PlanArtifact, "x")
	w, _ := callGetArtifact(t, db, store, false, 42, "sess_abc", artifact.PlanArtifact, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("未启用应 404，实际 %d", w.Code)
	}
}

// TestArtifactHandler_OwnerIsolation 验证跨用户访问他人会话的产物返回 404（不泄漏存在性）。
func TestArtifactHandler_OwnerIsolation(t *testing.T) {
	db := newArtifactAPITestDB(t)
	seedArtifactSession(t, db, 42, "sess_abc")
	store := artifact.NewMemoryStore()
	_ = store.Write("sess:sess_abc", artifact.PlanArtifact, "x")

	// 用户 99 没有该会话 → 列表与查看都应 404。
	code, _ := callListArtifacts(t, db, store, true, 99, "sess_abc")
	if code != http.StatusNotFound {
		t.Fatalf("跨用户列表应 404，实际 %d", code)
	}
	w, _ := callGetArtifact(t, db, store, true, 99, "sess_abc", artifact.PlanArtifact, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("跨用户查看应 404，实际 %d", w.Code)
	}
}

// TestGetArtifactHandler_Binary 验证二进制产物内联时清空 content（引导下载）。
func TestGetArtifactHandler_Binary(t *testing.T) {
	db := newArtifactAPITestDB(t)
	seedArtifactSession(t, db, 42, "sess_abc")
	store := artifact.NewMemoryStore()
	scope := "sess:sess_abc"
	binary := "PK\x03\x04\x00\x00" + string(rune(0)) + "end" // 含 NUL 字节，被判定为二进制
	_ = store.Write(scope, "build.zip", binary)

	_, res := callGetArtifact(t, db, store, true, 42, "sess_abc", "build.zip", "")
	if !res.Binary {
		t.Fatal("含 NUL 应判定为 binary")
	}
	if res.Content != "" {
		t.Fatal("二进制内容内联应清空，引导走下载")
	}
}

// TestGetArtifactHandler_Truncated 验证超过 256KiB 的内容被截断并置 truncated=true。
func TestGetArtifactHandler_Truncated(t *testing.T) {
	db := newArtifactAPITestDB(t)
	seedArtifactSession(t, db, 42, "sess_abc")
	store := artifact.NewMemoryStore()
	scope := "sess:sess_abc"
	huge := strings.Repeat("a", (256*1024)+1024) // 257 KiB
	_ = store.Write(scope, "big.log", huge)

	_, res := callGetArtifact(t, db, store, true, 42, "sess_abc", "big.log", "")
	if !res.Truncated {
		t.Fatal("超大内容应 truncated=true")
	}
	if int64(len(res.Content)) != 256*1024 {
		t.Fatalf("截断后应为 256KiB，实际 %d", len(res.Content))
	}
}
