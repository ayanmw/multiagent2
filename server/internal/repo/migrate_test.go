package repo

import (
	"path/filepath"
	"testing"

	"github.com/ayanmw/multiagent2/server/internal/config"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// newMigrateTestDB 打开一个纯 Go（无 CGO）SQLite 连接，不做任何迁移。
func newMigrateTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "migrate.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, e := db.DB(); e == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

// TestMigrations_ListIsValid 防呆：版本号非空、唯一、严格升序（手写列表易插错位）。
func TestMigrations_ListIsValid(t *testing.T) {
	ms := Migrations()
	if len(ms) == 0 {
		t.Fatal("迁移列表不应为空")
	}
	if err := validateMigrations(ms); err != nil {
		t.Fatalf("Migrations() 非法: %v", err)
	}
	for _, m := range ms {
		if m.Name == "" || m.Up == nil {
			t.Fatalf("迁移 %s 缺少 Name/Up", m.Version)
		}
	}

	// 反例：重复版本 / 乱序都应被拒。
	if err := validateMigrations([]Migration{{Version: "0001"}, {Version: "0001"}}); err == nil {
		t.Fatal("重复版本应报错")
	}
	if err := validateMigrations([]Migration{{Version: "0002"}, {Version: "0001"}}); err == nil {
		t.Fatal("乱序版本应报错")
	}
}

// TestRunMigrations_FreshDBBuildsFullSchema 验收核心之一：
// 全新库跑完基线后，表结构与当前模型一致（无需再靠 AutoMigrate 补齐）。
func TestRunMigrations_FreshDBBuildsFullSchema(t *testing.T) {
	db := newMigrateTestDB(t)

	applied, err := RunMigrations(db, MigrationContext{EncryptionKey: testEncKey()})
	if err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	if len(applied) != len(Migrations()) {
		t.Fatalf("首次应执行全部迁移, got %v", applied)
	}

	// 全部模型的表都应存在。
	for _, m := range baselineModels() {
		if !db.Migrator().HasTable(m) {
			t.Fatalf("基线未建表: %T", m)
		}
	}
	// 抽查几个后期里程碑新增的列，确认基线是「当前」结构而非历史快照。
	for _, c := range []struct {
		dst any
		col string
	}{
		{&model.MCPServer{}, "env_enc"},        // M3-07
		{&model.Session{}, "workspace_id"},     // M1-07
		{&model.Checkpoint{}, "status"},        // M3-05
		{&model.UsageRecord{}, "total_tokens"}, // M3-03
	} {
		if !db.Migrator().HasColumn(c.dst, c.col) {
			t.Fatalf("基线缺列 %T.%s", c.dst, c.col)
		}
	}

	// 版本表落齐。
	rows, err := AppliedMigrations(db)
	if err != nil {
		t.Fatalf("AppliedMigrations: %v", err)
	}
	if len(rows) != len(Migrations()) {
		t.Fatalf("schema_migrations 行数不符: %d", len(rows))
	}
	if rows[0].Version != "0001" || rows[0].Name == "" || rows[0].AppliedAt.IsZero() {
		t.Fatalf("版本行内容异常: %+v", rows[0])
	}

	pending, err := PendingMigrations(db)
	if err != nil {
		t.Fatalf("PendingMigrations: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("迁移完成后不应有 pending: %v", pending)
	}
}

// TestRunMigrations_Idempotent 二次启动不得重复执行、不得重复记录版本。
func TestRunMigrations_Idempotent(t *testing.T) {
	db := newMigrateTestDB(t)
	mc := MigrationContext{EncryptionKey: testEncKey()}

	if _, err := RunMigrations(db, mc); err != nil {
		t.Fatalf("first run: %v", err)
	}
	// 写入一行业务数据，验证二次迁移不破坏数据。
	if err := db.Create(&model.AuditLog{UserID: 1, Command: "ls", Decision: "allow"}).Error; err != nil {
		t.Fatalf("insert audit log: %v", err)
	}

	applied, err := RunMigrations(db, mc)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if len(applied) != 0 {
		t.Fatalf("二次运行不应再应用迁移, got %v", applied)
	}

	var cnt int64
	if err := db.Model(&SchemaMigration{}).Count(&cnt).Error; err != nil {
		t.Fatalf("count versions: %v", err)
	}
	if int(cnt) != len(Migrations()) {
		t.Fatalf("版本行重复: %d", cnt)
	}
	var logs int64
	if err := db.Model(&model.AuditLog{}).Count(&logs).Error; err != nil || logs != 1 {
		t.Fatalf("二次迁移破坏数据: err=%v logs=%d", err, logs)
	}
}

// TestRunMigrations_LegacySessionIndexDropped 验证 0002 挂接正确：
// 遗留的单列 session_key 唯一索引在迁移中被删除（模拟未应用 0002 的旧库）。
func TestRunMigrations_LegacySessionIndexDropped(t *testing.T) {
	db := newMigrateTestDB(t)
	mc := MigrationContext{EncryptionKey: testEncKey()}
	if _, err := RunMigrations(db, mc); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// 造出「旧库」：手工补回遗留索引，并抹掉 0002 版本记录。
	if err := db.Exec("CREATE UNIQUE INDEX idx_legacy_session_key ON sessions(session_key)").Error; err != nil {
		t.Fatalf("create legacy index: %v", err)
	}
	if err := db.Where("version = ?", "0002").Delete(&SchemaMigration{}).Error; err != nil {
		t.Fatalf("delete version row: %v", err)
	}

	applied, err := RunMigrations(db, mc)
	if err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if len(applied) != 1 {
		t.Fatalf("应只补跑 0002, got %v", applied)
	}
	var names []string
	if err := db.Raw(`SELECT name FROM sqlite_master WHERE type='index' AND name='idx_legacy_session_key'`).
		Scan(&names).Error; err != nil {
		t.Fatalf("query index: %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("遗留单列唯一索引未被删除: %v", names)
	}
}

// TestRunMigrations_UnknownVersionIgnored 前向兼容：库里存在代码未知的版本
// （回滚旧二进制的场景）不应报错，也不应重跑已应用版本。
func TestRunMigrations_UnknownVersionIgnored(t *testing.T) {
	db := newMigrateTestDB(t)
	mc := MigrationContext{EncryptionKey: testEncKey()}
	if _, err := RunMigrations(db, mc); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	if err := db.Create(&SchemaMigration{Version: "9999", Name: "from_future"}).Error; err != nil {
		t.Fatalf("insert future version: %v", err)
	}
	applied, err := RunMigrations(db, mc)
	if err != nil {
		t.Fatalf("re-run with unknown version: %v", err)
	}
	if len(applied) != 0 {
		t.Fatalf("不应重跑任何迁移, got %v", applied)
	}
}

// TestDropLegacyMCPColumns_RefusesWhenPlaintextRemains 数据安全护栏：
// 0004 删列前必须确认该列已无明文残留（即 0003 已成功），否则宁可报错中止，
// 绝不能把尚未加密的数据连列一起删掉。
func TestDropLegacyMCPColumns_RefusesWhenPlaintextRemains(t *testing.T) {
	db := newMigrateTestDB(t)
	if _, err := RunMigrations(db, MigrationContext{EncryptionKey: testEncKey()}); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	// 造出「带明文残留的遗留列」。
	if err := db.Exec("ALTER TABLE mcp_servers ADD COLUMN `env` text").Error; err != nil {
		t.Fatalf("add legacy column: %v", err)
	}
	if err := db.Exec(`INSERT INTO mcp_servers
		(created_at, updated_at, user_id, name, transport, url, enabled, env, env_enc, headers_enc)
		VALUES (datetime('now'), datetime('now'), 7, 'leftover', 'sse', 'https://x/sse', 1,
		        '{"TOKEN":"still-plain"}', '', '')`).Error; err != nil {
		t.Fatalf("insert row: %v", err)
	}

	if err := dropLegacyMCPPlaintextColumns(db); err == nil {
		t.Fatal("仍有明文时应拒绝删列")
	}
	// 列与数据都必须原封不动，便于运维修复后重跑。
	var remaining int64
	if err := db.Raw("SELECT COUNT(*) FROM mcp_servers WHERE env IS NOT NULL AND env != ''").
		Scan(&remaining).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("明文数据不应被破坏, got %d", remaining)
	}

	// 明文清空后（等价于 0003 已处理）再删列即应成功，且幂等。
	if err := db.Exec("UPDATE mcp_servers SET env = NULL").Error; err != nil {
		t.Fatalf("clear: %v", err)
	}
	if err := dropLegacyMCPPlaintextColumns(db); err != nil {
		t.Fatalf("清空后应可删列: %v", err)
	}
	hasEnv, _, err := legacyMCPPlaintextColumns(db)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if hasEnv {
		t.Fatal("遗留列未被删除")
	}
	if err := dropLegacyMCPPlaintextColumns(db); err != nil {
		t.Fatalf("二次调用应为 no-op: %v", err)
	}
}

// TestNewDB_UsesVersionedMigrations 端到端：NewDB 默认走 migration（不依赖
// AutoMigrate fallback），建库后 schema_migrations 齐全且角色播种照常生效。
func TestNewDB_UsesVersionedMigrations(t *testing.T) {
	t.Setenv("DB_AUTO_MIGRATE", "") // 明确默认关闭 fallback
	path := filepath.Join(t.TempDir(), "newdb.db")
	db, err := NewDB(&config.Config{DBPath: path, EncryptionKey: testEncKey()})
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, e := db.DB.DB(); e == nil {
			_ = sqlDB.Close()
		}
	})

	rows, err := AppliedMigrations(db.DB)
	if err != nil {
		t.Fatalf("AppliedMigrations: %v", err)
	}
	if len(rows) != len(Migrations()) {
		t.Fatalf("NewDB 未执行全部迁移: %+v", rows)
	}
	if !db.Migrator().HasTable(&model.AuditLog{}) {
		t.Fatal("NewDB 后缺 audit_logs 表")
	}
	var roles int64
	if err := db.Model(&model.Role{}).Count(&roles).Error; err != nil || roles == 0 {
		t.Fatalf("角色播种未生效: err=%v roles=%d", err, roles)
	}
}

// TestRunMigrations_VersionTableIsSourceOfTruth 验收 M6-03：
// schema_migrations 版本表是生产 schema 的唯一真相源——跑完迁移后不应有任何待应用
// 版本，且版本表记录了全部已应用版本。
func TestRunMigrations_VersionTableIsSourceOfTruth(t *testing.T) {
	db := newMigrateTestDB(t)

	applied, err := RunMigrations(db, MigrationContext{EncryptionKey: testEncKey()})
	if err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	if len(applied) != len(Migrations()) {
		t.Fatalf("首次应执行全部 %d 个迁移, got %d: %v", len(Migrations()), len(applied), applied)
	}

	// 版本表存在且记录了全部已应用版本。
	if !db.Migrator().HasTable(&SchemaMigration{}) {
		t.Fatal("schema_migrations 版本表应存在")
	}
	appliedRows, err := AppliedMigrations(db)
	if err != nil {
		t.Fatalf("AppliedMigrations: %v", err)
	}
	if len(appliedRows) != len(Migrations()) {
		t.Fatalf("版本表应记录全部 %d 个版本, got %d", len(Migrations()), len(appliedRows))
	}

	// 全量执行后不应有任何待应用迁移——这正是「版本表即真相源」的体现。
	pending, err := PendingMigrations(db)
	if err != nil {
		t.Fatalf("PendingMigrations: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("全量迁移后不应有待应用版本, got %v", pending)
	}
}
