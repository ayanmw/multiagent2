package repo

import (
	"errors"

	"github.com/anmingwei/go-multi-agent-v2/internal/model"
	"gorm.io/gorm"
)

// ErrModelNotFound is returned when a managed model lookup yields no result.
var ErrModelNotFound = errors.New("model not found")

// UpsertModel inserts a new managed model or, when a row already exists for the
// same (provider_id, model_id) pair, refreshes its descriptive fields (Name,
// OwnedBy) while preserving the user's enable/default choices. On return the
// input model is updated to reflect the persisted row (id, enabled, is_default),
// mirroring GORM's Create behavior.
func UpsertModel(db *gorm.DB, m *model.Model) error {
	var existing model.Model
	err := db.Where("provider_id = ? AND model_id = ?", m.ProviderID, m.ModelID).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.Create(m).Error
	}
	if err != nil {
		return err
	}
	// Keep the row's primary key, enabled and is_default flags; only refresh
	// the discoverable metadata.
	existing.Name = m.Name
	existing.OwnedBy = m.OwnedBy
	if err := db.Save(&existing).Error; err != nil {
		return err
	}
	// Reflect the persisted state back onto the caller's struct.
	m.ID = existing.ID
	m.Enabled = existing.Enabled
	m.IsDefault = existing.IsDefault
	return nil
}

// ListModelsByProvider returns all managed models for a provider owned by a user.
func ListModelsByProvider(db *gorm.DB, providerID, userID uint) ([]model.Model, error) {
	var list []model.Model
	if err := db.Where("provider_id = ? AND user_id = ?", providerID, userID).
		Order("is_default desc, name asc").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// ListEnabledModels returns every enabled model owned by a user across all of
// their providers. This is the pool an Agent may select from (M0-10).
func ListEnabledModels(db *gorm.DB, userID uint) ([]model.Model, error) {
	var list []model.Model
	if err := db.Where("user_id = ? AND enabled = ?", userID, true).
		Order("provider_id asc, is_default desc, name asc").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// GetModelByID loads a managed model by primary key and verifies ownership.
func GetModelByID(db *gorm.DB, id, userID uint) (*model.Model, error) {
	var m model.Model
	if err := db.First(&m, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrModelNotFound
		}
		return nil, err
	}
	if m.UserID != userID {
		return nil, ErrModelNotFound
	}
	return &m, nil
}

// PatchModel updates the enable/default flags of a managed model. When a model is
// marked default, any other default in the same provider is cleared so that a
// provider keeps at most one default. The update runs in a transaction.
func PatchModel(db *gorm.DB, m *model.Model, enabled, isDefault *bool) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if isDefault != nil && *isDefault {
			if err := tx.Model(&model.Model{}).
				Where("provider_id = ? AND id <> ?", m.ProviderID, m.ID).
				Update("is_default", false).Error; err != nil {
				return err
			}
		}
		if enabled != nil {
			m.Enabled = *enabled
		}
		if isDefault != nil {
			m.IsDefault = *isDefault
		}
		return tx.Save(m).Error
	})
}
