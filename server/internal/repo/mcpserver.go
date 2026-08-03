package repo

import (
	"errors"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"gorm.io/gorm"
)

// ErrMCPServerNotFound 表示 MCP 服务器查找无结果（缺失或不属于当前用户）。
var ErrMCPServerNotFound = errors.New("mcp server not found")

// CreateMCPServer 持久化一个新的 MCP 服务器配置。
func CreateMCPServer(db *gorm.DB, m *model.MCPServer) error {
	return db.Create(m).Error
}

// ListMCPServers 返回某用户归属的全部 MCP 服务器（按创建时间倒序）。
func ListMCPServers(db *gorm.DB, userID uint) ([]model.MCPServer, error) {
	var list []model.MCPServer
	if err := db.Where("user_id = ?", userID).Order("created_at desc").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// GetMCPServerByName 按 (user_id, name) 查重，用于创建前的冲突检测。
// 未找到返回 ErrMCPServerNotFound（供调用方区分「真的没找到」与「其他错误」）。
func GetMCPServerByName(db *gorm.DB, userID uint, name string) (*model.MCPServer, error) {
	var m model.MCPServer
	if err := db.Where("user_id = ? AND name = ?", userID, name).First(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrMCPServerNotFound
		}
		return nil, err
	}
	return &m, nil
}

// GetMCPServerByID 按主键查并校验归属；缺失或越权返回 ErrMCPServerNotFound。
func GetMCPServerByID(db *gorm.DB, userID, id uint) (*model.MCPServer, error) {
	var m model.MCPServer
	if err := db.First(&m, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrMCPServerNotFound
		}
		return nil, err
	}
	if m.UserID != userID {
		return nil, ErrMCPServerNotFound
	}
	return &m, nil
}

// UpdateMCPServer 写入已存在 MCP 服务器配置的变更。
func UpdateMCPServer(db *gorm.DB, m *model.MCPServer) error {
	return db.Save(m).Error
}

// DeleteMCPServer 按主键删除。
func DeleteMCPServer(db *gorm.DB, id uint) error {
	return db.Delete(&model.MCPServer{}, id).Error
}
