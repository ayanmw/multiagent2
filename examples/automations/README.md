# 自动化与场景示例（examples/automations）

> 配合 [docs/DEMO-24H.md](../docs/DEMO-24H.md) 与 [docs/ARCHITECTURE.md](../docs/ARCHITECTURE.md) 使用的可拷贝示例。
> 所有示例通过 REST API 操作；也可以用前端「自动化」页点选创建（字段完全一致）。

---

## 通用前置（拿 token）

```sh
# 登录（字段是 account，可为 username 或 email）
TOKEN=$(curl -s -X POST http://localhost:8080/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"account":"admin","password":"<你的密码>"}' | \
  python -c "import sys,json;print(json.load(sys.stdin)['token'])")
echo "$TOKEN"
```

> 容器内访问把 `localhost:8080` 换成 `server:8080`；前端经 nginx 反代则用 `http://localhost:8081/api/...`。

---

## 创建自动化（通用请求体）

`POST /api/automations`（需 `automations:write`）：

```json
{
  "name": "<名称>",
  "trigger_type": "cron",
  "cron_expr": "0 * * * *",
  "goal_prompt": "<目标提示词>",
  "enabled": true
}
```

- `trigger_type`：`cron`（定时）或 `webhook`（外部事件）。
- `cron_expr`：仅 cron 触发器需要；标准 5 段 cron。
- `goal_prompt`：驱动 Loop 的目标，必填。
- webhook 触发器创建后会返回 `webhook_token`，外部打 `POST /api/webhooks/:token` 即可触发。

其他接口：

- `GET /api/automations` 列表（owner 隔离）
- `GET /api/automations/:id` 详情
- `PUT /api/automations/:id` 部分更新（`enabled` 可单独切）
- `DELETE /api/automations/:id` 删除
- `GET /api/automations/:id/runs` 运行历史

---

## 示例清单

| 文件 | 场景 | 触发 |
|------|------|------|
| [24h-self-improve.md](./24h-self-improve.md) | 24h 自主：每小时自改进代码 | cron |
| [skill-flywheel.md](./skill-flywheel.md) | 技能进化飞轮（后台自动，无需手动建） | 后台定时扫描 |

更多场景（IM 触发、知识库 RAG、连接器市场导入）见 [docs/DEMO-24H.md](../docs/DEMO-24H.md) 第 6 节。
