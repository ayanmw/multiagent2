package repo

import (
	"github.com/ayanmw/multiagent2/server/internal/model"
	"gorm.io/gorm"
)

// 本文件是 AutomationRun 的唯一持久化出口（M4-05 跨天恢复的运行记录）。

// CreateAutomationRun 持久化一次自动化运行记录（status=running）。
func CreateAutomationRun(db *gorm.DB, run *model.AutomationRun) error {
	return db.Create(run).Error
}

// ListUnfinishedAutomationRuns 返回所有「运行中（running）」的自动化运行，
// 即进程重启/中断后尚未收敛的 Goal Session（M4-05 恢复扫描入口）。
func ListUnfinishedAutomationRuns(db *gorm.DB) ([]model.AutomationRun, error) {
	var list []model.AutomationRun
	if err := db.Where("status = ?", model.RunStatusRunning).Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// ListAutomationRuns 返回某用户归属的全部运行记录（按创建时间倒序，诊断/测试用）。
func ListAutomationRuns(db *gorm.DB, userID uint) ([]model.AutomationRun, error) {
	var list []model.AutomationRun
	if err := db.Where("user_id = ?", userID).Order("created_at desc").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// MarkAutomationRunStatus 更新运行记录的状态/错误/重试次数（幂等，供恢复扫描与调度器共用）。
// attempts 为当前累计重试次数（done 时传 0 即可，failed/续跑保留时传累计值）。
func MarkAutomationRunStatus(db *gorm.DB, id uint, status, errMsg string, attempts int) error {
	return db.Model(&model.AutomationRun{}).Where("id = ?", id).
		Updates(map[string]any{
			"status":   status,
			"error":    errMsg,
			"attempts": attempts,
		}).Error
}
