package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newTestDB 用纯 Go sqlite 建一个仅含调度测试所需表的库（免 gcc），单连接避免内存库跨连接丢失。
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	if err := db.AutoMigrate(&model.Automation{}, &model.Session{}, &model.Message{}, &model.AuditLog{}, &model.AutomationRun{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	return db
}

// mockRunner 记录调用并可选地返回错误，用于验证调度编排。
type mockRunner struct {
	mu    sync.Mutex
	calls []callRecord
	err   error
}

type callRecord struct {
	UID        uint
	Name       string
	SessionKey string
	Goal       string
}

func (m *mockRunner) Run(_ context.Context, a *model.Automation, sessionKey string) error {
	m.mu.Lock()
	m.calls = append(m.calls, callRecord{UID: a.UserID, Name: a.Name, SessionKey: sessionKey, Goal: a.GoalPrompt})
	m.mu.Unlock()
	return m.err
}

func (m *mockRunner) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

func ptrTime(t time.Time) *time.Time { return &t }

func TestScheduler_ComputesNextForNewAutomation(t *testing.T) {
	db := newTestDB(t)
	a := &model.Automation{
		UserID: 1, Name: "nightly", TriggerType: model.AutomationTriggerCron,
		CronExpr: "0 2 * * *", GoalPrompt: "g", Enabled: true,
		// NextRun 为空，模拟新创建。
	}
	if err := repo.CreateAutomation(db, a); err != nil {
		t.Fatal(err)
	}
	s := New(db, &mockRunner{})
	base := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return base }
	ran := s.TickSync(context.Background())
	if ran != 0 {
		t.Fatalf("新自动化首轮不应触发运行，ran=%d", ran)
	}
	got, err := repo.GetAutomationByID(db, 1, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.NextRun == nil {
		t.Fatal("应已写入 next_run")
	}
	want := time.Date(2026, 8, 11, 2, 0, 0, 0, time.UTC)
	if !got.NextRun.Equal(want) {
		t.Fatalf("next_run=%v, want %v", got.NextRun, want)
	}
}

func TestScheduler_RunsDueAutomationAndAdvances(t *testing.T) {
	db := newTestDB(t)
	base := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	a := &model.Automation{
		UserID: 1, Name: "every-min", TriggerType: model.AutomationTriggerCron,
		CronExpr: "*/1 * * * *", GoalPrompt: "do something", Enabled: true,
		NextRun: ptrTime(base.Add(-time.Minute)), // 已过期，立即到期
	}
	if err := repo.CreateAutomation(db, a); err != nil {
		t.Fatal(err)
	}
	mock := &mockRunner{}
	s := New(db, mock)
	s.Now = func() time.Time { return base }
	s.MaxRetries = 0
	ran := s.TickSync(context.Background())
	if ran != 1 {
		t.Fatalf("应触发 1 个，ran=%d", ran)
	}
	if mock.count() != 1 {
		t.Fatalf("runner 应被调用 1 次，实际 %d", mock.count())
	}

	// 自动建了会话（goal_prompt 由 runner 落库，但调度器已预先 create session）。
	var sessCnt int64
	if err := db.Model(&model.Session{}).Where("user_id = ?", 1).Count(&sessCnt).Error; err != nil {
		t.Fatal(err)
	}
	if sessCnt != 1 {
		t.Fatalf("应创建 1 个会话，实际 %d", sessCnt)
	}

	// next_run 应推进到未来；last_run 应写入。
	got, err := repo.GetAutomationByID(db, 1, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.NextRun == nil || !got.NextRun.After(base) {
		t.Fatalf("next_run 应推进到未来, got=%v", got.NextRun)
	}
	if got.LastRun == nil || !got.LastRun.Equal(base) {
		t.Fatalf("last_run 应写入 base, got=%v", got.LastRun)
	}
}

func TestScheduler_RetryAndAuditOnFailure(t *testing.T) {
	db := newTestDB(t)
	base := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	a := &model.Automation{
		UserID: 1, Name: "flaky", TriggerType: model.AutomationTriggerCron,
		CronExpr: "*/1 * * * *", GoalPrompt: "g", Enabled: true,
		NextRun: ptrTime(base.Add(-time.Minute)),
	}
	if err := repo.CreateAutomation(db, a); err != nil {
		t.Fatal(err)
	}
	mock := &mockRunner{err: errors.New("boom")}
	s := New(db, mock)
	s.Now = func() time.Time { return base }
	s.MaxRetries = 2
	s.RetryBackoff = 0
	s.RetryDelay = time.Minute
	ran := s.TickSync(context.Background())
	if ran != 1 {
		t.Fatalf("应触发 1 个（尽管失败），ran=%d", ran)
	}
	// MaxRetries=2 -> 共 MaxRetries+1 = 3 次尝试。
	if mock.count() != 3 {
		t.Fatalf("runner 应被调用 3 次（含重试），实际 %d", mock.count())
	}

	// 每次失败写一条审计，共 3 条。
	var auditCnt int64
	if err := db.Model(&model.AuditLog{}).Count(&auditCnt).Error; err != nil {
		t.Fatal(err)
	}
	if auditCnt != 3 {
		t.Fatalf("应写 3 条失败审计，实际 %d", auditCnt)
	}

	// 失败时 last_run 不写，next_run 按 RetryDelay 快速重试。
	got, err := repo.GetAutomationByID(db, 1, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastRun != nil {
		t.Fatalf("失败不应写 last_run, got=%v", got.LastRun)
	}
	if got.NextRun == nil || !got.NextRun.Equal(base.Add(time.Minute)) {
		t.Fatalf("失败 next_run 应为 base+1min, got=%v", got.NextRun)
	}
}

func TestScheduler_SkipsNotDue(t *testing.T) {
	db := newTestDB(t)
	base := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	a := &model.Automation{
		UserID: 1, Name: "future", TriggerType: model.AutomationTriggerCron,
		CronExpr: "0 2 * * *", GoalPrompt: "g", Enabled: true,
		NextRun: ptrTime(base.Add(time.Hour)), // 未来，未到期
	}
	if err := repo.CreateAutomation(db, a); err != nil {
		t.Fatal(err)
	}
	mock := &mockRunner{}
	s := New(db, mock)
	s.Now = func() time.Time { return base }
	ran := s.TickSync(context.Background())
	if ran != 0 {
		t.Fatalf("未到期不应触发, ran=%d", ran)
	}
	if mock.count() != 0 {
		t.Fatalf("runner 不应被调用, 实际 %d", mock.count())
	}
}

// TestScheduler_RecordsRunDone 验证 M4-05：运行成功收敛后写入 automation_runs(done)，
// 供进程重启后「跨天恢复」扫描判定已结束（不误判为待恢复）。
func TestScheduler_RecordsRunDone(t *testing.T) {
	db := newTestDB(t)
	base := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	a := &model.Automation{
		UserID: 1, Name: "ok-loop", TriggerType: model.AutomationTriggerCron,
		CronExpr: "*/1 * * * *", GoalPrompt: "g", Enabled: true,
		NextRun: ptrTime(base.Add(-time.Minute)),
	}
	if err := repo.CreateAutomation(db, a); err != nil {
		t.Fatal(err)
	}
	mock := &mockRunner{} // 成功
	s := New(db, mock)
	s.Now = func() time.Time { return base }
	s.MaxRetries = 0
	if ran := s.TickSync(context.Background()); ran != 1 {
		t.Fatalf("应触发 1 个，ran=%d", ran)
	}
	// 注意：scheduler 测试库为 cache=shared 内存库，跨测试共享，故按本 automation 主键过滤，
	// 而非按 user_id 统计（会混入其他测试的残留运行记录）。
	var runs []model.AutomationRun
	if err := db.Where("automation_id = ?", a.ID).Find(&runs).Error; err != nil {
		t.Fatalf("查询运行记录: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("应写入 1 条运行记录，实际 %d", len(runs))
	}
	if runs[0].Status != model.RunStatusDone {
		t.Fatalf("成功应标记 done，实际 %q", runs[0].Status)
	}
	if runs[0].Channel != model.RunChannelCron {
		t.Fatalf("应由 cron 渠道触发，实际 %q", runs[0].Channel)
	}
	// 不应残留 running（否则会被恢复扫描误续跑）。
	unfinished, err := repo.ListUnfinishedAutomationRuns(db)
	if err != nil {
		t.Fatalf("ListUnfinished: %v", err)
	}
	if len(unfinished) != 0 {
		t.Fatalf("成功运行不应残留 running，剩余 %d", len(unfinished))
	}
}

// TestScheduler_RecordsRunFailed 验证 M4-05：运行失败写入 automation_runs(failed)，
// 避免恢复扫描将其当作「未收敛」在每次重启时无限续跑。
func TestScheduler_RecordsRunFailed(t *testing.T) {
	db := newTestDB(t)
	base := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	a := &model.Automation{
		UserID: 1, Name: "fail-loop", TriggerType: model.AutomationTriggerCron,
		CronExpr: "*/1 * * * *", GoalPrompt: "g", Enabled: true,
		NextRun: ptrTime(base.Add(-time.Minute)),
	}
	if err := repo.CreateAutomation(db, a); err != nil {
		t.Fatal(err)
	}
	mock := &mockRunner{err: errors.New("boom")}
	s := New(db, mock)
	s.Now = func() time.Time { return base }
	s.MaxRetries = 0
	if ran := s.TickSync(context.Background()); ran != 1 {
		t.Fatalf("失败也应触发 1 个，ran=%d", ran)
	}
	// 按本 automation 主键过滤（cache=shared 内存库跨测试共享，避免统计其他测试残留）。
	var runs []model.AutomationRun
	if err := db.Where("automation_id = ?", a.ID).Find(&runs).Error; err != nil {
		t.Fatalf("查询运行记录: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("应写入 1 条运行记录，实际 %d", len(runs))
	}
	if runs[0].Status != model.RunStatusFailed {
		t.Fatalf("失败应标记 failed，实际 %q", runs[0].Status)
	}
	if runs[0].Error == "" {
		t.Fatal("失败记录应保留错误信息")
	}
}
