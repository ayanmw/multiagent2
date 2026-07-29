package repo

import (
	"errors"
	"fmt"
	"log"

	"github.com/anmingwei/go-multi-agent-v2/internal/config"
	"github.com/anmingwei/go-multi-agent-v2/internal/model"
	"gorm.io/driver/sqlite"
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
		&model.Role{},
		&model.RolePermission{},
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
