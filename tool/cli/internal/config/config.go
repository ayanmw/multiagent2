// Package config 管理 CLI 的本地配置（后端地址、JWT、账号）。
// 配置文件落在 os.UserConfigDir()/gm-agent-cli/config.json，跨平台一致。
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// DefaultBaseURL 是后端默认地址（与前端 dev 代理同源约定）。
const DefaultBaseURL = "http://localhost:8080"

// Config 是 CLI 的持久化配置。
type Config struct {
	BaseURL string `json:"base_url"`
	Token   string `json:"token"`
	Account string `json:"account"`
	ModelID uint   `json:"model_id"`
}

// Path 返回默认配置文件路径。
func Path() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "gm-agent-cli", "config.json")
}

// PathSafe 返回默认配置文件路径（供命令打印用，等价于 Path）。
func (c *Config) PathSafe() string {
	return Path()
}

// LoadPath 读取配置；path 为空时用默认路径。文件不存在时返回带默认值的空配置（不报错）。
func LoadPath(path string) (*Config, error) {
	if path == "" {
		path = Path()
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{BaseURL: DefaultBaseURL}, nil
		}
		return nil, err
	}
	c := &Config{BaseURL: DefaultBaseURL}
	if err := json.Unmarshal(b, c); err != nil {
		return nil, err
	}
	if c.BaseURL == "" {
		c.BaseURL = DefaultBaseURL
	}
	return c, nil
}

// Save 将配置写回默认路径（自动建目录，权限 0600）。
func (c *Config) Save() error {
	p := Path()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o600)
}
