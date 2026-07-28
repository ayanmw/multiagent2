package model

import "gorm.io/gorm"

// ProviderProtocol enumerates the LLM provider protocols supported by the
// platform. The protocol determines how the model list is discovered (M0-08)
// and how requests are shaped by the agent engine (M0-10).
type ProviderProtocol string

const (
	ProtocolOpenAI    ProviderProtocol = "openai"
	ProtocolAnthropic ProviderProtocol = "anthropic"
	ProtocolGemini    ProviderProtocol = "gemini"
)

// ParseProtocol validates and normalizes a protocol string.
func ParseProtocol(s string) (ProviderProtocol, bool) {
	switch ProviderProtocol(s) {
	case ProtocolOpenAI, ProtocolAnthropic, ProtocolGemini:
		return ProviderProtocol(s), true
	}
	return "", false
}

// Provider represents a user-owned LLM provider configuration. The API key is
// encrypted at rest with AES-256-GCM; only the ciphertext (APIKeyEnc) is
// persisted, never the plaintext. Providers are scoped to the user that
// created them so each account can bring its own LLM credentials.
type Provider struct {
	gorm.Model
	UserID      uint            `gorm:"not null;index" json:"user_id"`
	Name        string          `gorm:"size:128;not null" json:"name"`
	Protocol    ProviderProtocol `gorm:"size:32;not null;default:openai" json:"protocol"`
	BaseURL     string          `gorm:"size:512" json:"base_url"`
	APIKeyEnc   string          `gorm:"size:1024" json:"-"` // AES-GCM ciphertext (base64), never exposed
	Description string          `gorm:"size:512" json:"description"`
	Status      string          `gorm:"size:16;not null;default:active" json:"status"`
}

// TableName overrides the default GORM table name.
func (Provider) TableName() string {
	return "providers"
}
