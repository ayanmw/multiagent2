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

	// AutoMigrate all models
	if err := db.AutoMigrate(
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
	); err != nil {
		return nil, fmt.Errorf("failed to auto-migrate: %w", err)
	}

	// Seed default roles if not present
	if err := seedRoles(db); err != nil {
		return nil, fmt.Errorf("failed to seed roles: %w", err)
	}

	// 迁移：旧模型曾把 session_key 设为全局单列 uniqueIndex，禁止跨用户复用同
	// key（M0.5-03 已改为复合唯一 (user_id, session_key)）。对已有库删除遗留的
	// 单列唯一索引，避免它继续阻断跨用户复用；复合索引由 AutoMigrate 自动补齐。
	if err := migrateCompositeSessionKey(db); err != nil {
		return nil, fmt.Errorf("failed to migrate session key index: %w", err)
	}

	// 迁移（M3-07）：mcp_servers 的 env/headers 早期以明文 JSON 落库，现改为
	// AES-256-GCM 密文列 env_enc/headers_enc。对已有库把遗留明文就地加密并清空原列。
	if err := migrateMCPSecretEncryption(db, cfg.EncryptionKey); err != nil {
		return nil, fmt.Errorf("failed to migrate mcp secrets: %w", err)
	}

	log.Printf("[DB] Connected to SQLite3: %s", cfg.DBPath)
	return &DB{DB: db}, nil
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

// migrateMCPSecretEncryption 把 mcp_servers 表遗留的明文 env/headers 列就地加密
// 进 env_enc/headers_enc，并将原列置 NULL（M3-07）。
//
// 幂等安全：遗留列不存在（全新库）或已无明文残留时为 no-op；已有密文的行不覆盖。
// 注意 GORM/SQLite 不会自动删除已废弃的列，故遗留列仍在表结构中，只是内容被清空。
func migrateMCPSecretEncryption(db *gorm.DB, encKey []byte) error {
	if !db.Migrator().HasTable("mcp_servers") {
		return nil
	}
	var ddl string
	if err := db.Raw(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'mcp_servers'`).
		Scan(&ddl).Error; err != nil {
		return err
	}
	// 反引号包裹可精确区分遗留列 `env` 与新列 `env_enc`。
	hasEnv := strings.Contains(ddl, "`env`")
	hasHeaders := strings.Contains(ddl, "`headers`")
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
