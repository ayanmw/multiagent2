package config

import (
	"os"
	"strings"
)

// Config 控制网关监听地址与所选 backend 的行为。
// 所有字段均可由环境变量覆盖，便于容器/服务部署。
type Config struct {
	ListenAddr       string   // 监听地址，如 :8080
	Backend          string   // passthrough | mock | codebuddy
	BaseURL          string   // passthrough 模式的上游 OpenAI 兼容 base URL
	APIKey           string   // passthrough 模式的上游 API Key
	CodeBuddySidecar string   // codebuddy 模式的 Python sidecar 脚本路径
	CodeBuddyAPIKey  string   // CodeBuddy API Key（消耗积分），即 CODEBUDDY_API_KEY
	CodeBuddyEnv     string   // 中国版=internal / iOA 版=ioa，留空为海外版
	SidecarPython    string   // 运行 sidecar 的 python 可执行文件
	DefaultModel     string   // 请求未指定 model 时使用的默认模型名
	Models           []string // /v1/models 返回的模型列表
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Load 从环境变量读取配置，并对缺失项填充默认值。
func Load() *Config {
	models := getenv("WB_MODELS", "gpt-4o-mini,gpt-4o,claude-3.5-sonnet,codebuddy-default")
	cfg := &Config{
		ListenAddr:       getenv("WB_LISTEN", ":8080"),
		Backend:          getenv("WB_BACKEND", "mock"),
		BaseURL:          getenv("WB_BASE_URL", "https://api.openai.com/v1"),
		APIKey:           getenv("WB_API_KEY", ""),
		CodeBuddySidecar: getenv("WB_CODEBUDDY_SIDECAR", "bridge/codebuddy_bridge.py"),
		CodeBuddyAPIKey:  getenv("CODEBUDDY_API_KEY", ""),
		CodeBuddyEnv:     getenv("CODEBUDDY_INTERNET_ENVIRONMENT", ""),
		SidecarPython:    getenv("WB_PYTHON", "python"),
		DefaultModel:     getenv("WB_DEFAULT_MODEL", "codebuddy-default"),
	}
	for _, m := range strings.Split(models, ",") {
		m = strings.TrimSpace(m)
		if m != "" {
			cfg.Models = append(cfg.Models, m)
		}
	}
	return cfg
}
