# 连接器市场（MCP 模板）

> M8-08 交付：预置常用 MCP 服务器模板，一键导入为自己的 MCP 配置。
> 后端：`server/internal/marketplace`（模板库 + 渲染纯函数）+ `server/internal/api/mcp_templates.go`（列表/导入 API）。
> 前端：`web/src/views/McpView.vue`「连接器市场」抽屉。

## 是什么

连接器市场是 MCP 管理的「快速上手」通道：内置 8 个常用 MCP 模板（GitHub / GitLab /
Slack / Jira·Confluence / PostgreSQL / Redis / 文件系统 / 网页抓取），用户点「一键导入」
即生成一条完整的 MCP 配置（名称、传输方式、命令/URL、env/headers 骨架），只需补上
自己的密钥/参数即可「测试连接 → 在对话中按需装载」。

模板是**纯数据 + 纯函数**，不落库、不发起网络请求；导入时渲染成 `model.MCPServer`
后走与手动创建完全相同的 repo 路径（env/headers AES-256-GCM 加密落库、同名冲突
409、Validate 校验、owner 隔离）。

## API

| 方法 | 路径 | 权限 | 说明 |
|------|------|------|------|
| GET | `/api/mcp/templates` | mcp:read | 模板列表（含分类、所需密钥提示；不回显模板 env/headers 值） |
| POST | `/api/mcp/templates/:id/import` | mcp:write | 按模板创建配置；body 可覆盖 name/enabled/description、提供密钥实际值 |

### 导入请求体

```json
{
  "name": "我的 GitHub",          // 可选，缺省用模板 default_name
  "enabled": true,                // 可选，缺省用模板 default_enabled
  "description": "自建实例",      // 可选，覆盖模板描述
  "env": { "GITHUB_TOKEN": "ghp_xxx" },    // 占位符实际值（键名 = 模板 secret_fields）
  "headers": {}                    // 远程端点的请求头（可选）
}
```

占位符机制：模板中敏感/个性化字段用 `{{KEY}}` 占位符（如 `{{GITHUB_TOKEN}}`、
`{{POSTGRES_CONNECTION_STRING}}`）。导入时后端按 **env ∪ headers 合并查找** 替换——
密钥放 `env` 或 `headers` 都能替换任意位置的占位符；未提供的占位符保留原样（可导入后
编辑补齐）。匹配到占位符的键只作查找源、不冗余落库（GitHub 导入后 env 保持为空）。

### 响应

与手动创建一致（`mcpServerView`）：`id / name / transport / has_env / env_keys / url /
has_headers / header_keys / enabled / description / created_at / updated_at`。密钥值
永不下发，只给掩码（键名）。

## 内置模板一览

| id | 名称 | 分类 | 传输 | 所需密钥 |
|----|------|------|------|----------|
| github | GitHub | 代码托管 | streamable | `GITHUB_TOKEN`（GitHub PAT，repo 读权限即可） |
| gitlab | GitLab | 代码托管 | stdio | `GITLAB_TOKEN`（自建实例可改 env `GITLAB_API_URL`） |
| slack | Slack | 团队协作 | streamable | `SLACK_TOKEN`（Slack MCP 集成 Bot Token） |
| atlassian | Jira / Confluence | 团队协作 | stdio | `JIRA_URL` / `JIRA_EMAIL` / `JIRA_API_TOKEN` |
| postgres | PostgreSQL | 数据与存储 | stdio | `POSTGRES_CONNECTION_STRING` |
| redis | Redis | 数据与存储 | stdio | `REDIS_URL` |
| filesystem | 文件系统 | 通用工具 | stdio | `WORKSPACE_DIR`（允许 Agent 操作的目录） |
| fetch | 网页抓取 | 通用工具 | stdio | 无（导入即用） |

## 本次顺带修复（M2-02 遗留缺陷）

1. **`mcp_servers` 复合唯一索引**：原模型只在 `Name` 上声明 `uniqueIndex:idx_user_mcp`，
   `UserID` 仅普通索引——实际是**单列 name 全局唯一**，不同用户无法建同名 MCP（对
   连接器市场是致命缺陷：每用户都要能导入同名模板）。已补 `UserID` 的 uniqueIndex 声明，
   并新增迁移 `0013_fix_mcp_servers_composite_unique` 把旧库单列索引重建为
   `(user_id, name)` 复合（幂等，旧库无跨用户同名数据，重建无冲突）。
2. **GORM 零值 bool 陷阱**：`Enabled` 带 `default:true`，Create 遇零值（false）时 GORM
   省略该列、由 DB 默认值回填结构体——`enabled:false` 被悄悄置回 true。`repo.CreateMCPServer`
   现按 Create 前的期望值显式校正；`CreateMCPServerHandler` 同步显式化「缺省启用」语义。

## 测试

- `internal/marketplace`：8 例（模板库自洽 / ID 唯一 / 占位符替换·保留 / env∪headers 合并查找 /
  自定义 env 合并 / 无密钥模板 / 正则容错 / 空选项全模板 Validate）。
- `internal/api`：7 例（列表内容与掩码 / 一键导入 GitHub（占位符替换 + 密文落库 + 不回显）/
  覆盖 name·enabled·description / 同名 409 / 未知模板 404·非法 body 400 / viewer RBAC /
  跨用户 owner 隔离 + 同名模板按用户隔离）。
- `internal/repo`：迁移 0013 单测（旧库单列索引重建为复合 + 双用户同名插入成功 + 同用户
  同名冲突）。
