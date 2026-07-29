# workbuddyLLMAPI —— 本地 OpenAI 兼容 LLM 网关

在本地封装一套 **OpenAI 兼容的 LLM API**，把 WorkBuddy/CodeBuddy 的「积分」当作模型后端来用，
同时保留通用的 `passthrough` 代理模式，方便对接任意 OpenAI 兼容服务（含本机 vLLM）以及本地开发联调。

> 背景：WorkBuddy 桌面工作台本身没有裸的 LLM 调用 API/SDK；它基于 CodeBuddy，而 CodeBuddy 的积分
> 默认只能通过 **CodeBuddy Agent SDK** 或 **开放平台 REST API** 消费，二者都不是原生 OpenAI 兼容端点。
>
> 关键发现：WorkBuddy 本机实际上跑着一个 **CodeBuddy CLI**，它对外暴露了本地 HTTP 守护进程
> （`codebuddy daemon` / `--serve`，端口可指定）。该守护进程复用 `~/.codebuddy` 的已登录态，直接调用
> `https://copilot.tencent.com/v2/chat/completions` 并消耗 WorkBuddy/CodeBuddy 积分。守护进程支持
> **ACP（Agent Client Protocol，JSON-RPC over SSE）**，本网关的 `codebuddy` 后端即直接走 ACP 驱动它。
>
> 因此本项目在本地做一层网关：对上暴露标准 OpenAI 协议，对下经 ACP 直达本机守护进程，**无需明文
> API Key / Token**，直接复用你已登录的 WorkBuddy 账号积分。

## 工作原理（codebuddy 后端）

```
trpc-agent-go / 任意 OpenAI 客户端
        │  OpenAI 协议 (/v1/chat/completions)
        ▼
workbuddyLLMAPI 网关  (本工具, 端口如 :8088)
        │  ACP over HTTP (JSON-RPC + SSE)
        │  connect → initialize → session/new → session/prompt
        │  ← session/update(agent_message_chunk) 流
        ▼
本机 CodeBuddy 守护进程  (端口 18765, 复用 ~/.codebuddy 登录态)
        │  HTTPS
        ▼
https://copilot.tencent.com/v2/chat/completions   ← 消耗 WorkBuddy/CodeBuddy 积分
```

每个请求：建立连接 → `initialize` → `session/new`（**独立会话，避免共享会话卡死**）→
`session/prompt` → 从持久 SSE 流收集 `agent_message_chunk` 文本增量 → `session_end` 结束 →
`session/close` → `disconnect`。

> 守护进程同一时刻只执行一个 agent 任务，因此网关用互斥锁串行化提示词，避免堆积导致卡死。
> 若守护进程因异常中断而卡在「busy」，重启守护进程即可（见下）。

## 目录结构

```
tool/workbuddyLLMAPI/
├── main.go                      # 入口：加载配置并启动 HTTP 服务
├── go.mod
├── bin/
│   └── workbuddy-llm-api.exe    # 编译产物（go build 生成）
├── internal/
│   ├── config/config.go         # 环境变量配置（含守护进程地址/工作目录/模型）
│   ├── openai/                  # 请求/响应类型 + 路由（/v1/chat/completions、/v1/models、/healthz）
│   └── backend/                 # 三种后端实现
│       ├── backend.go           # Backend 接口 + 工厂
│       ├── passthrough.go       # 转发到任意 OpenAI 兼容 base URL（含 SSE 透传）
│       ├── mock.go              # 本地回显后端（开发联调）
│       ├── codebuddy.go         # 经 ACP 直连本机 CodeBuddy 守护进程（消耗积分）
│       └── util.go              # SSE/响应/错误辅助
└── bridge/
    └── acp_probe.py             # 调试工具：复现 ACP 线协议，验证守护进程连通性
```

## 一、启动本机 CodeBuddy 守护进程（仅需一次，常驻）

使用 WorkBuddy 自带的 CLI（**不要杀 WorkBuddy 桌面进程**，它和守护进程是分开的进程）：

```bash
# WorkBuddy 自带的 CLI 路径（Windows 示例）
CLI="C:/Users/anmingwei/AppData/Local/Programs/WorkBuddy/resources/app.asar.unpacked/cli/bin/codebuddy"

# 以守护进程模式启动，端口 18765（idempotent，重复执行安全）
"$CLI" daemon start --port 18765 --host 127.0.0.1
```

- 登录态复用 `~/.codebuddy`，所以**只要 WorkBuddy/CodeBuddy 已登录，就会自动消耗其积分**，无需任何 Key。
- 验证：`curl http://127.0.0.1:18765/api/v1/health` 应返回 `{"data":{"status":"ok",...}}`；
  `curl http://127.0.0.1:18765/api/v1/info` 可见当前登录用户名。
- 若守护进程卡死（长期无响应），重启即可：`"$CLI" daemon start --port 18765 --host 127.0.0.1`
  （它会重新拉起；不会触碰 WorkBuddy 桌面程序的端口）。
- ⚠️ **安全红线**：本文涉及的端口 18765 是本工具的守护进程端口；**切勿操作 WorkBuddy 桌面程序的
  端口（如 18488 / 50551 / 56062 / 56066 / 56088 / 64074）或杀 WorkBuddy.exe**。

## 二、编译并启动网关

```bash
cd tool/workbuddyLLMAPI
go build -o bin/workbuddy-llm-api.exe .      # 编译

# 以 codebuddy 后端启动（指向本机守护进程 18765）
WB_BACKEND=codebuddy \
WB_LISTEN=:8088 \
WB_DAEMON_URL=http://127.0.0.1:18765 \
WB_DAEMON_CWD="C:/Users/anmingwei/WorkBuddy/goMultiAgentV2" \
WB_DAEMON_MODEL=hy3 \
WB_DAEMON_FALLBACK_MODEL=deepseek-v4-pro \
./bin/workbuddy-llm-api.exe -backend codebuddy -addr :8088
```

其他后端（开发联调用）：

```bash
WB_BACKEND=mock        WB_LISTEN=:8080 go run .          # 本地回显，无需外部依赖
WB_BACKEND=passthrough WB_BASE_URL=https://api.openai.com/v1 WB_API_KEY=sk-xxx WB_LISTEN=:8080 go run .
```

## 三、模型默认与自动回退（codebuddy 后端）

- **默认模型**：`WB_DAEMON_MODEL`（默认 `hy3`）。请求体不指定 `model`（或写占位名 `codebuddy-default`）时使用它。
- **自动回退**：`WB_DAEMON_FALLBACK_MODEL`（默认 `deepseek-v4-pro`）。
  - 规则：**未显式指定模型**的请求按 `主模型 → 回退模型` 顺序尝试；主模型失败（网络/HTTP 错误/返回空）时自动切换。
  - **显式指定**了模型的请求（如 `model:"glm-5.1"`）**不做回退**，直接透传该模型。
- 验证默认=hy3：见测试 4（不传 model，返回 `"model":"hy3"`）。
- 验证回退：把 `WB_DAEMON_MODEL` 设成不存在的 id、`WB_DAEMON_FALLBACK_MODEL=hy3`，再不指定模型调用，
  会先失败一次再回退到 hy3（已实测）。

## 四、测试（全接口）

> 假设网关在 `:8088`，守护进程在 `:18765`。所有调用都会真实消耗 WorkBuddy/CodeBuddy 积分。

### 一键脚本（推荐）

```bash
cd tool/workbuddyLLMAPI
python tests/test_api.py                 # 默认打 http://127.0.0.1:8088
python tests/test_api.py http://x:8080   # 指定地址
```

覆盖 11 个用例：`GET /`、`GET /healthz`、`GET /v1/models`、非流式/流式 `chat/completions`（默认 hy3）、
显式模型 `glm-5.1`/`deepseek-v4-pro`/`kimi-k2.6`、多轮对话、system 提示词、错误用例（非法 JSON→400）。
输出每个用例 `PASS/FAIL` 与汇总（已验证 11/11 通过）。

### 手动 curl 逐接口

```bash
B=http://127.0.0.1:8088

# 1) 服务信息 GET /
curl $B/ ; echo

# 2) 健康检查 GET /healthz  -> ok
curl $B/healthz ; echo

# 3) 模型目录 GET /v1/models（含 hy3，共 28 个）
curl $B/v1/models ; echo

# 4) 非流式（默认模型 = hy3，不传 model 即走 hy3）
curl $B/v1/chat/completions -H "Content-Type: application/json" \
  -d '{"messages":[{"role":"user","content":"你是什么模型？只答模型名。"}],"stream":false}'
# -> "model":"hy3"

# 5) 流式 SSE（默认模型），结尾 data: [DONE]
curl -N $B/v1/chat/completions -H "Content-Type: application/json" \
  -d '{"messages":[{"role":"user","content":"用一句话介绍北京"}],"stream":true}'

# 6) 显式国产模型（直连，模型名原样透传，不做回退）
curl $B/v1/chat/completions -H "Content-Type: application/json" \
  -d '{"model":"glm-5.1","messages":[{"role":"user","content":"只回答：OK"}],"stream":false}'
curl $B/v1/chat/completions -H "Content-Type: application/json" \
  -d '{"model":"deepseek-v4-pro","messages":[{"role":"user","content":"只回答：OK"}],"stream":false}'
curl $B/v1/chat/completions -H "Content-Type: application/json" \
  -d '{"model":"kimi-k2.6","messages":[{"role":"user","content":"1+1=? 只答数字"}],"stream":false}'

# 7) 多轮对话（含 system + 历史）
curl $B/v1/chat/completions -H "Content-Type: application/json" \
  -d '{"messages":[
        {"role":"system","content":"你是一名简洁的助手"},
        {"role":"user","content":"记住：我的幸运数字是 7"},
        {"role":"assistant","content":"好的，记住了。"},
        {"role":"user","content":"我的幸运数字是几？只答数字。"}],"stream":false}'
# -> 应含 "7"

# 8) 错误用例：非法 JSON 体 -> 400
curl -i $B/v1/chat/completions -H "Content-Type: application/json" -d '{bad json}' ; echo
```

调试工具（可选）：`python bridge/acp_probe.py http://127.0.0.1:18765` 会复现 ACP 流程并打印
`session/update` 文本增量，用于确认守护进程本身可用。

## 五、接入 trpc-agent-go（本项目模型三协议适配层）

将网关当作一个标准的 OpenAI 协议 Provider 接入即可：

- `base_url` = `http://localhost:8088/v1`
- `api_key`  = 任意非空字符串（codebuddy 后端不校验，随便填即可）
- `model`    = `codebuddy-default`（或 `WB_MODELS` 中列出的任一名称）

这样本项目（goMultiAgentV2）即可在不直接持有模型密钥的情况下，统一经由本网关调用模型，
而在 `codebuddy` 后端下实际消耗的是 WorkBuddy/CodeBuddy 的积分。

## 配置项一览（环境变量）

| 变量 | 默认 | 说明 |
|------|------|------|
| `WB_LISTEN` | `:8080` | 网关监听地址 |
| `WB_BACKEND` | `mock` | `passthrough` / `mock` / `codebuddy` |
| `WB_BASE_URL` | `https://api.openai.com/v1` | passthrough 上游 base URL |
| `WB_API_KEY` | 空 | passthrough 上游 API Key |
| `WB_DAEMON_URL` | `http://127.0.0.1:18765` | 本机 CodeBuddy 守护进程地址（codebuddy 后端） |
| `WB_DAEMON_CWD` | `.` | ACP `session/new` 的 agent 工作目录 |
| `WB_DAEMON_MODEL` | `hy3` | **默认模型**：请求未指定 `model` 时使用的模型 id（hy3/glm-5.1/deepseek-v4-pro/kimi-*/...） |
| `WB_DAEMON_FALLBACK_MODEL` | `deepseek-v4-pro` | **回退模型**：仅当未显式指定 `model` 且主模型失败（网络/HTTP 错误/返回空）时自动切换 |
| `WB_DEFAULT_MODEL` | `codebuddy-default` | 缺省模型占位名（实际被 `WB_DAEMON_MODEL` 取代） |
| `WB_MODELS` | 逗号分隔列表（**默认即 CodeBuddy 真实模型目录**：auto/hy3/glm-*/kimi-*/deepseek-*/minimax-*/qwen-*/step + 少量国外旗舰） | `/v1/models` 返回内容；请求里的 model 名会**原样透传**给守护进程，按账号实际可用模型路由 |
