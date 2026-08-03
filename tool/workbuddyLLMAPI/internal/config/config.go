package config

import (
	"os"
	"strings"
)

// Config 控制网关监听地址与所选 backend 的行为。
// 所有字段均可由环境变量覆盖，便于容器/服务部署。
type Config struct {
	ListenAddr             string   // 监听地址，如 :8080
	Backend                string   // passthrough | mock | codebuddy
	BaseURL                string   // passthrough 模式的上游 OpenAI 兼容 base URL
	APIKey                 string   // passthrough 模式的上游 API Key
	CodeBuddyDaemonURL     string   // 本地 CodeBuddy/WorkBuddy 守护进程 HTTP 地址（ACP 直连，消耗积分）
	CodeBuddyCWD           string   // 守护进程 agent 的工作目录（ACP session/new 的 cwd）
	CodeBuddyModel         string   // 透传给守护进程的默认模型 id（hy3/glm-5.1/deepseek-v4-pro/...）；请求未指定时使用
	CodeBuddyFallbackModel string   // 主模型不可用时回退的模型 id（默认 deepseek-v4-pro）
	DefaultModel           string   // 请求未指定 model 时使用的默认模型名（占位 codebuddy-default → 走 CodeBuddyModel）
	Models                 []string // /v1/models 返回的模型列表
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Load 从环境变量读取配置，并对缺失项填充默认值。
func Load() *Config {
	// 真实目录取自本机 CodeBuddy CLI 内置模型清单（国产优先，含少量国外旗舰）。
	// 这些 id 直接透传给守护进程（copilot.tencent.com），由它按登录账号的实际可用模型路由。
	models := getenv("WB_MODELS", strings.Join([]string{
		"auto",
		"hy3",
		"glm-5.1", "glm-5", "glm-4.7", "glm-5v-turbo", "glm-4.5-air",
		"kimi-k2.6", "kimi-k2.5", "kimi-k2-thinking",
		"deepseek-v4-pro", "deepseek-v4-flash", "deepseek-r1-distill-llama-70b",
		"minimax-m3", "minimax-m2.7", "minimax-m2.5",
		"qwen3.6-plus", "qwen3.5-plus", "qwen3.7-max",
		"step",
		"gpt-5.2", "gpt-5.1", "gpt-5",
		"claude-opus-4-5", "claude-sonnet-4-5", "claude-haiku-4-5",
		"gemini-2.5-pro", "gemini-2.5-flash",
	}, ","))
	cfg := &Config{
		ListenAddr:             getenv("WB_LISTEN", ":8080"),
		Backend:                getenv("WB_BACKEND", "mock"),
		BaseURL:                getenv("WB_BASE_URL", "https://api.openai.com/v1"),
		APIKey:                 getenv("WB_API_KEY", ""),
		CodeBuddyDaemonURL:     getenv("WB_DAEMON_URL", "http://127.0.0.1:18765"),
		CodeBuddyCWD:           getenv("WB_DAEMON_CWD", "."),
		CodeBuddyModel:         getenv("WB_DAEMON_MODEL", "hy3"),
		CodeBuddyFallbackModel: getenv("WB_DAEMON_FALLBACK_MODEL", "deepseek-v4-pro"),
		DefaultModel:           getenv("WB_DEFAULT_MODEL", "codebuddy-default"),
	}
	for _, m := range strings.Split(models, ",") {
		m = strings.TrimSpace(m)
		if m != "" {
			cfg.Models = append(cfg.Models, m)
		}
	}
	return cfg
}
