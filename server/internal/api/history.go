package api

import (
	"github.com/ayanmw/multiagent2/server/internal/engine"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"gorm.io/gorm"
)

// loadChatHistory 从 DB 读取会话历史并转换为引擎消息 DTO（多轮记忆回灌用，M6-02）。
// DB 角色字符串（user/assistant/system/tool）直接作为 ChatMessage.Role 传给引擎，
// 由引擎边界处的 ToFramework 完成到框架 Role 枚举的映射，api 层不再依赖框架 model 包。
// excludeLast 表示跳过末尾的若干条消息（通常为「本轮刚写入的 user 消息」，引擎会自行追加），
// 避免历史与当前输入重复。失败时返回空切片（退化为单轮），不阻断主流程。
func loadChatHistory(db *gorm.DB, sessionID uint, excludeLast int) []engine.ChatMessage {
	msgs, err := repo.ListSessionMessages(db, sessionID)
	if err != nil || len(msgs) == 0 {
		return nil
	}
	end := len(msgs) - excludeLast
	if end < 0 {
		end = 0
	}
	out := make([]engine.ChatMessage, 0, end)
	for i := 0; i < end; i++ {
		out = append(out, engine.ChatMessage{Role: msgs[i].Role, Content: msgs[i].Content})
	}
	return out
}
