# 多租户隔离强化（M8-09）

> 里程碑 M8-09：workspace 级配额、租户预算上限、资源隔离。
> 验收核心：「租户 A 超配额不影响 B」。

## 一、背景与目标

M3-04 已提供 **user / session / automation** 三级预算护栏（`budget_policies` 表 +
`repo.EvaluateBudgets` 运行时拦截）。M8-09 在此之上补齐多租户隔离所需的三个维度：

| 维度 | 说明 | 落点 |
|------|------|------|
| **租户预算上限** | 一组用户（租户）共享一个 token 预算，租户超限只拦本租户 | `tenants` 表 + `users.tenant_id` + `BudgetScopeTenant` |
| **workspace 级配额** | 每个 workspace 独立的 token 预算 + 磁盘配额 | `BudgetScopeWorkspace` + `workspaces.disk_quota_bytes` |
| **资源隔离** | 租户间互不污染聚合、磁盘配额按目录边界独立统计 | 用量按 user 归属子查询聚合、配额按 workdir 独立 Walk |

## 二、模型与迁移

### 1. 租户（`model/tenant.go`）

```go
type Tenant struct {
    gorm.Model
    Name        string       // 唯一
    Description string
    Status      TenantStatus // active / disabled
    CreatedBy   uint         // 创建者（平台管理员）
}
```

- `users.tenant_id`（可空，索引）：nil=独立用户（不参与租户聚合，向后兼容既有部署）；
  非空=归属该租户，与租户内其他用户共享租户级预算。
- 迁移 `0014_add_tenant_and_quota_columns`：建 `tenants` 表；幂等补
  `users.tenant_id` / `usage_records.workspace_key` / `workspaces.disk_quota_bytes` 三列。

### 2. 预算作用域扩展（`model/budget.go`）

新增两个作用域（`budget_policies.scope`）：

- `tenant`：`scope_key=<tenant_id>`，聚合 `users.tenant_id=<tid>` 下**全部用户**的 token；
- `workspace`：`scope_key=<workspace_key>`，聚合绑定该 workspace 的会话 token。

### 3. 用量记录挂 workspace（`model/usage.go`）

`usage_records.workspace_key`（可空，索引）：会话绑定 workspace 时随用量落库，
供 workspace 作用域聚合；默认目录会话留空。

## 三、运行时拦截链路

### `repo.EvaluateBudgets(db, uid, sessionKey, workspaceKey, automationID)`

按 **user → session → workspace → tenant → automation** 顺序评估，任一超限即
`Blocked=true`（携带 scope/used/max 明细）。关键实现：

- **workspace 作用域**：`workspaceKey` 非空且存在该策略时，按 `workspace_key` 聚合；
- **tenant 作用域**：查 `users.tenant_id`，存在租户策略时按
  `user_id IN (SELECT id FROM users WHERE tenant_id=?)` 聚合——**租户 A 的用量
  只来自 A 的用户，天然不影响 B**（核心隔离）。

### `Gateway.prepareRun` 顺序调整（M8-09）

原顺序「预算检查 → 模型 → 会话 → workspace」调整为
「会话 → workspace 解析 → 预算检查 → 模型」：workspace 作用域预算需要
会话绑定的 workspace key，故 workspace 解析提前到预算检查之前。
`preparedRun` 新增 `wsKey` / `wsQuota` 字段：

- `wsKey` → `finalize → recordEngineUsage` 落 `usage_records.workspace_key`；
- `wsQuota` → 装配单代理文件工具时注入 `NewCodeActWithGitBackend(..., wsQuota)`。

## 四、workspace 磁盘配额

`workspaces.disk_quota_bytes`（0=不限，默认）：`file_write` / `file_edit` 写入前
经 `codectool.enforceQuota(workdir, quota, delta)` 检查目录**总大小**（`filepath.Walk`），
`size+delta > quota` 返回 `ErrDiskQuotaExceeded` 拒绝写入：

- `file_write`：`delta = len(content)`；
- `file_edit`：`delta = (len(new)-len(old)) * count`（替换净增量，体积变小放行）；
- 配额按 workdir 目录边界独立统计——一个 workspace 写爆不影响同用户其他 workspace；
- docker 后端下 workdir 即宿主机挂载目录，统计口径一致。

## 五、API

### 租户管理（`tenants:read` / `tenants:write`，admin 全权限 / developer 只读）

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/tenants` | 租户列表（含 member_count） |
| POST | `/api/tenants` | 创建租户（同名 409） |
| GET | `/api/tenants/:id` | 详情 |
| PUT | `/api/tenants/:id` | 改名 / 描述 / 启停用 |
| DELETE | `/api/tenants/:id` | 删除（**有成员 409**，需先迁移成员） |
| POST | `/api/tenants/:id/members` | `{user_id}` 加入租户（停用租户 409） |
| DELETE | `/api/tenants/:id/members/:uid` | 移出租户（置 NULL 恢复独立） |

### 用户归属租户（`api/admin.go` 扩展）

- `POST /api/admin/users`：可选 `tenant_id`，创建即归属；
- `PUT /api/admin/users/:id`：`tenant_id`（0=移出 / >0=加入 / 缺省=不修改）。

### 预算设置（复用 `budgets` API）

租户 / workspace 预算与既有策略同接口：

```sh
PUT /api/budgets   # {"scope":"tenant","scope_key":"<tenant_id>","max_tokens":100000,"window":"daily"}
PUT /api/budgets   # {"scope":"workspace","scope_key":"<workspace_key>","max_tokens":5000,"window":"daily"}
```

### workspace 磁盘配额（`workspaces` API）

```sh
PUT /api/workspaces/<key>   # {"disk_quota_bytes": 104857600}   # 100MiB；0=不限
```

## 六、测试

- `repo/tenant_test.go`：租户 CRUD / 成员管理 / 删除非空拒绝；
- `repo/budget_test.go`（扩展）：**workspace 作用域拦截**（ws-a 超限拦 ws-a、
  ws-b 与默认目录不受影响）；
- `repo/tenant_test.go`：**租户隔离核心验收**——租户 A 两用户共享上限超限后
  A 内用户全被拦（聚合=120）、租户 B 用户不受影响、独立用户不受影响、
  且 A 的聚合不含 B 的用量；
- `tool/quota_test.go`：file_write 超限拒绝且文件不落盘、file_edit 净增量超限拒绝
  且文件保持原样、体积变小放行、边界放行、quota<=0 不限；
- `api/tenant_test.go`：RBAC（developer 只读 / admin 全通）、CRUD 全流程、
  admin 建用户带租户归属、租户成员数正确；
- `repo/migrate_test.go`：0014 迁移建表补列 + 行为验收（租户可建、用户可归属、
  workspace 可设配额）。

## 七、边界与说明

1. **租户删除为软约束**：有成员时拒绝删除（`ErrTenantNotEmpty`），需先迁移成员或
   停用租户；数据层面不级联删用户（取消聚合边界而非销毁数据）。
2. **team 模式 Coder 磁盘配额**：Coder 工作目录为 git worktree（主仓库派生），
   M8-09 按 0（不限）处理；其产物最终 merge 回主 workspace，由单代理路径的
   workspace 配额兜底。若需 worktree 级配额，可后续在 worktree 派生点读取父 workspace 配额注入。
3. **workspace token 预算仅覆盖「绑定 workspace 的会话」**：默认目录会话
   （未绑定 workspace）不参与 workspace 聚合，但仍受 user / tenant 预算约束。
4. **租户预算按日/总量窗口与 user 预算一致**：`window=daily`（自然日重置）或
   `total`（全周期累计），配置于策略行。
