package model

import (
	"errors"
	"strings"

	"gorm.io/gorm"
)

// IMBinding 是 IM 用户与平台用户的绑定关系（M8-07 IM Channel）。
//
// 语义：某 IM 平台（飞书/钉钉/企微）的用户 IMUserID（如飞书 open_id、钉钉
// senderStaffId、企微 FromUserName）被绑定到平台 UserID，其发送的消息经
// IM webhook 进入后以该平台用户身份跑 Gateway Loop，并把结果回发到 ChatID。
// ChatID 即「回发目标地址」：飞书=chat_id(oc_xxx)、钉钉=conversationId(cidxxx)、
// 企微=FromUserName(userid，机器人主动回发按 touser 寻址)。
//
// 复合唯一 (platform, im_user_id)：同一 IM 用户只能绑定一个平台用户
// （多 chat 场景以 ChatID 记录回发目标，绑定本身按 IM 用户唯一）。
type IMBinding struct {
	gorm.Model
	UserID    uint   `gorm:"not null;index" json:"user_id"`
	Platform  string `gorm:"size:16;not null;uniqueIndex:idx_im_binding,priority:1" json:"platform"`
	IMUserID  string `gorm:"size:128;not null;uniqueIndex:idx_im_binding,priority:2" json:"im_user_id"`
	ChatID    string `gorm:"size:128;not null" json:"chat_id"`
	Username  string `gorm:"size:128" json:"username"` // IM 侧用户名（冗余展示，非绑定键）
}

// TableName overrides the default GORM table name.
func (IMBinding) TableName() string { return "im_bindings" }

// Validate 校验绑定配置自洽性：platform 合法、im_user_id 与 chat_id 非空。
func (b *IMBinding) Validate() error {
	switch b.Platform {
	case "feishu", "dingtalk", "wecom":
	default:
		return errors.New("invalid platform (must be feishu, dingtalk or wecom)")
	}
	if strings.TrimSpace(b.IMUserID) == "" {
		return errors.New("im_user_id is required")
	}
	if strings.TrimSpace(b.ChatID) == "" {
		return errors.New("chat_id is required")
	}
	return nil
}
