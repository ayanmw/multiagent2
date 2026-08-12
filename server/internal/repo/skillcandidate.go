package repo

import (
	"errors"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"gorm.io/gorm"
)

// CreateSkillCandidate 创建一条候选技能（owner 隔离，落库归属用户）。
func CreateSkillCandidate(db *gorm.DB, c *model.SkillCandidate) error {
	if err := c.Validate(); err != nil {
		return err
	}
	return db.Create(c).Error
}

// GetSkillCandidate 按 id 取候选（owner 隔离：非归属用户返回 nil）。
func GetSkillCandidate(db *gorm.DB, id, userID uint) (*model.SkillCandidate, error) {
	var c model.SkillCandidate
	if err := db.First(&c, "id = ? AND user_id = ?", id, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

// ListSkillCandidates 列出某用户的候选技能，可按 status 过滤（空串表示全部）。
// 按创建时间倒序（最近提取的排前）。分页由 limit/offset 控制（limit<=0 不限）。
func ListSkillCandidates(db *gorm.DB, userID uint, status string, limit, offset int) ([]model.SkillCandidate, error) {
	q := db.Where("user_id = ?", userID)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	q = q.Order("created_at DESC, id DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	if offset > 0 {
		q = q.Offset(offset)
	}
	var list []model.SkillCandidate
	if err := q.Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// UpdateSkillCandidateStatus 流转候选技能状态（owner 隔离）。status 须为合法值；
// 当 decision=rejected 时 rejectReason 一并写入；approved 清空 reject_reason。
// 未找到返回 nil（调用方据此视为 404）。
func UpdateSkillCandidateStatus(db *gorm.DB, id, userID uint, status model.SkillCandidateStatus, rejectReason string) (*model.SkillCandidate, error) {
	c, err := GetSkillCandidate(db, id, userID)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, nil
	}
	c.Status = status
	if status == model.SkillCandidateRejected {
		c.RejectReason = rejectReason
	} else if status == model.SkillCandidateApproved {
		c.RejectReason = ""
	}
	if err := db.Save(c).Error; err != nil {
		return nil, err
	}
	return c, nil
}

// ExistsCandidateForSession 判断某个会话是否已被提取过（任何非 rejected 状态均视为已处理，
// 避免重复扫描）。扫描器据此跳过已处理会话。
func ExistsCandidateForSession(db *gorm.DB, userID uint, sessionKey string) (bool, error) {
	if sessionKey == "" {
		return false, nil
	}
	var cnt int64
	if err := db.Model(&model.SkillCandidate{}).
		Where("user_id = ? AND source_session_key = ? AND status <> ?", userID, sessionKey, model.SkillCandidateRejected).
		Count(&cnt).Error; err != nil {
		return false, err
	}
	return cnt > 0, nil
}

// ExistsCandidateByHash 判断某用户是否已存在相同内容哈希的候选（任何非 rejected 状态），
// 用于跨会话去重：同一套技能的提取只保留一条。
func ExistsCandidateByHash(db *gorm.DB, userID uint, hash string) (bool, error) {
	if hash == "" {
		return false, nil
	}
	var cnt int64
	if err := db.Model(&model.SkillCandidate{}).
		Where("user_id = ? AND content_hash = ? AND status <> ?", userID, hash, model.SkillCandidateRejected).
		Count(&cnt).Error; err != nil {
		return false, err
	}
	return cnt > 0, nil
}

// CountSkillCandidates 统计某用户的候选数（可按 status 过滤，空串不过滤）。
// 供 API 概览返回 total。
func CountSkillCandidates(db *gorm.DB, userID uint, status string) (int64, error) {
	q := db.Model(&model.SkillCandidate{}).Where("user_id = ?", userID)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var cnt int64
	if err := q.Count(&cnt).Error; err != nil {
		return 0, err
	}
	return cnt, nil
}
