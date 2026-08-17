package repo

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/ayanmw/multiagent2/server/internal/config"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// DB wraps the GORM database instance.
type DB struct {
	*gorm.DB
}

// NewDB initializes the database connection, runs auto-migration, and seeds default data.
func NewDB(cfg *config.Config) (*DB, error) {
	db, err := gorm.Open(sqlite.Open(cfg.DBPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect database: %w", err)
	}

	// Configure connection pool (sensible defaults for SQLite)
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get underlying sql.DB: %w", err)
	}
	sqlDB.SetMaxOpenConns(1) // SQLite only supports one writer at a time

	// 结构迁移（M3-08）：版本化 migration 取代裸 AutoMigrate。
	// `schema_migrations` 记录已应用版本，启动时只执行尚未应用的部分；
	// 基线（0001）覆盖 M0~M3-07 全部表，历史数据修复（0002/0003）随后按序执行。
	applied, err := RunMigrations(db, MigrationContext{EncryptionKey: cfg.EncryptionKey})
	if err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}
	if len(applied) > 0 {
		log.Printf("[DB] Applied %d migration(s): %s", len(applied), strings.Join(applied, ", "))
	}

	// 启动自检（M6-03）：重申「schema_migrations 版本表是生产 schema 的唯一真相源」。
	// 即便 Dev fallback 开启，也不改变这一事实——它只是本地便利，绝不能替代迁移。
	if err := logMigrationAuthority(db); err != nil {
		log.Printf("[DB] [WARN] 迁移自检失败: %v", err)
	}

	// 开发 fallback（M3-08）：DB_AUTO_MIGRATE=true 时额外跑一次 AutoMigrate，
	// 便于本地改模型时免写 migration 迭代。生产环境务必保持关闭——AutoMigrate
	// 只加表/加列，不能删列、改类型或回填数据，长期使用会让各环境结构漂移，
	// 且绕过 schema_migrations 版本表，使「生产 schema 真相源」这一保证失效。
	if cfg.DBAutoMigrate() {
		log.Println("[WARN] DB_AUTO_MIGRATE=true: 开发期兜底 AutoMigrate 已执行（仅本地改模型用）。" +
			"生产必须 DB_AUTO_MIGRATE=false，schema 由 schema_migrations 版本表统一管理，否则将产生不可控漂移。")
		if err := db.AutoMigrate(baselineModels()...); err != nil {
			return nil, fmt.Errorf("failed to auto-migrate (dev fallback): %w", err)
		}
	}

	// Seed default roles if not present
	if err := seedRoles(db); err != nil {
		return nil, fmt.Errorf("failed to seed roles: %w", err)
	}

	log.Printf("[DB] Connected to SQLite3: %s", cfg.DBPath)
	return &DB{DB: db}, nil
}

// logMigrationAuthority 打印迁移权威声明：schema_migrations 版本表是生产 schema
// 的唯一真相源（M6-03）。用于启动自检，强化「版本化迁移统一管理结构」这一运营约束。
// 返回错误仅用于调用方决定是否打印自检告警，不影响启动。
func logMigrationAuthority(db *gorm.DB) error {
	applied, err := AppliedMigrations(db)
	if err != nil {
		return err
	}
	pending, err := PendingMigrations(db)
	if err != nil {
		return err
	}
	log.Printf("[DB] Schema 真相源 = schema_migrations 版本表：已应用 %d 个版本，待应用 %d 个。",
		len(applied), len(pending))
	if len(pending) > 0 {
		log.Printf("[DB] [WARN] 存在 %d 个未应用迁移（%v）——生产环境出现此情况请确认部署是否滞后。",
			len(pending), pending)
	}
	return nil
}

// migrateCompositeSessionKey 删除 sessions 表上遗留的「单列 session_key 唯一索引」
// （由早期模型 gorm:"uniqueIndex" 产生），保留复合唯一索引 (user_id, session_key)。
// 通过 sql 文本特征判断：含 session_key 但不含 user_id 的唯一索引即为遗留单列索引。
// 幂等安全（DROP INDEX IF EXISTS），无遗留索引时为 no-op。
func migrateCompositeSessionKey(db *gorm.DB) error {
	var names []string
	if err := db.Raw(`SELECT name FROM sqlite_master
		WHERE type = 'index' AND tbl_name = 'sessions'
		AND sql LIKE '%session_key%' AND sql NOT LIKE '%user_id%'`).Scan(&names).Error; err != nil {
		return err
	}
	for _, name := range names {
		if err := db.Exec(fmt.Sprintf("DROP INDEX IF EXISTS `%s`", name)).Error; err != nil {
			return err
		}
		log.Printf("[DB] Dropped legacy single-column unique index on sessions.session_key: %s", name)
	}
	return nil
}

// legacyMCPSecretRow 承载一行待迁移的 MCP 配置（遗留明文 + 现有密文）。
type legacyMCPSecretRow struct {
	ID         uint   `gorm:"column:id"`
	Env        string `gorm:"column:env"`
	Headers    string `gorm:"column:headers"`
	EnvEnc     string `gorm:"column:env_enc"`
	HeadersEnc string `gorm:"column:headers_enc"`
}

// legacyMCPPlaintextColumns 探测 mcp_servers 表上是否还存在 M3-07 之前的明文列
// env / headers。判定依据是建表 DDL 中的**反引号包裹**列名，可精确区分遗留列
// `env` 与现行密文列 `env_enc`（GORM sqlite 的 HasColumn 用宽松 LIKE 匹配，
// 此处不采用以免误判）。表不存在时返回 false, false。
func legacyMCPPlaintextColumns(db *gorm.DB) (hasEnv bool, hasHeaders bool, err error) {
	if !db.Migrator().HasTable("mcp_servers") {
		return false, false, nil
	}
	var ddl string
	if err := db.Raw(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'mcp_servers'`).
		Scan(&ddl).Error; err != nil {
		return false, false, err
	}
	return strings.Contains(ddl, "`env`"), strings.Contains(ddl, "`headers`"), nil
}

// dropLegacyMCPPlaintextColumns 物理删除 mcp_servers 上遗留的明文列 env/headers（M3-08）。
//
// M3-07 的迁移只把明文加密进 env_enc/headers_enc 并将原列置 NULL —— 因为
// `AutoMigrate` 只能加表/加列，删不掉列，空列会长期残留在表结构中。这正是
// M3-08 引入版本化迁移的直接动因，故由本迁移收尾（LEARNINGS 2026-08-10 已记「彻底
// 删列留给 M3-08 的正式迁移机制」）。
//
// 安全性：仅在该列**确认无明文残留**（即 0003 已成功处理）时才删除，否则报错中止，
// 避免把尚未加密的数据一并删掉。幂等：列不存在时为 no-op。
func dropLegacyMCPPlaintextColumns(db *gorm.DB) error {
	hasEnv, hasHeaders, err := legacyMCPPlaintextColumns(db)
	if err != nil {
		return err
	}
	cols := make([]string, 0, 2)
	if hasEnv {
		cols = append(cols, "env")
	}
	if hasHeaders {
		cols = append(cols, "headers")
	}
	for _, col := range cols {
		var remaining int64
		if err := db.Raw(fmt.Sprintf(
			"SELECT COUNT(*) FROM mcp_servers WHERE %s IS NOT NULL AND %s != '' AND %s != 'null'",
			col, col, col)).Scan(&remaining).Error; err != nil {
			return err
		}
		if remaining > 0 {
			return fmt.Errorf("mcp_servers.%s 仍有 %d 行明文未加密，拒绝删列（请先确认 0003 迁移成功）", col, remaining)
		}
		if err := db.Migrator().DropColumn(&model.MCPServer{}, col); err != nil {
			return fmt.Errorf("drop legacy column mcp_servers.%s: %w", col, err)
		}
		log.Printf("[DB] Dropped legacy plaintext column mcp_servers.%s", col)
	}
	return nil
}

// migrateMCPSecretEncryption 把 mcp_servers 表遗留的明文 env/headers 列就地加密
// 进 env_enc/headers_enc，并将原列置 NULL（M3-07）。
//
// 幂等安全：遗留列不存在（全新库）或已无明文残留时为 no-op；已有密文的行不覆盖。
// 本迁移只负责「搬运 + 清空」，遗留列本身由后续的 0004 迁移物理删除
// （见 dropLegacyMCPPlaintextColumns —— AutoMigrate 无删列能力）。
func migrateMCPSecretEncryption(db *gorm.DB, encKey []byte) error {
	hasEnv, hasHeaders, err := legacyMCPPlaintextColumns(db)
	if err != nil {
		return err
	}
	if !hasEnv && !hasHeaders {
		return nil
	}

	// 只挑「遗留列有值」的行；'null' 是 GORM serializer:json 对 nil map 的落库形态。
	notEmpty := func(col string) string {
		return fmt.Sprintf("(%s IS NOT NULL AND %s != '' AND %s != 'null')", col, col, col)
	}
	selectCols := []string{"id", "env_enc", "headers_enc"}
	var conds []string
	if hasEnv {
		selectCols = append(selectCols, "env")
		conds = append(conds, notEmpty("env"))
	}
	if hasHeaders {
		selectCols = append(selectCols, "headers")
		conds = append(conds, notEmpty("headers"))
	}
	var rows []legacyMCPSecretRow
	query := fmt.Sprintf("SELECT %s FROM mcp_servers WHERE %s",
		strings.Join(selectCols, ", "), strings.Join(conds, " OR "))
	if err := db.Raw(query).Scan(&rows).Error; err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	if len(encKey) != 32 {
		return fmt.Errorf("found %d mcp_servers rows with plaintext secrets but encryption key is unavailable "+
			"(set PROVIDER_ENC_KEY or JWT_SECRET)", len(rows))
	}

	// 清空遗留列（哪些列存在就清哪些），避免明文继续留在库里。
	clearCols := ""
	if hasEnv {
		clearCols += ", env = NULL"
	}
	if hasHeaders {
		clearCols += ", headers = NULL"
	}
	migrated := 0
	for _, r := range rows {
		tmp := model.MCPServer{}
		if r.EnvEnc == "" {
			tmp.Env = decodeLegacySecretMap(r.Env)
		}
		if r.HeadersEnc == "" {
			tmp.Headers = decodeLegacySecretMap(r.Headers)
		}
		if err := tmp.SealSecrets(encKey); err != nil {
			return fmt.Errorf("encrypt mcp_servers row %d: %w", r.ID, err)
		}
		envEnc, headersEnc := r.EnvEnc, r.HeadersEnc
		if envEnc == "" {
			envEnc = tmp.EnvEnc
		}
		if headersEnc == "" {
			headersEnc = tmp.HeadersEnc
		}
		if err := db.Exec(
			"UPDATE mcp_servers SET env_enc = ?, headers_enc = ?"+clearCols+" WHERE id = ?",
			envEnc, headersEnc, r.ID,
		).Error; err != nil {
			return err
		}
		migrated++
	}
	log.Printf("[DB] Encrypted plaintext env/headers for %d mcp_servers row(s)", migrated)
	return nil
}

// decodeLegacySecretMap 解析遗留列中的 JSON 明文；非法/空值返回 nil（当作未配置）。
func decodeLegacySecretMap(raw string) map[string]string {
	if raw == "" || raw == "null" {
		return nil
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		log.Printf("[WARN] mcp_servers: 跳过无法解析的遗留明文密钥字段: %v", err)
		return nil
	}
	return out
}

// seedRoles ensures the default roles and their permissions exist. It is
// idempotent: existing roles keep their current permissions and any permission
// declared in model.SeedRoles but missing from the DB is added (without
// removing anything). This lets role changes (e.g. granting developer new
// write permissions) take effect on already-initialized databases.
func seedRoles(db *gorm.DB) error {
	for _, seeded := range model.SeedRoles() {
		var existing model.Role
		err := db.Where("name = ?", seeded.Name).First(&existing).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				if err := db.Create(&seeded).Error; err != nil {
					return fmt.Errorf("failed to seed role %q: %w", seeded.Name, err)
				}
				continue
			}
			return err
		}
		// Role already exists: add any declared permission not yet present.
		for _, p := range seeded.Permissions {
			var cnt int64
			if err := db.Model(&model.RolePermission{}).
				Where("role_id = ? AND resource = ? AND action = ?", existing.ID, p.Resource, p.Action).
				Count(&cnt).Error; err != nil {
				return err
			}
			if cnt == 0 {
				if err := db.Create(&model.RolePermission{
					RoleID:   existing.ID,
					Resource: p.Resource,
					Action:   p.Action,
				}).Error; err != nil {
					return fmt.Errorf("failed to add permission %s:%s for role %q: %w", p.Resource, p.Action, seeded.Name, err)
				}
			}
		}
	}
	log.Println("[DB] Ensured default roles: admin, developer, viewer")
	return nil
}
