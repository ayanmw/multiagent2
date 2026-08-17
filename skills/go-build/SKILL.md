---
name: go-build
description: Go 项目构建、测试与依赖管理规范；当涉及编译、跑测试、修复构建错误、管理 Go module 或排查沙箱环境问题时使用。
---
# Go 构建与测试规范（go-build）

本仓库后端为 Go（module `github.com/ayanmw/multiagent2/server`），前端为 Node。

## 构建与验证命令
- 后端：`cd server && CGO_ENABLED=0 go build ./...`（已切纯 Go SQLite 驱动 glebarez/sqlite，无需 gcc）。
- 静态检查：`go vet ./...`。
- 测试：`go test -count=1 ./...`；依赖 DB 且缺 CGO 的用例在沙箱会被跳过（环境限制，非缺陷）。

## 依赖管理
- 框架 trpc-agent-go 锁定 `v1.10.0`，业务代码只经 `internal/engine` 层使用，禁止绕过。
- 网络受限环境：设 `GOPROXY=https://goproxy.cn,direct GOSUMDB=off` 走 IPv4 镜像。
- 依赖必须留在隔离目录，禁止全局污染式安装。

## 常见沙箱坑
- PATH 白名单缺失 `sleep`/`which` 等命令：`export PATH="/c/Program Files/Git/usr/bin:/c/Program Files/Go/bin:$PATH"`。
- 禁止因命令在沙箱缺失就弱化生产代码或测试；先补 PATH/环境复核确认是环境问题还是真实缺陷。
- 测试禁止真正调用外部 LLM，用 mock OpenAI 桩（httptest）脚本化验证。

## 提交前
- 必须 `go build` / `go vet` 通过；单测绿；涉及前端的改动需 `npm run build` + `vue-tsc --noEmit` 绿。
