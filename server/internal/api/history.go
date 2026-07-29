package api

import (
	"github.com/anmingwei/go-multi-agent-v2/internal/model"
	"github.com/anmingwei/go-multi-agent-v2/internal/repo"
	framework "trpc.group/trpc-go/trpc-agent-go/model"
	"gorm.io/gorm"
)

// toFrameworkMessage 把 DB 持久化的消息映射为框架 model.Message（多轮记忆回灌用）。
// DB 角色字符串（user/assistant/system/tool）映射到框架 Role 枚举；未知角色按 user 处理。
func toFrameworkMessage(m model.Message) framework.Message {
	var role framework.Role
	switch m.Role {
	case "assistant":
		role = framework.RoleAssistant
	case "system":
		role = framework.RoleSystem
	case "tool":
		role = framework.RoleTool
	default:
		role = framework.RoleUser
	}
	return framework.Message{Role: role, Content: m.Content}
}

// loadChatHistory 从 DB 读取会话历史并转换为框架消息序列，用于回灌引擎实现多轮记忆（M0.5-01）。
// excludeLast 表示跳过末尾的若干条消息（通常为「本轮刚写入的 user 消息」，引擎会自行追加），
// 避免历史与当前输入重复。失败时返回空切片（退化为单轮），不阻断主流程。
func loadChatHistory(db *gorm.DB, sessionID uint, excludeLast int) []framework.Message {
	msgs, err := repo.ListSessionMessages(db, sessionID)
	if err != nil || len(msgs) == 0 {
		return nil
	}
	end := len(msgs) - excludeLast
	if end < 0 {
		end = 0
	}
	out := make([]framework.Message, 0, end)
	for i := 0; i < end; i++ {
		out = append(out, toFrameworkMessage(msgs[i]))
	}
	return out
}
