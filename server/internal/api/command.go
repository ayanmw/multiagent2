package api

import (
	"net/http"

	"github.com/ayanmw/multiagent2/server/internal/command"
	"github.com/gin-gonic/gin"
)

// ListCommandsHandler 下发斜杠命令注册表（M1-14）。
//
// 路由：GET /api/commands（受保护，前端/CLI 共用）。
// 返回体：{ "commands": [ Command, ... ] }，Command 结构见 internal/command 包。
// 新增命令只需在 command.Builtin() 追加一条，前端自动渲染，无需改客户端代码。
func ListCommandsHandler() gin.HandlerFunc {
	// 在构造期固化一次（注册表是静态元数据，避免每次请求重复构建）。
	cmds := command.Builtin()
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"commands": cmds})
	}
}
