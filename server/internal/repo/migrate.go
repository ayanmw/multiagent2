package repo

import (
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// 版本化 DB 迁移机制（M3-08）
//
// 背景：M0~M3-07 期间表结构完全依赖 `AutoMigrate`，它只会「加表/加列」，无法
// 删列、改类型、回填数据，也没有版本记录 —— 生产环境不可控。M3-08 引入最小可用
// 的版本化迁移：`schema_migrations` 版本表 + 有序 migration 列表，启动时只执行
// 尚未应用的版本，`AutoMigrate` 降级为「开发期 fallback」（env DB_AUTO_MIGRATE）。
//
// 新增一次结构变更的正确姿势（务必遵守，勿再直接依赖 AutoMigrate）：
//  1. 修改 `internal/model` 中的结构体；
//  2. 在 `Migrations()` 末尾追加一条新版本（版本号递增、四位数字前缀），
//     在 `Up` 里用 `db.Migrator().AddColumn/AlterColumn/DropColumn` 或对**单个**
//     模型调用 `db.AutoMigrate(&model.X{})` 完成变更，必要时回填数据；
//  3. `baselineModels()` 保持为「当前全部模型」，使全新库经基线一次建成最新结构；
//  4. 迁移函数**必须幂等**（见下方「为什么不用事务」）。
//
// 为什么不用事务包裹：SQLite 上 GORM 的 DDL（尤其是重建表）会自行处理外键
// PRAGMA，包在事务里易触发驱动层限制；且本项目迁移全部为「幂等结构操作 + 幂等
// 数据回填」。故采用「执行成功后才写版本行」的策略：中途失败时版本行不落库，
// 下次启动重跑同一条迁移，因幂等而安全。
// ---------------------------------------------------------------------------

// SchemaMigration 是 `schema_migrations` 版本表的一行，记录某个迁移版本已应用。
type SchemaMigration struct {
	Version   string    `gorm:"column:version;primaryKey;size:64" json:"version"`
	Name      string    `gorm:"column:name;size:191;not null" json:"name"`
	AppliedAt time.Time `gorm:"column:applied_at;not null" json:"applied_at"`
}

// TableName 固定版本表名，避免 GORM 复数化规则变化影响既有库。
func (SchemaMigration) TableName() string { return "schema_migrations" }

// MigrationContext 承载迁移执行期需要的外部依赖，避免迁移函数直连 config 包
// （repo 已依赖 config，但迁移应尽量只吃「值」，便于单测构造）。
type MigrationContext struct {
	// EncryptionKey 是 AES-256-GCM 主密钥，供需要就地加密历史数据的迁移使用。
	EncryptionKey []byte
}

// Migration 描述一个不可变的结构/数据迁移步骤。
//
// Version 一经发布不得修改（已应用的库不会重跑），语义变更请追加新版本。
type Migration struct {
	Version string
	Name    string
	Up      func(db *gorm.DB, mc MigrationContext) error
}

// baselineModels 返回当前全部持久化模型。
//
// 用途有二：① `0001_baseline` 基线迁移据此一次性建出与当前代码一致的表结构；
// ② `DB_AUTO_MIGRATE=true` 的开发 fallback。生产路径不应依赖它做增量变更。
func baselineModels() []any {
	return []any{
		&model.User{},
		&model.APIKey{},
		&model.Provider{},
		&model.Model{},
		&model.Session{},
		&model.Message{},
		&model.Workspace{},
		&model.MCPServer{},
		&model.Role{},
		&model.RolePermission{},
		&model.AuditLog{},
		&model.UsageRecord{},
		&model.BudgetPolicy{},
		&model.Checkpoint{},
		&model.Automation{},
		&model.AutomationRun{},
		&model.Notification{},
		&model.KnowledgeBase{},
		&model.SkillCandidate{},
		&model.EvalDataset{},
		&model.EvalCase{},
		&model.EvalRun{},
		&model.EvalResult{},
		&model.AgentInstruction{},
		&model.PromptIterRun{},
	}
}

// Migrations 返回按版本升序排列的全部迁移。
//
// 顺序即执行顺序；`RunMigrations` 会再做一次升序校验，防止手写时插错位置。
func Migrations() []Migration {
	return []Migration{
		{
			Version: "0001",
			Name:    "baseline_schema",
			// 基线：建出 M3-08 时刻（含 M0~M3-07 全部表）的完整结构。
			// 对已有库而言 AutoMigrate 是幂等的「补齐缺失表/列」，不会破坏数据。
			Up: func(db *gorm.DB, _ MigrationContext) error {
				return db.AutoMigrate(baselineModels()...)
			},
		},
		{
			Version: "0002",
			Name:    "drop_legacy_session_key_unique_index",
			// M0.5-03：早期 sessions.session_key 是全局单列唯一索引，禁止跨用户
			// 复用同 key；改为复合唯一 (user_id, session_key) 后需删除遗留索引。
			Up: func(db *gorm.DB, _ MigrationContext) error {
				return migrateCompositeSessionKey(db)
			},
		},
		{
			Version: "0003",
			Name:    "encrypt_mcp_server_secrets",
			// M3-07：mcp_servers 的 env/headers 由明文 JSON 改为 AES-256-GCM 密文列。
			Up: func(db *gorm.DB, mc MigrationContext) error {
				return migrateMCPSecretEncryption(db, mc.EncryptionKey)
			},
		},
		{
			Version: "0004",
			Name:    "drop_legacy_mcp_plaintext_columns",
			// M3-07 遗留：明文列被置 NULL 但删不掉（AutoMigrate 无删列能力），
			// 由本迁移彻底移除 —— 也是「为什么需要正式迁移机制」的直接例证。
			// 必须排在 0003 之后：先加密搬运，再删源列。
			Up: func(db *gorm.DB, _ MigrationContext) error {
				return dropLegacyMCPPlaintextColumns(db)
			},
		},
		{
			Version: "0005",
			Name:    "add_automation_runs",
			// M4-05：新增 automation_runs 表，记录每次自动化 Loop 运行的生命周期
			// （running/done/failed + attempts + channel），作为「跨重启恢复」的
			// 唯一真相源（目标契约收敛状态原本只活在内存储存，重启即丢失）。
			Up: func(db *gorm.DB, _ MigrationContext) error {
				return db.AutoMigrate(&model.AutomationRun{})
			},
		},
		{
			Version: "0006",
			Name:    "add_notifications",
			// M4-07：新增 notifications 表（站内信），作为「通知/结果回发」的落点。
			// 自主化 Loop（cron/webhook/recover）完成/失败/需检查点时写入，前端通知中心消费。
			Up: func(db *gorm.DB, _ MigrationContext) error {
				return db.AutoMigrate(&model.Notification{})
			},
		},
		{
			Version: "0007",
			Name:    "add_knowledge_bases",
			// M5-02：新增 knowledge_bases 表（用户私有知识库元数据）。
			// 实际切片向量存于独立的 kb_vectors 表（由 knowledge 包在构造时自管），
			// 此处只持久化知识库的归属/名称/统计等元数据。
			Up: func(db *gorm.DB, _ MigrationContext) error {
				return db.AutoMigrate(&model.KnowledgeBase{})
			},
		},
		{
			Version: "0008",
			Name:    "add_skill_candidates",
			// M5-03：新增 skill_candidates 表（进化技能飞轮产出的候选技能）。
			// 后台扫描 session transcript → LLM 提取候选 SKILL.md → 质量门控 →
			// 落库 pending，等待人工审批（M5-04）后发布为托管技能。
			Up: func(db *gorm.DB, _ MigrationContext) error {
				return db.AutoMigrate(&model.SkillCandidate{})
			},
		},
		{
			Version: "0009",
			Name:    "add_eval_tables",
			// M5-05：新增评估回归四表（eval_datasets / eval_cases / eval_runs /
			// eval_results）。一次模型/Prompt 改动后跑评估集对比「稳定分」判断退步。
			// 全部 owner-scoped（user_id 归属隔离），不依赖 AutoMigrate fallback。
			Up: func(db *gorm.DB, _ MigrationContext) error {
				return db.AutoMigrate(&model.EvalDataset{}, &model.EvalCase{}, &model.EvalRun{}, &model.EvalResult{})
			},
		},
		{
			Version: "0010",
			Name:    "add_agent_instructions",
			// M5-06：新增 agent_instructions 表（可优化的 Agent 系统提示词，owner-scoped）。
			// promptiter 的 GEPA 反射式优化把改进后提示词写回此表，单代理对话经
			// engine.ModelConfig.InstructionOverride 注入生效。
			Up: func(db *gorm.DB, _ MigrationContext) error {
				return db.AutoMigrate(&model.AgentInstruction{})
			},
		},
		{
			Version: "0011",
			Name:    "add_promptiter_runs",
			// M5-06：新增 promptiter_runs 表（GEPA 反射式优化运行记录，owner-scoped）。
			// 落库 baseline/candidate 分数、优化前后指令全文与改进理由，支撑「可读、可回滚」。
			Up: func(db *gorm.DB, _ MigrationContext) error {
				return db.AutoMigrate(&model.PromptIterRun{})
			},
		},
	}
}

// RunMigrations 执行所有尚未应用的迁移，返回本次实际应用的版本号列表。
//
// 幂等：已应用的版本会被跳过；DB 中存在但代码里没有的版本会被忽略（前向兼容，
// 便于回滚二进制而不炸库）。
func RunMigrations(db *gorm.DB, mc MigrationContext) ([]string, error) {
	if db == nil {
		return nil, fmt.Errorf("migrate: nil db")
	}
	migrations := Migrations()
	if err := validateMigrations(migrations); err != nil {
		return nil, err
	}
	if err := db.AutoMigrate(&SchemaMigration{}); err != nil {
		return nil, fmt.Errorf("migrate: ensure schema_migrations table: %w", err)
	}

	applied, err := appliedVersions(db)
	if err != nil {
		return nil, err
	}

	var done []string
	for _, m := range migrations {
		if applied[m.Version] {
			continue
		}
		if m.Up != nil {
			if err := m.Up(db, mc); err != nil {
				return done, fmt.Errorf("migrate %s_%s: %w", m.Version, m.Name, err)
			}
		}
		row := SchemaMigration{Version: m.Version, Name: m.Name, AppliedAt: time.Now().UTC()}
		if err := db.Create(&row).Error; err != nil {
			return done, fmt.Errorf("migrate: record version %s: %w", m.Version, err)
		}
		done = append(done, m.Version+"_"+m.Name)
		log.Printf("[DB] Applied migration %s_%s", m.Version, m.Name)
	}
	return done, nil
}

// AppliedMigrations 返回已应用的迁移记录（按版本升序），供运维/诊断查询。
func AppliedMigrations(db *gorm.DB) ([]SchemaMigration, error) {
	if db == nil {
		return nil, fmt.Errorf("migrate: nil db")
	}
	if !db.Migrator().HasTable(&SchemaMigration{}) {
		return nil, nil
	}
	var rows []SchemaMigration
	if err := db.Order("version ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// PendingMigrations 返回尚未应用的迁移版本号（不执行），便于启动前自检。
func PendingMigrations(db *gorm.DB) ([]string, error) {
	applied, err := appliedVersions(db)
	if err != nil {
		return nil, err
	}
	var pending []string
	for _, m := range Migrations() {
		if !applied[m.Version] {
			pending = append(pending, m.Version+"_"+m.Name)
		}
	}
	return pending, nil
}

// appliedVersions 读取版本表；表不存在视作「一条都没应用」。
func appliedVersions(db *gorm.DB) (map[string]bool, error) {
	out := map[string]bool{}
	if db == nil || !db.Migrator().HasTable(&SchemaMigration{}) {
		return out, nil
	}
	var rows []SchemaMigration
	if err := db.Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("migrate: load applied versions: %w", err)
	}
	for _, r := range rows {
		out[r.Version] = true
	}
	return out, nil
}

// validateMigrations 校验版本号非空、唯一且严格升序（手写列表的防呆）。
func validateMigrations(ms []Migration) error {
	seen := map[string]bool{}
	versions := make([]string, 0, len(ms))
	for _, m := range ms {
		if m.Version == "" {
			return fmt.Errorf("migrate: empty version in migration %q", m.Name)
		}
		if seen[m.Version] {
			return fmt.Errorf("migrate: duplicated version %q", m.Version)
		}
		seen[m.Version] = true
		versions = append(versions, m.Version)
	}
	if !sort.StringsAreSorted(versions) {
		return fmt.Errorf("migrate: versions must be listed in ascending order, got %v", versions)
	}
	return nil
}
