package repo

import (
	"errors"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"gorm.io/gorm"
)

// 指令相关错误（owner 隔离：跨用户直接 404，不暴露存在性）。
var (
	ErrInstructionNotFound = errors.New("instruction not found")
)

// CreateOrUpdateInstruction 按 (user_id, name) upsert 一条 Agent 指令：
// 已存在则覆盖 Content 并自增 Version；不存在则新建（默认 role=single）。
// 供 promptiter 把优化后的提示词写回，也供前端指令管理创建/更新复用。
func CreateOrUpdateInstruction(db *gorm.DB, userID uint, name, role, content string) (*model.AgentInstruction, error) {
	if name == "" {
		name = model.DefaultInstructionName
	}
	if role == "" {
		role = "single"
	}
	var existing model.AgentInstruction
	err := db.Where("user_id = ? AND name = ?", userID, name).First(&existing).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			ins := &model.AgentInstruction{
				UserID:  userID,
				Name:    name,
				Role:    role,
				Content: content,
				Version: 1,
			}
			if verr := ins.Validate(); verr != nil {
				return nil, verr
			}
			if cerr := db.Create(ins).Error; cerr != nil {
				return nil, cerr
			}
			return ins, nil
		}
		return nil, err
	}
	existing.Content = content
	existing.Role = role
	existing.Version++
	if verr := existing.Validate(); verr != nil {
		return nil, verr
	}
	if uerr := db.Save(&existing).Error; uerr != nil {
		return nil, uerr
	}
	return &existing, nil
}

// GetInstruction 按 (user_id, name) 取一条指令（owner 隔离）。
func GetInstruction(db *gorm.DB, userID uint, name string) (*model.AgentInstruction, error) {
	if name == "" {
		name = model.DefaultInstructionName
	}
	var ins model.AgentInstruction
	err := db.Where("user_id = ? AND name = ?", userID, name).First(&ins).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrInstructionNotFound
		}
		return nil, err
	}
	return &ins, nil
}

// GetInstructionContent 返回该用户指定名称指令的 Content；不存在时返回 ""（引擎回退内置默认）。
// 用于生产引擎注入 InstructionOverride：空字符串即「不覆盖」。
func GetInstructionContent(db *gorm.DB, userID uint, name string) (string, error) {
	ins, err := GetInstruction(db, userID, name)
	if err != nil {
		if errors.Is(err, ErrInstructionNotFound) {
			return "", nil
		}
		return "", err
	}
	return ins.Content, nil
}

// ListInstructions 列出该用户的全部指令（按 name 升序）。
func ListInstructions(db *gorm.DB, userID uint) ([]model.AgentInstruction, error) {
	var list []model.AgentInstruction
	if err := db.Where("user_id = ?", userID).Order("name ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// UpdateInstructionContent 仅更新已有指令的 Content 并自增 Version（不存在返回 404）。
func UpdateInstructionContent(db *gorm.DB, userID uint, name, content string) (*model.AgentInstruction, error) {
	ins, err := GetInstruction(db, userID, name)
	if err != nil {
		return nil, err
	}
	ins.Content = content
	ins.Version++
	if verr := ins.Validate(); verr != nil {
		return nil, verr
	}
	if uerr := db.Save(ins).Error; uerr != nil {
		return nil, uerr
	}
	return ins, nil
}
