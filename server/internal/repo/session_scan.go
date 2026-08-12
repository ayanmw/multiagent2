package repo

import (
	"github.com/ayanmw/multiagent2/server/internal/model"
	"gorm.io/gorm"
)

// ListSessionsAll 返回全部用户的会话（按更新时间倒序），供「进化技能飞轮」扫描器
// 跨用户遍历。扫描器按 UserID 分组后逐会话提取候选技能；调用方负责 owner 隔离的
// 落库（候选技能写入各自归属用户）。性能上限由扫描器自身的「已处理会话去重」保证
// （每个会话只提取一次）。
func ListSessionsAll(db *gorm.DB) ([]model.Session, error) {
	var list []model.Session
	if err := db.Order("updated_at DESC, id DESC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}
