package repo

import (
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

// seedRoles inserts default roles if the roles table is empty.
func seedRoles(db *gorm.DB) error {
	var count int64
	if err := db.Model(&model.Role{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	for _, role := range model.SeedRoles() {
		if err := db.Create(&role).Error; err != nil {
			return fmt.Errorf("failed to seed role %q: %w", role.Name, err)
		}
	}
	log.Println("[DB] Seeded default roles: admin, developer, viewer")
	return nil
}
