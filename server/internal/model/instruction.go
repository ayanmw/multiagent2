package model

import (
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
)

// AgentInstruction 是「可优化并写回」的 Agent 系统提示词（M5-06 promptiter）。
//
// 设计目标：把原本硬编码在 engine/codeagent 的 Agent 指令外置为「按用户归属、按名称
// 区分角色」的可持久化条目，使 promptiter 的 GEPA 反射式优化能把改进后的提示词落库，
// 并在单代理对话（默认 AGENT_MODE=single，也是 24h 自主 Loop 的实际模式）中经
// engine.ModelConfig.InstructionOverride 生效。
//
// 字段说明：
//   - UserID: 归属用户（owner-scoped CRUD，越权即 404）
//   - Name:   指令名称（同一用户内唯一）；生产引擎固定读取名为 "default" 的单代理指令
//   - Role:   角色标签（single/orchestrator/coder，预留；当前仅 single 经引擎注入生效）
//   - Content: 指令全文（写回的内容，可能为空表示回退引擎内置默认）
//   - Version: 每次写回自增，便于回滚与审计
type AgentInstruction struct {
	gorm.Model
	UserID  uint   `gorm:"not null;index" json:"user_id"`
	Name    string `gorm:"size:128;not null;uniqueIndex:idx_user_instruction,priority:1" json:"name"`
	Role    string `gorm:"size:32;not null;default:single" json:"role"`
	Content string `gorm:"type:text" json:"content"`
	Version int    `gorm:"not null;default:1" json:"version"`
}

// TableName 固定表名，避免 GORM 复数化规则变化影响既有库。
func (AgentInstruction) TableName() string { return "agent_instructions" }

// DefaultInstructionName 是生产引擎默认读取的单代理指令名称。
const DefaultInstructionName = "default"

// Validate 校验指令自洽性：名称必填且合法、角色合法。
func (i *AgentInstruction) Validate() error {
	if strings.TrimSpace(i.Name) == "" {
		return errors.New("指令名称不能为空")
	}
	if len(i.Name) > 128 {
		return errors.New("指令名称过长（上限 128）")
	}
	switch i.Role {
	case "single", "orchestrator", "coder", "":
		// 合法；空视为 single
	default:
		return errors.New("非法的指令角色（应为 single / orchestrator / coder）")
	}
	if len(i.Content) > 1<<20 {
		return errors.New("指令内容过长")
	}
	return nil
}

// AgentInstructionView 是 AgentInstruction 的对外视图（不含 gorm.Model 噪声）。
type AgentInstructionView struct {
	ID        uint      `json:"id"`
	UserID    uint      `json:"user_id"`
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ToAgentInstructionView 把模型转为对外视图。
func (i *AgentInstruction) ToAgentInstructionView() AgentInstructionView {
	return AgentInstructionView{
		ID:        i.ID,
		UserID:    i.UserID,
		Name:      i.Name,
		Role:      i.Role,
		Content:   i.Content,
		Version:   i.Version,
		CreatedAt: i.CreatedAt,
		UpdatedAt: i.UpdatedAt,
	}
}
