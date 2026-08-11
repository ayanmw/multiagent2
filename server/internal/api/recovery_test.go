package api

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ayanmw/multiagent2/server/internal/engine"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// recoveryDBSeq 为每个测试分配独立的共享缓存内存库名称，避免同进程内多个 Open(":memory:")
// 共享同一库导致 recovery 测试间相互污染。
var recoveryDBSeq int64

// newRecoveryTestDB 构造纯 Go 内存 SQLite（免 gcc），迁移 recovery 链路所需表
// （automations 供 GetAutomationByID 重建目标提示；automation_runs 供扫描/标记）。
func newRecoveryTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	name := fmt.Sprintf("file:recovery_%d?mode=memory&cache=shared", atomic.AddInt64(&recoveryDBSeq, 1))
	db, err := gorm.Open(sqlite.Open(name), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	if err := db.AutoMigrate(&model.Automation{}, &model.AutomationRun{}); err != nil {
		t.Fatalf("migrate recovery tables: %v", err)
	}
	return db
}

// mockRecoveryRunner 是 RecoveryRunner 的测试桩：记录收到的请求并可选地返回错误。
type mockRecoveryRunner struct {
	mu    sync.Mutex
	calls []Request
	err   error
}

func (m *mockRecoveryRunner) Run(_ context.Context, req Request) (*Result, error) {
	m.mu.Lock()
	m.calls = append(m.calls, req)
	m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	return &Result{SessionKey: req.SessionKey, Reply: "recovered"}, nil
}

func (m *mockRecoveryRunner) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

// captureCall 返回第 n 次调用的请求（线程安全）。
func (m *mockRecoveryRunner) captureCall(n int) Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n < 0 || n >= len(m.calls) {
		return Request{}
	}
	return m.calls[n]
}

// createRecoveryAutomation 落库一条启用的 cron 自动化，返回带主键的实体。
func createRecoveryAutomation(t *testing.T, db *gorm.DB, goal string) *model.Automation {
	t.Helper()
	a := &model.Automation{
		UserID:      1,
		Name:        "recover-" + goal,
		TriggerType: model.AutomationTriggerCron,
		CronExpr:    "0 2 * * *",
		GoalPrompt:  goal,
		Enabled:     true,
	}
	if err := repo.CreateAutomation(db, a); err != nil {
		t.Fatalf("create automation: %v", err)
	}
	return a
}

// TestRecover_NoUnfinished 验证无未收敛运行时直接返回 0，不触发任何恢复 Runner。
func TestRecover_NoUnfinished(t *testing.T) {
	db := newRecoveryTestDB(t)
	mock := &mockRecoveryRunner{}
	got := RecoverUnfinishedRuns(context.Background(), db, mock, engine.TeamConfig{}, 3, nil)
	if got != 0 {
		t.Fatalf("无未收敛运行应返回 0，实际 %d", got)
	}
	if mock.count() != 0 {
		t.Fatalf("不应调用恢复 Runner，calls=%d", mock.count())
	}
}

// TestRecover_SuccessMarksDone 验证一条 running 运行被续跑成功并标记 done，
// 且重发请求携带恢复提示、ChannelRecover 与目标契约 TeamOverride。
func TestRecover_SuccessMarksDone(t *testing.T) {
	db := newRecoveryTestDB(t)
	a := createRecoveryAutomation(t, db, "完成季度报告")
	run := &model.AutomationRun{
		AutomationID: a.ID, UserID: a.UserID, SessionKey: "sess-recover",
		Channel: model.RunChannelCron, Status: model.RunStatusRunning,
	}
	if err := repo.CreateAutomationRun(db, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	team := engine.TeamConfig{EnableSubAgents: true, EnableGoal: true}
	mock := &mockRecoveryRunner{}
	got := RecoverUnfinishedRuns(context.Background(), db, mock, team, 3, nil)
	if got != 1 {
		t.Fatalf("应恢复 1 条，实际 %d", got)
	}
	if mock.count() != 1 {
		t.Fatalf("恢复 Runner 应被调用 1 次，实际 %d", mock.count())
	}

	// 请求内容校验：恢复提示 + ChannelRecover + 目标契约覆盖 + 原 session 续跑。
	req := mock.captureCall(0)
	if !strings.Contains(req.Message, "[系统恢复]") {
		t.Fatalf("恢复消息应含系统恢复前缀，实际 %q", req.Message)
	}
	if !strings.Contains(req.Message, "完成季度报告") {
		t.Fatalf("恢复消息应含原始目标，实际 %q", req.Message)
	}
	if req.Channel == nil || req.Channel.Kind() != ChannelRecover.Kind() {
		t.Fatalf("恢复请求 Channel 应为 recover，实际 %v", req.Channel)
	}
	if req.UserID != a.UserID || req.SessionKey != "sess-recover" {
		t.Fatalf("恢复请求应沿用原归属与 session: uid=%d key=%q", req.UserID, req.SessionKey)
	}
	if req.TeamOverride == nil || !req.TeamOverride.EnableGoal {
		t.Fatalf("恢复请求应强制目标契约 TeamOverride，实际 %+v", req.TeamOverride)
	}

	// 运行记录应已标记 done。
	reloaded, err := repo.ListUnfinishedAutomationRuns(db)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(reloaded) != 0 {
		t.Fatalf("成功后不应再有待恢复运行，剩余 %d", len(reloaded))
	}
	var finalRun model.AutomationRun
	if err := db.First(&finalRun, run.ID).Error; err != nil {
		t.Fatalf("reload run: %v", err)
	}
	if finalRun.Status != model.RunStatusDone {
		t.Fatalf("运行应标记 done，实际 %q", finalRun.Status)
	}
}

// TestRecover_DoneNotRecovered 验证已 done 的运行不会被再次拾取续跑。
func TestRecover_DoneNotRecovered(t *testing.T) {
	db := newRecoveryTestDB(t)
	a := createRecoveryAutomation(t, db, "g")
	done := &model.AutomationRun{AutomationID: a.ID, UserID: a.UserID, SessionKey: "s", Channel: model.RunChannelCron, Status: model.RunStatusDone}
	if err := repo.CreateAutomationRun(db, done); err != nil {
		t.Fatalf("create done run: %v", err)
	}
	mock := &mockRecoveryRunner{}
	if got := RecoverUnfinishedRuns(context.Background(), db, mock, engine.TeamConfig{}, 3, nil); got != 0 {
		t.Fatalf("done 运行不应被恢复，got=%d", got)
	}
	if mock.count() != 0 {
		t.Fatalf("done 不应触发 Runner，calls=%d", mock.count())
	}
}

// TestRecover_UnknownAutomationMarksFailed 验证所属 automation 已删除时标记 failed 并跳过。
func TestRecover_UnknownAutomationMarksFailed(t *testing.T) {
	db := newRecoveryTestDB(t)
	run := &model.AutomationRun{AutomationID: 9999, UserID: 1, SessionKey: "s", Channel: model.RunChannelCron, Status: model.RunStatusRunning}
	if err := repo.CreateAutomationRun(db, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	mock := &mockRecoveryRunner{}
	if got := RecoverUnfinishedRuns(context.Background(), db, mock, engine.TeamConfig{}, 3, nil); got != 0 {
		t.Fatalf("automation 缺失不应计为已恢复，got=%d", got)
	}
	if mock.count() != 0 {
		t.Fatalf("缺失 automation 不应调用 Runner，calls=%d", mock.count())
	}
	var finalRun model.AutomationRun
	if err := db.First(&finalRun, run.ID).Error; err != nil {
		t.Fatalf("reload run: %v", err)
	}
	if finalRun.Status != model.RunStatusFailed {
		t.Fatalf("automation 缺失应标记 failed，实际 %q", finalRun.Status)
	}
}

// TestRecover_FailureBelowLimitKeepsRunning 验证运行失败且未达重试上限时保留 running。
func TestRecover_FailureBelowLimitKeepsRunning(t *testing.T) {
	db := newRecoveryTestDB(t)
	a := createRecoveryAutomation(t, db, "g")
	run := &model.AutomationRun{AutomationID: a.ID, UserID: a.UserID, SessionKey: "s", Channel: model.RunChannelCron, Status: model.RunStatusRunning, Attempts: 0}
	if err := repo.CreateAutomationRun(db, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	mock := &mockRecoveryRunner{err: errBoom}
	// maxAttempts=3，本次 attempts 0→1，未达上限，保留 running 待下次重启续跑。
	if got := RecoverUnfinishedRuns(context.Background(), db, mock, engine.TeamConfig{}, 3, nil); got != 1 {
		t.Fatalf("应计为已尝试 1 次，got=%d", got)
	}
	if mock.count() != 1 {
		t.Fatalf("失败也应调用 Runner 1 次，calls=%d", mock.count())
	}
	var finalRun model.AutomationRun
	if err := db.First(&finalRun, run.ID).Error; err != nil {
		t.Fatalf("reload run: %v", err)
	}
	if finalRun.Status != model.RunStatusRunning {
		t.Fatalf("未达上限应保留 running，实际 %q", finalRun.Status)
	}
	if finalRun.Attempts != 1 {
		t.Fatalf("失败应累加 attempts 至 1，实际 %d", finalRun.Attempts)
	}
}

// TestRecover_FailureAtLimitMarksFailed 验证运行失败且已达重试上限时标记 failed。
func TestRecover_FailureAtLimitMarksFailed(t *testing.T) {
	db := newRecoveryTestDB(t)
	a := createRecoveryAutomation(t, db, "g")
	// 已重试 2 次（maxAttempts=3），本次再失败即达上限。
	run := &model.AutomationRun{AutomationID: a.ID, UserID: a.UserID, SessionKey: "s", Channel: model.RunChannelCron, Status: model.RunStatusRunning, Attempts: 2}
	if err := repo.CreateAutomationRun(db, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	mock := &mockRecoveryRunner{err: errBoom}
	if got := RecoverUnfinishedRuns(context.Background(), db, mock, engine.TeamConfig{}, 3, nil); got != 1 {
		t.Fatalf("应计为已尝试 1 次，got=%d", got)
	}
	var finalRun model.AutomationRun
	if err := db.First(&finalRun, run.ID).Error; err != nil {
		t.Fatalf("reload run: %v", err)
	}
	if finalRun.Status != model.RunStatusFailed {
		t.Fatalf("达上限应标记 failed，实际 %q", finalRun.Status)
	}
	if finalRun.Attempts != 3 {
		t.Fatalf("达上限 attempts 应为 3，实际 %d", finalRun.Attempts)
	}
}

// errBoom 是恢复测试用的固定错误。
var errBoom = &recoveryError{}

type recoveryError struct{}

func (recoveryError) Error() string { return "recovery boom" }
