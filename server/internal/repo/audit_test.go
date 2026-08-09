package repo

import (
	"testing"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/executor"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// newAuditTestDB 构造一个内存 SQLite（纯 Go，无需 CGO），并自动迁移 audit_logs 表。
func newAuditTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.AuditLog{}); err != nil {
		t.Fatalf("migrate audit_logs: %v", err)
	}
	return db
}

// TestDBAuditor_RecordsAllDecisions 验证 M3-01 验收核心：
// allow / deny / ask 三类命令执行均被 DBAuditor 写入审计日志，且归属 owner 正确。
func TestDBAuditor_RecordsAllDecisions(t *testing.T) {
	db := newAuditTestDB(t)
	const owner uint = 42
	aud := NewDBAuditor(db, owner)

	now := time.Now()
	aud.Record(executor.AuditEntry{Command: "ls -la", Workdir: "/w", Decision: executor.DecisionAllow, Reason: "策略放行", Allowed: true, Timestamp: now})
	aud.Record(executor.AuditEntry{Command: "rm -rf /", Workdir: "/w", Decision: executor.DecisionDeny, Reason: "危险命令黑名单", Allowed: false, Timestamp: now})
	aud.Record(executor.AuditEntry{Command: "git push --force", Workdir: "/w", Decision: executor.DecisionAsk, Reason: "需确认", Allowed: true, Note: "交互模式确认", Timestamp: now})

	list, total, err := ListAuditLogs(db, AuditLogFilter{UserID: owner})
	if err != nil {
		t.Fatalf("ListAuditLogs: %v", err)
	}
	if total != 3 {
		t.Fatalf("期望 3 条审计，实际 %d", total)
	}
	seen := map[string]bool{}
	for _, l := range list {
		if l.UserID != owner {
			t.Fatalf("owner 应为 %d，实际 %d", owner, l.UserID)
		}
		seen[l.Decision] = true
	}
	for _, d := range []string{
		executor.DecisionAllow.String(),
		executor.DecisionDeny.String(),
		executor.DecisionAsk.String(),
	} {
		if !seen[d] {
			t.Fatalf("缺少 decision=%s 的审计记录", d)
		}
	}
}

// TestDBAuditor_OwnerIsolation 验证 owner 隔离：不同用户写入后按 UserID 过滤互不串台。
func TestDBAuditor_OwnerIsolation(t *testing.T) {
	db := newAuditTestDB(t)
	NewDBAuditor(db, 42).Record(executor.AuditEntry{Command: "echo a", Decision: executor.DecisionAllow, Allowed: true, Timestamp: time.Now()})
	NewDBAuditor(db, 99).Record(executor.AuditEntry{Command: "echo b", Decision: executor.DecisionAllow, Allowed: true, Timestamp: time.Now()})

	own, total, err := ListAuditLogs(db, AuditLogFilter{UserID: 42})
	if err != nil {
		t.Fatalf("ListAuditLogs(42): %v", err)
	}
	if total != 1 || len(own) != 1 || own[0].UserID != 42 {
		t.Fatalf("owner=42 应仅见本人 1 条，实际 total=%d", total)
	}
	// developer/admin 使用 UserID=0 看全员。
	_, totalAll, err := ListAuditLogs(db, AuditLogFilter{UserID: 0})
	if err != nil {
		t.Fatalf("ListAuditLogs(0): %v", err)
	}
	if totalAll != 2 {
		t.Fatalf("owner=0 应见全员 2 条，实际 %d", totalAll)
	}
}

// TestDBAuditor_NilDBNoop 验证 db 为 nil 时 Record 不 panic（安全降级，不阻断命令执行）。
func TestDBAuditor_NilDBNoop(t *testing.T) {
	aud := NewDBAuditor(nil, 1)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nil db 下 Record 不应 panic: %v", r)
		}
	}()
	aud.Record(executor.AuditEntry{Command: "ls", Decision: executor.DecisionAllow, Allowed: true, Timestamp: time.Now()})
}

// TestListAuditLogs_FilterAndPagination 验证按 decision/command 过滤与分页。
func TestListAuditLogs_FilterAndPagination(t *testing.T) {
	db := newAuditTestDB(t)
	const owner uint = 7
	aud := NewDBAuditor(db, owner)
	for i := 0; i < 5; i++ {
		aud.Record(executor.AuditEntry{
			Command:  "shell-" + string(rune('a'+i)),
			Decision: executor.DecisionAllow,
			Allowed:  true,
			Timestamp: time.Now(),
		})
	}
	aud.Record(executor.AuditEntry{Command: "forbidden", Decision: executor.DecisionDeny, Allowed: false, Timestamp: time.Now()})

	// 仅过滤 deny。
	deny, totalDeny, err := ListAuditLogs(db, AuditLogFilter{UserID: owner, Decision: executor.DecisionDeny.String()})
	if err != nil {
		t.Fatalf("ListAuditLogs deny: %v", err)
	}
	if totalDeny != 1 || deny[0].Command != "forbidden" {
		t.Fatalf("deny 过滤异常: total=%d", totalDeny)
	}

	// 命令模糊匹配。
	_, totalM, err := ListAuditLogs(db, AuditLogFilter{UserID: owner, Command: "shell"})
	if err != nil {
		t.Fatalf("ListAuditLogs command: %v", err)
	}
	if totalM != 5 {
		t.Fatalf("command 模糊匹配应命中 5 条，实际 %d", totalM)
	}

	// 分页：取前 2 条。
	p, totalP, err := ListAuditLogs(db, AuditLogFilter{UserID: owner, Limit: 2, Offset: 0})
	if err != nil {
		t.Fatalf("ListAuditLogs page: %v", err)
	}
	if totalP != 6 || len(p) != 2 {
		t.Fatalf("分页异常: total=%d returned=%d", totalP, len(p))
	}
}
