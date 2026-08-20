package model

import "gorm.io/gorm"

// UsageRecord 记录一次对话（或 SubAgent 调用）的 token 用量（M3-03 Token/费用计量）。
// 归属到 user / session / provider / model，便于按维度聚合成本。
//
// 优先级：优先读上游响应里的 usage（网关/OpenAI 兼容协议由框架 model.Usage 透传）；
// 若上游未给（如本地 mock 网关），api 层会用 engine.EstimateUsage 做本地粗估并以
// Estimated=true 标记，保证 usage_records 始终有可观测的行。
type UsageRecord struct {
	gorm.Model
	UserID     uint   `gorm:"not null;index" json:"user_id"`       // 归属用户（owner 隔离）
	SessionID  uint   `gorm:"not null;index" json:"session_id"`    // 会话 DB id（repo.Session.ID）
	SessionKey string `gorm:"index" json:"session_key"`            // 会话业务 key（便于按对话聚合）
	// WorkspaceKey 是会话绑定的 workspace key（M8-09，可空）：供 workspace 作用域
	// 预算聚合；无绑定的会话（默认用户目录）留空，不参与 workspace 聚合。
	WorkspaceKey string `gorm:"index" json:"workspace_key,omitempty"`
	ProviderID uint   `gorm:"index" json:"provider_id"`            // 上游 Provider id
	ModelID    uint   `gorm:"index" json:"model_id"`               // 模型 id
	ModelName  string `json:"model_name"`                          // 模型展示名（冗余，便于展示）
	PromptTokens     int  `json:"prompt_tokens"`                   // 提示词 token 数
	CompletionTokens int  `json:"completion_tokens"`               // 补全 token 数
	TotalTokens      int  `json:"total_tokens"`                    // 合计 token 数
	Estimated  bool   `gorm:"not null;default:false" json:"estimated"` // true=上游未给 usage，本地估算
}

// TableName overrides the default GORM table name.
func (UsageRecord) TableName() string { return "usage_records" }
