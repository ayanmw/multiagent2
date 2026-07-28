package backend

import (
	"fmt"

	"workbuddyllmapi/internal/config"
	"workbuddyllmapi/internal/openai"
)

// New 依据配置构造对应 backend。返回的实例都满足 openai.Backend 接口。
func New(cfg *config.Config) (openai.Backend, error) {
	switch cfg.Backend {
	case "passthrough":
		return NewPassthrough(cfg), nil
	case "mock":
		return NewMock(cfg), nil
	case "codebuddy":
		return NewCodeBuddy(cfg), nil
	default:
		return nil, fmt.Errorf("unknown backend: %s (want passthrough|mock|codebuddy)", cfg.Backend)
	}
}
