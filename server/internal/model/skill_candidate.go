package model

import (
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
)

// SkillCandidateStatus 是「进化技能飞轮」候选技能的生命周期状态（M5-03）。
//   - pending:  待审批（扫描提取后默认状态，不自动发布，等待人在前端审批）；
//   - approved: 已批准（M5-04 审批前端 approve 后转为该态，并可发布为托管技能进共享库）；
//   - rejected: 已拒绝（空泛/重复/不合规范的候选，质量门控拦截或人工 reject）。
type SkillCandidateStatus string

const (
	SkillCandidatePending  SkillCandidateStatus = "pending"
	SkillCandidateApproved SkillCandidateStatus = "approved"
	SkillCandidateRejected SkillCandidateStatus = "rejected"
)

// ValidSkillCandidateStatuses 列出所有合法 status 值（供校验与前端提示）。
var ValidSkillCandidateStatuses = []SkillCandidateStatus{
	SkillCandidatePending, SkillCandidateApproved, SkillCandidateRejected,
}

// ParseSkillCandidateStatus 校验并归一化 status 字符串（大小写/空白容错）。
func ParseSkillCandidateStatus(s string) (SkillCandidateStatus, bool) {
	t := SkillCandidateStatus(strings.TrimSpace(strings.ToLower(s)))
	switch t {
	case SkillCandidatePending, SkillCandidateApproved, SkillCandidateRejected:
		return t, true
	}
	return "", false
}

// SkillCandidate 是「进化技能飞轮」后台扫描产出的候选技能（M5-03）。
//
// 平台在自主 Loop / 日常对话结束后，后台异步扫描已结束 session 的 transcript，
// 经 LLM 提取出一份候选 SKILL.md（name/description/body），经质量门控（长度/结构/
// 去重）后写入本表，状态为 pending，等待人工审批（M5-04）；审批通过后才发布为
// 托管技能进 skills/ 共享库（不自动发布）。
//
// 字段说明：
//   - UserID:           归属用户（owner-scoped，扫描按会话归属隔离）。
//   - Name:             技能名（仅 [A-Za-z0-9_-]，与 skillrepo.ValidSkillName 同约束）。
//   - Description:      一句话描述（用于技能索引与人类审阅）。
//   - Body:             SKILL.md 完整内容（候选技能的全部说明/步骤）。
//   - SourceSessionKey: 来源会话标识（用于去重，避免同一会话重复提取）。
//   - ContentHash:      规范化内容哈希（name+body 的 sha256 前 64 位）——跨会话去重键，
//     相同内容的候选只保留一条，避免「同一套路被反复提取成不同候选」。
//   - Status:           生命周期状态（pending/approved/rejected）。
//   - RejectReason:     拒绝原因（人工 reject 或质量门控拦截时填写）。
//   - QualityNotes:     质量门控给出的改进建议（pending 时供人参考）。
type SkillCandidate struct {
	ID               uint                  `gorm:"primaryKey" json:"id"`
	UserID           uint                  `gorm:"index;not null" json:"user_id"`
	Name             string                `gorm:"size:128;not null" json:"name"`
	Description      string                `gorm:"size:512" json:"description"`
	Body             string                `gorm:"type:text" json:"body"`
	SourceSessionKey string                `gorm:"size:64;index" json:"source_session_key"`
	ContentHash      string                `gorm:"size:64;index" json:"content_hash"`
	Status           SkillCandidateStatus  `gorm:"size:16;not null;default:pending" json:"status"`
	RejectReason     string                `gorm:"size:512" json:"reject_reason"`
	QualityNotes     string                `gorm:"size:512" json:"quality_notes"`
	CreatedAt        time.Time             `json:"created_at"`
	UpdatedAt        time.Time             `json:"updated_at"`
	DeletedAt        gorm.DeletedAt        `gorm:"index" json:"-"`
}

// TableName 固定表名，避免 GORM 复数化规则变化影响既有库。
func (SkillCandidate) TableName() string { return "skill_candidates" }

// Validate 校验候选技能自洽性：名称必填且合法、描述长度合理、body 非空。
// 长度上限与字段 size 约束对齐，避免越界写入失败。
func (s *SkillCandidate) Validate() error {
	if !skillCandidateNameValid(s.Name) {
		return errors.New("技能名非法（仅允许字母数字下划线连字符，且不能为空）")
	}
	if len(s.Description) > 512 {
		return errors.New("技能描述过长（上限 512）")
	}
	if s.Body == "" {
		return errors.New("技能内容 body 不能为空")
	}
	if _, ok := ParseSkillCandidateStatus(string(s.Status)); !ok {
		return errors.New("非法的候选状态")
	}
	return nil
}

// skillCandidateNameValid 复用与 skillrepo 一致的技能名约束（仅 [A-Za-z0-9_-]）。
func skillCandidateNameValid(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') &&
			!(r >= '0' && r <= '9') && r != '_' && r != '-' {
			return false
		}
	}
	return len(name) <= 128
}
