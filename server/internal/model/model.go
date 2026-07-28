package model

import "gorm.io/gorm"

// Model represents a user-managed model entry under a Provider. Unlike the
// transient model list returned by upstream discovery (M0-08), a Model row is
// persisted so the user can enable/disable it and mark one as the provider's
// default. Agent configuration may only select models that are enabled.
//
// A provider may have at most one default model (IsDefault); enabling/disabling
// is fully independent of the default flag.
type Model struct {
	gorm.Model
	ProviderID uint   `gorm:"not null;index;uniqueIndex:uniq_provider_model" json:"provider_id"`
	UserID     uint   `gorm:"not null;index" json:"user_id"`
	ModelID    string `gorm:"size:256;not null;uniqueIndex:uniq_provider_model" json:"model_id"` // upstream model id
	Name       string `gorm:"size:256" json:"name"`
	OwnedBy    string `gorm:"size:128" json:"owned_by"`
	Enabled    bool   `gorm:"not null;default:false" json:"enabled"`
	IsDefault  bool   `gorm:"not null;default:false" json:"is_default"`
}

// TableName overrides the default GORM table name.
func (Model) TableName() string {
	return "models"
}
