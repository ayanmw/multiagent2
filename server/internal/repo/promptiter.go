package repo

import (
	"errors"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"gorm.io/gorm"
)

// promptiter 运行记录相关错误（owner 隔离：跨用户直接 404）。
var (
	ErrPromptIterRunNotFound = errors.New("promptiter run not found")
)

// CreatePromptIterRun 插入一条优化运行记录（默认 pending）。
func CreatePromptIterRun(db *gorm.DB, run *model.PromptIterRun) error {
	return db.Create(run).Error
}

// GetPromptIterRun 按 id + owner 取一条运行记录（owner 隔离）。
func GetPromptIterRun(db *gorm.DB, userID, id uint) (*model.PromptIterRun, error) {
	var run model.PromptIterRun
	err := db.Where("user_id = ? AND id = ?", userID, id).First(&run).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrPromptIterRunNotFound
		}
		return nil, err
	}
	return &run, nil
}

// ListPromptIterRuns 列出该用户的全部优化运行（按 id 降序，最新在前）。
func ListPromptIterRuns(db *gorm.DB, userID uint) ([]model.PromptIterRun, error) {
	var list []model.PromptIterRun
	if err := db.Where("user_id = ?", userID).Order("id DESC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// UpdatePromptIterRun 原地更新一条运行记录（状态/分数/内容变更）。
func UpdatePromptIterRun(db *gorm.DB, run *model.PromptIterRun) error {
	return db.Save(run).Error
}
