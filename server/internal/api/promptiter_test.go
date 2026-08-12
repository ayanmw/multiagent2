package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ayanmw/multiagent2/server/internal/middleware"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/promptiter"
	"github.com/glebarez/sqlite"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newPromptiterTestDB 建一个含指令/优化运行表的纯 Go sqlite 库。
func newPromptiterTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.AgentInstruction{}, &model.PromptIterRun{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	return db
}

// withUser 给 gin 路由注入测试用户上下文（模拟已认证）。
func withUser() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(middleware.CtxUserID, uint(1))
		c.Next()
	}
}

// TestInstructionsCRUD 覆盖 GET 列表 / GET 单条（含未找到）/ PUT 创建 / 再次 GET。
func TestInstructionsCRUD(t *testing.T) {
	db := newPromptiterTestDB(t)
	r := gin.New()
	r.Use(withUser())
	r.GET("/api/instructions", ListInstructionsHandler(db))
	r.GET("/api/instructions/:name", GetInstructionHandler(db))
	r.PUT("/api/instructions/:name", UpdateInstructionHandler(db))

	// 未找到。
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/instructions/default", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("未找到应 404，实际 %d", w.Code)
	}

	// 创建（PUT）。
	body, _ := json.Marshal(map[string]string{"content": "improved instruction", "role": "single"})
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodPut, "/api/instructions/default", bytes.NewReader(body))
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("创建应 200，实际 %d: %s", w.Code, w.Body.String())
	}
	var created instructionView
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if created.Content != "improved instruction" || created.Version != 1 {
		t.Fatalf("创建内容/版本不符: %+v", created)
	}

	// 再次 GET。
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodGet, "/api/instructions/default", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("获取应 200，实际 %d", w.Code)
	}
	var got instructionView
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Content != "improved instruction" {
		t.Fatalf("内容不符: %q", got.Content)
	}

	// 列表。
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodGet, "/api/instructions", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("列表应 200，实际 %d", w.Code)
	}
	var list struct {
		Instructions []instructionView `json:"instructions"`
		Total        int               `json:"total"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if list.Total != 1 {
		t.Fatalf("列表应含 1 条，实际 %d", list.Total)
	}
}

// TestOptimizeHandler_Unavailable 验证服务未注入时返回 503。
func TestOptimizeHandler_Unavailable(t *testing.T) {
	SetPromptIterService(nil)
	db := newPromptiterTestDB(t)
	r := gin.New()
	r.Use(withUser())
	r.POST("/api/promptiter/optimize", OptimizePromptIterHandler(db))

	w := httptest.NewRecorder()
	body, _ := json.Marshal(map[string]uint{"dataset_id": 1})
	req, _ := http.NewRequest(http.MethodPost, "/api/promptiter/optimize", bytes.NewReader(body))
	r.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("未注入应 503，实际 %d", w.Code)
	}
}

// TestOptimizeHandler_Accepted 验证服务注入后异步触发返回 202。
func TestOptimizeHandler_Accepted(t *testing.T) {
	db := newPromptiterTestDB(t)
	// 注入一个真实服务（resolver 为 nil：异步评估会失败，但 handler 同步返回 202）。
	SetPromptIterService(promptiter.NewService(db, nil, nil, nil))
	defer SetPromptIterService(nil)

	r := gin.New()
	r.Use(withUser())
	r.POST("/api/promptiter/optimize", OptimizePromptIterHandler(db))

	w := httptest.NewRecorder()
	body, _ := json.Marshal(map[string]uint{"dataset_id": 1})
	req, _ := http.NewRequest(http.MethodPost, "/api/promptiter/optimize", bytes.NewReader(body))
	r.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("应返回 202，实际 %d: %s", w.Code, w.Body.String())
	}
	var run promptIterRunView
	if err := json.Unmarshal(w.Body.Bytes(), &run); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if run.ID == 0 {
		t.Fatalf("应返回有效的 run id")
	}
}
