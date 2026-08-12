package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/eval"
	"github.com/ayanmw/multiagent2/server/internal/engine"
	"github.com/ayanmw/multiagent2/server/internal/middleware"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// newEvalTestDB 构造内存 SQLite（纯 Go，无 CGO）并迁移评估四表。
func newEvalTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if s, err := db.DB(); err == nil {
		s.SetMaxOpenConns(1)
	}
	if err := db.AutoMigrate(&model.EvalDataset{}, &model.EvalCase{}, &model.EvalRun{}, &model.EvalResult{}); err != nil {
		t.Fatalf("migrate eval: %v", err)
	}
	return db
}

// evalCtxWithUser 构造带用户身份的测试上下文（绕过 RBAC 中间件，专注 handler 逻辑）。
func evalCtxWithUser(uid uint) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set(middleware.CtxUserID, uid)
	return c, w
}

// evalCall 给测试上下文装上 JSON 请求体 + 路由参数，并真正调用 handler。
func evalCall(db *gorm.DB, h gin.HandlerFunc, method, path string, body any, params gin.Params, uid uint) (*httptest.ResponseRecorder, *gin.Context) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set(middleware.CtxUserID, uid)
	var r *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	} else {
		r = bytes.NewReader(nil)
	}
	c.Request, _ = http.NewRequest(method, path, r)
	c.Params = params
	h(c)
	return w, c
}

func evalParams(kv ...string) gin.Params {
	p := gin.Params{}
	for i := 0; i+1 < len(kv); i += 2 {
		p = append(p, gin.Param{Key: kv[i], Value: kv[i+1]})
	}
	return p
}

// ---- mock 实现（不真实调 LLM，覆盖评估运行主链路）----

type evalMockRunner struct{ output string }

func (m *evalMockRunner) RunCase(_ context.Context, _ uint, _ string, _ string) (string, int64, error) {
	return m.output, 10, nil
}

func dummyEvalResolve(_ context.Context, _ uint, _ string) (engine.ModelConfig, error) {
	return engine.ModelConfig{ModelID: "m"}, nil
}

// TestEvalDatasetCRUD 验收：建 / 列 / 查 / 改 / 删 + owner 隔离（越权 404）。
func TestEvalDatasetCRUD(t *testing.T) {
	db := newEvalTestDB(t)
	uid := uint(7)

	// 创建
	w, _ := evalCall(db, CreateEvalDatasetHandler(db), http.MethodPost, "/api/eval/datasets", createEvalDatasetBody{Name: "ds1", DefaultGrader: "exact"}, nil, uid)
	if w.Code != http.StatusOK {
		t.Fatalf("create 期望 200, 实际 %d body=%s", w.Code, w.Body.String())
	}
	var created evalDatasetView
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if created.ID == 0 || created.Name != "ds1" {
		t.Fatalf("创建返回异常: %+v", created)
	}

	// 同名冲突
	w, _ = evalCall(db, CreateEvalDatasetHandler(db), http.MethodPost, "/api/eval/datasets", createEvalDatasetBody{Name: "ds1", DefaultGrader: "exact"}, nil, uid)
	if w.Code != http.StatusConflict {
		t.Fatalf("同名冲突期望 409, 实际 %d", w.Code)
	}

	// 列表
	w, _ = evalCall(db, ListEvalDatasetsHandler(db), http.MethodGet, "/api/eval/datasets", nil, nil, uid)
	if w.Code != http.StatusOK {
		t.Fatalf("list 期望 200, 实际 %d", w.Code)
	}

	// 查询（含 owner 隔离：别的用户 404）
	idStr := strconv.FormatUint(uint64(created.ID), 10)
	w, _ = evalCall(db, GetEvalDatasetHandler(db), http.MethodGet, "/api/eval/datasets/"+idStr, nil, evalParams("id", idStr), uid)
	if w.Code != http.StatusOK {
		t.Fatalf("get 期望 200, 实际 %d", w.Code)
	}
	w, _ = evalCall(db, GetEvalDatasetHandler(db), http.MethodGet, "/api/eval/datasets/"+idStr, nil, evalParams("id", idStr), 99)
	if w.Code != http.StatusNotFound {
		t.Fatalf("越权 get 期望 404, 实际 %d", w.Code)
	}

	// 更新
	w, _ = evalCall(db, UpdateEvalDatasetHandler(db), http.MethodPut, "/api/eval/datasets/"+idStr, updateEvalDatasetBody{Description: ptr("desc")}, evalParams("id", idStr), uid)
	if w.Code != http.StatusOK {
		t.Fatalf("update 期望 200, 实际 %d body=%s", w.Code, w.Body.String())
	}
	var upd evalDatasetView
	_ = json.Unmarshal(w.Body.Bytes(), &upd)
	if upd.Description != "desc" {
		t.Fatalf("更新未生效: %+v", upd)
	}

	// 删除（级联）
	w, _ = evalCall(db, DeleteEvalDatasetHandler(db), http.MethodDelete, "/api/eval/datasets/"+idStr, nil, evalParams("id", idStr), uid)
	if w.Code != http.StatusOK {
		t.Fatalf("delete 期望 200, 实际 %d", w.Code)
	}
	if _, err := repo.GetEvalDataset(db, uid, created.ID); err == nil {
		t.Fatalf("删除后仍能查到数据集")
	}
}

// TestEvalCaseCRUD 验收：用例建/列/查/改/删。
func TestEvalCaseCRUD(t *testing.T) {
	db := newEvalTestDB(t)
	uid := uint(7)
	ds := &model.EvalDataset{UserID: uid, Name: "ds", DefaultGrader: model.GraderExact}
	_ = ds.Validate()
	if err := repo.CreateEvalDataset(db, ds); err != nil {
		t.Fatalf("seed dataset: %v", err)
	}
	idStr := strconv.FormatUint(uint64(ds.ID), 10)

	// 创建用例
	w, _ := evalCall(db, CreateEvalCaseHandler(db), http.MethodPost, "/api/eval/datasets/"+idStr+"/cases", createEvalCaseBody{Input: "1+1=?", Expected: "2"}, evalParams("id", idStr), uid)
	if w.Code != http.StatusOK {
		t.Fatalf("create case 期望 200, 实际 %d body=%s", w.Code, w.Body.String())
	}
	var cs evalCaseView
	_ = json.Unmarshal(w.Body.Bytes(), &cs)
	if cs.ID == 0 || cs.Input != "1+1=?" {
		t.Fatalf("用例返回异常: %+v", cs)
	}

	// 列表
	w, _ = evalCall(db, ListEvalCasesHandler(db), http.MethodGet, "/api/eval/datasets/"+idStr+"/cases", nil, evalParams("id", idStr), uid)
	if w.Code != http.StatusOK {
		t.Fatalf("list cases 期望 200, 实际 %d", w.Code)
	}

	// 更新用例
	caseStr := strconv.FormatUint(uint64(cs.ID), 10)
	w, _ = evalCall(db, UpdateEvalCaseHandler(db), http.MethodPut, "/api/eval/datasets/"+idStr+"/cases/"+caseStr, updateEvalCaseBody{Expected: ptr("3")}, evalParams("id", idStr, "caseId", caseStr), uid)
	if w.Code != http.StatusOK {
		t.Fatalf("update case 期望 200, 实际 %d body=%s", w.Code, w.Body.String())
	}
	var upd evalCaseView
	_ = json.Unmarshal(w.Body.Bytes(), &upd)
	if upd.Expected != "3" {
		t.Fatalf("用例更新未生效: %+v", upd)
	}

	// 删除用例
	w, _ = evalCall(db, DeleteEvalCaseHandler(db), http.MethodDelete, "/api/eval/datasets/"+idStr+"/cases/"+caseStr, nil, evalParams("id", idStr, "caseId", caseStr), uid)
	if w.Code != http.StatusOK {
		t.Fatalf("delete case 期望 200, 实际 %d", w.Code)
	}
	if _, err := repo.GetEvalCase(db, ds.ID, cs.ID); err == nil {
		t.Fatalf("删除后仍能查到用例")
	}
}

// TestEvalRunFlow 验收：触发运行（异步）→ 轮询到 done → 聚合分正确 → 结果可读。
func TestEvalRunFlow(t *testing.T) {
	db := newEvalTestDB(t)
	uid := uint(7)
	ds := &model.EvalDataset{UserID: uid, Name: "ds", DefaultGrader: model.GraderExact, DefaultModel: "m1"}
	_ = ds.Validate()
	if err := repo.CreateEvalDataset(db, ds); err != nil {
		t.Fatalf("seed dataset: %v", err)
	}
	cs := &model.EvalCase{DatasetID: ds.ID, Input: "1+1=?", Expected: "2"}
	_ = cs.Validate()
	if err := repo.CreateEvalCase(db, cs); err != nil {
		t.Fatalf("seed case: %v", err)
	}

	// 注入评估服务（mock runner 返回 "2" → exact 通过）。
	SetEvalService(eval.NewService(db, dummyEvalResolve, &evalMockRunner{output: "2"}, nil))
	defer SetEvalService(nil)

	idStr := strconv.FormatUint(uint64(ds.ID), 10)
	w, _ := evalCall(db, RunEvalHandler(db), http.MethodPost, "/api/eval/datasets/"+idStr+"/run", runEvalBody{Repeats: 2}, evalParams("id", idStr), uid)
	if w.Code != http.StatusOK {
		t.Fatalf("run 期望 200, 实际 %d body=%s", w.Code, w.Body.String())
	}
	var run evalRunView
	_ = json.Unmarshal(w.Body.Bytes(), &run)
	if run.ID == 0 || run.Status != model.EvalRunStatusRunning {
		t.Fatalf("run 返回异常: %+v", run)
	}

	// 轮询到结束（StartRun 异步执行）。
	deadline := time.Now().Add(5 * time.Second)
	var got *model.EvalRun
	for time.Now().Before(deadline) {
		got, _ = repo.GetEvalRun(db, uid, run.ID)
		if got.Status != model.EvalRunStatusRunning {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got == nil || got.Status != model.EvalRunStatusDone {
		t.Fatalf("运行未收敛到 done: %+v", got)
	}
	if got.ScoreAvg != 1.0 || got.PassRate != 1.0 || got.TotalAttempts != 2 {
		t.Fatalf("聚合异常: score=%.2f pass=%.2f attempts=%d", got.ScoreAvg, got.PassRate, got.TotalAttempts)
	}

	// 结果可读
	runStr := strconv.FormatUint(uint64(run.ID), 10)
	w, _ = evalCall(db, ListEvalResultsHandler(db), http.MethodGet, "/api/eval/runs/"+runStr+"/results", nil, evalParams("id", runStr), uid)
	if w.Code != http.StatusOK {
		t.Fatalf("results 期望 200, 实际 %d", w.Code)
	}
	var res struct {
		Results []evalResultView `json:"results"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if len(res.Results) != 2 {
		t.Fatalf("期望 2 条结果, 实际 %d", len(res.Results))
	}
	for _, r := range res.Results {
		if r.Score != 1.0 || !r.Passed {
			t.Fatalf("结果异常: %+v", r)
		}
	}
}

func ptr(s string) *string { return &s }
