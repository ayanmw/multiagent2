# IM Channel（M8-07）：飞书 / 钉钉 / 企业微信接入

IM Channel 让自主 Agent 平台可以从 IM 机器人接收任务（触发 Loop）并把结果回发 IM，
兑现「Loop 可从 IM 触发与回发」。实现复用 M4-04 的 `Gateway` 统一入口：

- 入站：IM 平台把机器人收到的文本消息推送至 `POST /api/im/:platform/webhook`；
- 绑定：`im_bindings` 表记录「IM 用户 → 平台用户」映射（owner 隔离，仅本人绑定本人）；
- 执行：按绑定用户身份调 `Gateway.Run`（`Channel=im`），带与 cron/webhook 相同的
  Goal 契约 TeamOverride（子代理 + 目标契约 + 护栏），无人值守模式下 ask 危险命令
  自动进检查点队列；
- 会话：稳定 `session_key = im:<platform>:<chat_id>`，同一 IM 聊天天然多轮记忆；
- 回发：Loop 结果/错误/未绑定指引经各平台「自定义机器人 webhook」POST 回 IM。

## 1. 配置（.env.example）

```bash
# 出站回发 URL（机器人 webhook，非空才启用回发）
IM_FEISHU_WEBHOOK_URL=
IM_DINGTALK_WEBHOOK_URL=
IM_WECOM_WEBHOOK_URL=
# 入站验签密钥（生产必须配置；空=本地调试跳过验签，勿用于生产）
IM_FEISHU_SECRET=
IM_DINGTALK_SECRET=
IM_WECOM_SECRET=
```

未配置的 IM 平台仍可接收消息并执行 Loop（解析/运行照常），只是不回发、不验签。

## 2. 绑定 IM 用户

绑定 = 告诉平台「某个 IM 用户对应哪个平台用户」。绑定后该 IM 用户发消息即以该平台
用户身份执行（预算/审计/workspace 均归属该用户）。

```bash
# 需要平台登录态（Authorization: Bearer <JWT>）
curl -X POST http://localhost:8080/api/im/bindings \
  -H "Authorization: Bearer <token>" -H "Content-Type: application/json" \
  -d '{"platform":"feishu","im_user_id":"ou_xxx","chat_id":"oc_xxx","username":"张三"}'

curl http://localhost:8080/api/im/bindings -H "Authorization: Bearer <token>"   # 列表
curl -X DELETE http://localhost:8080/api/im/bindings/1 -H "Authorization: Bearer <token>"  # 删除
```

- `im_user_id`：飞书=open_id、钉钉=senderStaffId、企微=FromUserName（发送者 userid）；
- `chat_id`：回发目标——飞书=chat_id(oc_xxx)、钉钉=conversationId(cidxxx)、企微=FromUserName
  （企微机器人主动回发按 userid 寻址）；
- 复合唯一 `(platform, im_user_id)`，重复绑定返回 409；仅本人可创建/删除自己的绑定。

## 3. 各平台接入步骤

### 3.1 飞书（Feishu/Lark）

1. 开放平台建企业自建应用 → 开启「机器人」能力；
2. 事件订阅 → 订阅 `im.message.receive_v1`；请求地址填
   `https://<你的域名>/api/im/feishu/webhook`（公网可达）；
3. 事件订阅页的「加密策略」→ 开启后填 `IM_FEISHU_SECRET`（encrypt_key）；
4. 「机器人」页 → 复制自定义机器人 webhook 填 `IM_FEISHU_WEBHOOK_URL`；
5. 验签：`X-Lark-Request-Timestamp` + `X-Lark-Signature`（HMAC-SHA256，见
   `internal/im/feishu.go`），本平台按官方 v2.0 协议实现。

### 3.2 钉钉（DingTalk）

1. 群机器人 → 自定义机器人，生成 Webhook 地址填 `IM_DINGTALK_WEBHOOK_URL`；
2. 回调地址：机器人安全设置里配置「加签」密钥填 `IM_DINGTALK_SECRET`；
3. 将回调 URL（`https://<你的域名>/api/im/dingtalk/webhook`）配置到钉钉开放平台的
   「机器人回调」或经 stream 网关转发（本平台实现直接按回调 JSON 解析）；
4. 验签：URL query / header 的 `timestamp` + `sign`（HMAC-SHA256），见
   `internal/im/dingtalk.go`。

### 3.3 企业微信（WeCom）

1. 企业微信管理后台 → 应用管理 → 自建应用 → 开启「接收消息」；
2. URL 填 `https://<你的域名>/api/im/wecom/webhook`；Token 填 `IM_WECOM_SECRET`；
3. 明文回调模式下本平台按 `sha1(sort(Token,timestamp,nonce,body))` 验签
   （见 `internal/im/wecom.go`）；**生产建议开启安全模式（AES 加解密）**，此时需在
   网关前置做解密后再转发，或改造 `ParseWeComEvent` 接入加解密 SDK；
4. 应用 → 群机器人 webhook 填 `IM_WECOM_WEBHOOK_URL`（回发）。

## 4. 行为约定

- **未绑定**：IM 用户发消息 → 回发一条「尚未绑定 + 绑定指引」提示，不执行 Loop；
- **防重入**：同一 IM 用户已有 Loop 在跑，新消息回发「请稍候」提示并跳过；
- **限流**：按 `platform:senderID` 维度（复用 `WEBHOOK_RATE_LIMIT`/`WEBHOOK_RATE_WINDOW_SECONDS`）；
- **审计**：每次 IM 触发 Loop 均写 `audit_logs`（channel=im）；
- **安全**：webhook 端点不挂平台 JWT 鉴权，安全性来自①各平台验签（生产必配 secret）
  ②绑定隔离（未绑定用户无法触发任何执行）③预算护栏与检查点（无人值守默认 deny）。

## 5. 测试

- `internal/im/im_test.go`：三平台解析（text/非 text/错误事件）、验签（正确/错误/缺参/
  空 secret 放行）、出站 payload 格式、HTTPSender 真实 HTTP 路径（httptest）；
- `internal/api/im_test.go`：绑定 CRUD + owner 隔离 + 409/403、webhook 全链路
  （绑定用户触发 Loop 且 reply 回发、未绑定回发指引、签名错误 401、非法平台 400、
  非文本消息 400、有效签名通过）。
