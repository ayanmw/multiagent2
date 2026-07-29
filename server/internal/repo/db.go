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
		&model.Role{},
		&model.RolePermission{},
	); err != nil {
		return nil, fmt.Errorf("failed to auto-migrate: %w", err)
	}

	// Seed default roles if not present
	if err := seedRoles(db); err != nil {
		return nil, fmt.Errorf("failed to seed roles: %w", err)
	}

	log.Printf("[DB] Connected to SQLite3: %s", cfg.DBPath)
	return &DB{DB: db}, nil
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
