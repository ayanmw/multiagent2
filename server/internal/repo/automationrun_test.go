package repo

import (
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// runDBSeq 为每个测试分配独立的共享缓存内存库名称，避免同进程内多个 Open(":memory:")
// 共享同一库导致测试间相互污染（glebarez/sqlite 的 :memory: 在同进程内跨连接共享）。
var runDBSeq int64

// newRunTestDB 构造纯 Go 内存 SQLite（免 gcc），仅迁移 automation_runs 表。
// 使用唯一 cache=shared 名称，保证每个测试隔离。
func newRunTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	name := fmt.Sprintf("file:runtest_%d?mode=memory&cache=shared", atomic.AddInt64(&runDBSeq, 1))
	db, err := gorm.Open(sqlite.Open(name), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	if err := db.AutoMigrate(&model.AutomationRun{}); err != nil {
		t.Fatalf("migrate automation_runs: %v", err)
	}
	return db
}

// TestAutomationRun_CRUD 验证运行记录的创建、状态列表过滤与状态标记（M4-05 持久化出口）。
func TestAutomationRun_CRUD(t *testing.T) {
	db := newRunTestDB(t)

	// 创建 2 条 running、1 条 done、1 条 failed。
	running1 := &model.AutomationRun{AutomationID: 1, UserID: 10, SessionKey: "s1", Channel: model.RunChannelCron, Status: model.RunStatusRunning}
	running2 := &model.AutomationRun{AutomationID: 2, UserID: 10, SessionKey: "s2", Channel: model.RunChannelWebhook, Status: model.RunStatusRunning}
	done := &model.AutomationRun{AutomationID: 3, UserID: 10, SessionKey: "s3", Channel: model.RunChannelCron, Status: model.RunStatusDone}
	failed := &model.AutomationRun{AutomationID: 4, UserID: 10, SessionKey: "s4", Channel: model.RunChannelRecover, Status: model.RunStatusFailed}
	for _, r := range []*model.AutomationRun{running1, running2, done, failed} {
		if err := CreateAutomationRun(db, r); err != nil {
			t.Fatalf("CreateAutomationRun: %v", err)
		}
	}

	// ListUnfinished 只返回 running 的两条。
	unfinished, err := ListUnfinishedAutomationRuns(db)
	if err != nil {
		t.Fatalf("ListUnfinishedAutomationRuns: %v", err)
	}
	if len(unfinished) != 2 {
		t.Fatalf("未收敛运行应为 2 条，实际 %d", len(unfinished))
	}
	for _, r := range unfinished {
		if r.Status != model.RunStatusRunning {
			t.Fatalf("ListUnfinished 混入非 running 记录: %+v", r)
		}
	}

	// MarkAutomationRunStatus 把 running1 标为 done（幂等 Updates）。
	if err := MarkAutomationRunStatus(db, running1.ID, model.RunStatusDone, "", 1); err != nil {
		t.Fatalf("MarkAutomationRunStatus done: %v", err)
	}
	// 标记后再查未收敛，应只剩 running2。
	unfinished, err = ListUnfinishedAutomationRuns(db)
	if err != nil {
		t.Fatalf("ListUnfinishedAutomationRuns 2: %v", err)
	}
	if len(unfinished) != 1 || unfinished[0].ID != running2.ID {
		t.Fatalf("标记 done 后未收敛应只剩 running2，实际 %+v", unfinished)
	}

	// 标为 failed 并带回错误与重试次数。
	if err := MarkAutomationRunStatus(db, running2.ID, model.RunStatusFailed, "boom", 3); err != nil {
		t.Fatalf("MarkAutomationRunStatus failed: %v", err)
	}
	var got model.AutomationRun
	if err := db.First(&got, running2.ID).Error; err != nil {
		t.Fatalf("reload running2: %v", err)
	}
	if got.Status != model.RunStatusFailed || got.Error != "boom" || got.Attempts != 3 {
		t.Fatalf("failed 状态/错误/重试未正确回写: %+v", got)
	}
}

// TestAutomationRun_ListByUser 验证按用户隔离与倒序（诊断/测试用）。
func TestAutomationRun_ListByUser(t *testing.T) {
	db := newRunTestDB(t)

	// 用户 10 两条，用户 99 一条。
	for _, r := range []*model.AutomationRun{
		{AutomationID: 1, UserID: 10, SessionKey: "a", Channel: model.RunChannelCron, Status: model.RunStatusRunning},
		{AutomationID: 2, UserID: 10, SessionKey: "b", Channel: model.RunChannelCron, Status: model.RunStatusDone},
		{AutomationID: 3, UserID: 99, SessionKey: "c", Channel: model.RunChannelWebhook, Status: model.RunStatusRunning},
	} {
		if err := CreateAutomationRun(db, r); err != nil {
			t.Fatalf("CreateAutomationRun: %v", err)
		}
	}

	list, err := ListAutomationRuns(db, 10)
	if err != nil {
		t.Fatalf("ListAutomationRuns(10): %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("用户 10 应见 2 条，实际 %d", len(list))
	}
	// 倒序：后插入的 b（done）应在前。
	if list[0].SessionKey != "b" {
		t.Fatalf("应按 created_at 倒序，首条应为 b，实际 %q", list[0].SessionKey)
	}

	list99, err := ListAutomationRuns(db, 99)
	if err != nil {
		t.Fatalf("ListAutomationRuns(99): %v", err)
	}
	if len(list99) != 1 || list99[0].UserID != 99 {
		t.Fatalf("用户 99 隔离异常: %+v", list99)
	}
}
