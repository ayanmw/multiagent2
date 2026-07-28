# workbuddyLLMAPI —— 本地 OpenAI 兼容 LLM 网关

在本地封装一套 **OpenAI 兼容的 LLM API**，把 WorkBuddy/CodeBuddy 的「积分」当作模型后端来用，
同时保留通用的 `passthrough` 代理模式，方便对接任意 OpenAI 兼容服务（含本机 vLLM）以及本地开发联调。

> 背景：WorkBuddy 桌面工作台本身没有裸的 LLM 调用 API/SDK；它基于 CodeBuddy，而 CodeBuddy 的积分
> 只能通过 **CodeBuddy Agent SDK** 或 **开放平台 REST API** 消费，二者都不是原生 OpenAI 兼容端点。
> 因此本项目在本地做一层网关：对上暴露标准 OpenAI 协议，对下通过 sidecar 桥接 CodeBuddy Agent SDK 来消耗积分。

## 目录结构

```
tool/workbuddyLLMAPI/
├── main.go                      # 入口：加载配置并启动 HTTP 服务
├── go.mod
├── internal/
│   ├── config/config.go         # 环境变量配置
│   ├── openai/                  # 请求/响应类型 + 路由（/v1/chat/completions、/v1/models、/healthz）
│   └── backend/                 # 三种后端实现
│       ├── backend.go           # Backend 接口 + 工厂
│       ├── passthrough.go       # 转发到任意 OpenAI 兼容 base URL（含 SSE 透传）
│       ├── mock.go              # 本地回显后端（开发联调）
│       ├── codebuddy.go         # 桥接 CodeBuddy Agent SDK（消耗积分）
│       └── util.go              # SSE/响应/错误辅助
└── bridge/
    ├── codebuddy_bridge.py      # Python sidecar（调用 codebuddy-agent-sdk）
    └── requirements.txt
```

## 编译与运行

```bash
cd tool/workbuddyLLMAPI
go build -o workbuddy-llm-api .      # 编译
./workbuddy-llm-api                  # 默认 mock 后端，监听 :8080
```

或临时指定后端：

```bash
WB_BACKEND=mock     WB_LISTEN=:8080 go run .
WB_BACKEND=passthrough WB_BASE_URL=https://api.openai.com/v1 WB_API_KEY=sk-xxx go run .
```

## 三种后端

| 后端 | 说明 | 关键环境变量 |
|------|------|--------------|
| `mock` | 本地回显，支持流式，无需任何外部依赖，便于开发联调 | — |
| `passthrough` | 原样转发到任意 OpenAI 兼容 `base_url`，SSE 透传 | `WB_BASE_URL`、`WB_API_KEY` |
| `codebuddy` | 经 Python sidecar 调用 CodeBuddy Agent SDK，**消耗 WorkBuddy/CodeBuddy 账号积分** | `CODEBUDDY_API_KEY`、`CODEBUDDY_INTERNET_ENVIRONMENT`、`WB_PYTHON` |

### codebuddy 后端前置准备

```bash
# 1) 安装 Python sidecar 依赖（建议在独立 venv 中）
python -m venv .venv && .venv/Scripts/activate     # Windows
pip install -r bridge/requirements.txt

# 2) 准备 CodeBuddy 凭据（消耗积分）
#    方式 A：已在终端用 codebuddy CLI 登录过 —— 自动复用登录态
#    方式 B：使用 API Key
export CODEBUDDY_API_KEY="你的 CodeBuddy API Key"
#    中国版还需：export CODEBUDDY_INTERNET_ENVIRONMENT=internal
#    iOA 版还需：export CODEBUDDY_INTERNET_ENVIRONMENT=ioa

# 3) 启动（注意 sidecar 路径相对工作目录）
WB_BACKEND=codebuddy WB_PYTHON=python go run .
```

> 注意：CodeBuddy Agent SDK 需要本机已安装 `codebuddy` CLI（SDK 会自动查找），
> 且 `CODEBUDDY_API_KEY` 对应的账号需有可用积分。若环境未就绪，网关会返回明确的 502 错误。

## API 用法（OpenAI 兼容）

```bash
# 列出模型
curl http://localhost:8080/v1/models

# 非流式对话
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"codebuddy-default","messages":[{"role":"user","content":"你好"}]}'

# 流式对话（SSE）
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"codebuddy-default","stream":true,"messages":[{"role":"user","content":"你好"}]}'
```

## 接入 trpc-agent-go（本项目模型三协议适配层）

将网关当作一个标准的 OpenAI 协议 Provider 接入即可：

- `base_url` = `http://localhost:8080/v1`
- `api_key`  = 任意非空字符串（passthrough 真实上游时填对应 key；mock/codebuddy 可随便填）
- `model`    = `codebuddy-default`（或 `WB_MODELS` 中列出的任一名称）

这样本项目（goMultiAgentV2）即可在不直接持有模型密钥的情况下，统一经由本网关调用模型，
而在 `codebuddy` 后端下实际消耗的是 WorkBuddy/CodeBuddy 的积分。

## 配置项一览（环境变量）

| 变量 | 默认 | 说明 |
|------|------|------|
| `WB_LISTEN` | `:8080` | 监听地址 |
| `WB_BACKEND` | `mock` | `passthrough` / `mock` / `codebuddy` |
| `WB_BASE_URL` | `https://api.openai.com/v1` | passthrough 上游 base URL |
| `WB_API_KEY` | 空 | passthrough 上游 API Key |
| `WB_CODEBUDDY_SIDECAR` | `bridge/codebuddy_bridge.py` | sidecar 脚本路径 |
| `WB_PYTHON` | `python` | 运行 sidecar 的 python |
| `WB_DEFAULT_MODEL` | `codebuddy-default` | 缺省模型名 |
| `WB_MODELS` | 逗号分隔列表 | `/v1/models` 返回内容 |
| `CODEBUDDY_API_KEY` | 空 | CodeBuddy API Key（消耗积分） |
| `CODEBUDDY_INTERNET_ENVIRONMENT` | 空 | 中国版=internal / iOA 版=ioa |
