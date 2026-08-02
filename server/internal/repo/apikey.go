package repo

import (
	"errors"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"gorm.io/gorm"
)

// ErrAPIKeyNotFound is returned when an API key lookup yields no result.
var ErrAPIKeyNotFound = errors.New("api key not found")

// CreateAPIKey persists a new API key.
func CreateAPIKey(db *gorm.DB, ak *model.APIKey) error {
	return db.Create(ak).Error
}

// ListAPIKeysByUser returns all (non-revoked) API keys owned by a user.
func ListAPIKeysByUser(db *gorm.DB, userID uint) ([]model.APIKey, error) {
	var keys []model.APIKey
	if err := db.Where("user_id = ?", userID).Order("created_at desc").Find(&keys).Error; err != nil {
		return nil, err
	}
	return keys, nil
}

// GetAPIKeyByHash finds an API key by its SHA-256 hash.
func GetAPIKeyByHash(db *gorm.DB, hash string) (*model.APIKey, error) {
	var ak model.APIKey
	if err := db.Where("key_hash = ?", hash).First(&ak).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAPIKeyNotFound
		}
		return nil, err
	}
	return &ak, nil
}

// GetAPIKeyByID finds an API key by primary key.
func GetAPIKeyByID(db *gorm.DB, id uint) (*model.APIKey, error) {
	var ak model.APIKey
	if err := db.First(&ak, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAPIKeyNotFound
		}
		return nil, err
	}
	return &ak, nil
}

// RevokeAPIKey soft-deletes an API key (marks it revoked and removes it).
func RevokeAPIKey(db *gorm.DB, id uint) error {
	return db.Delete(&model.APIKey{}, id).Error
}
