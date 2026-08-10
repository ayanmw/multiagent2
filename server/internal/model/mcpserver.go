package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/ayanmw/multiagent2/server/internal/crypto"
	"gorm.io/gorm"
)

// MCPTransport 枚举一个 MCP 服务器配置支持的传输方式（M2-02）。
//   - stdio:      本地子进程，需 command（可选 args / env）
//   - sse:        远程 Server-Sent-Events 端点，需 url（可选 headers）
//   - streamable: 远程 Streamable HTTP 端点，需 url（可选 headers）
type MCPTransport string

const (
	MCPTransportStdio      MCPTransport = "stdio"
	MCPTransportSSE        MCPTransport = "sse"
	MCPTransportStreamable MCPTransport = "streamable"
)

// ValidMCPTransports 列出所有合法 transport 值（供校验与前端提示）。
var ValidMCPTransports = []MCPTransport{
	MCPTransportStdio, MCPTransportSSE, MCPTransportStreamable,
}

// ParseMCPTransport 校验并归一化 transport 字符串（大小写/空白容错）。
func ParseMCPTransport(s string) (MCPTransport, bool) {
	t := MCPTransport(strings.TrimSpace(strings.ToLower(s)))
	switch t {
	case MCPTransportStdio, MCPTransportSSE, MCPTransportStreamable:
		return t, true
	}
	return "", false
}

// MCPServer 表示一个用户归属的 MCP 服务器配置（M2-02 管理面）。
//
// 管理面只做「配置持久化 + 校验 + 管理 API」，不在此装载工具；真实装载
// 由 M2-06 toolsearch 按需调用框架 tool/mcp 完成（届时读取本表配置）。
//
// 配置字段按 transport 分两组：
//   - stdio:          Command / Args / Env
//   - sse|streamable: URL / Headers
//
// Args 经 GORM `serializer:json` 与 DB 互转 JSON。
//
// **M3-07 敏感字段加密**：Env / Headers 常含 token 等机密，故不再明文落库——
// 二者是 `gorm:"-"` 的**瞬态明文字段**（仅进程内存在），持久化的是 EnvEnc /
// HeadersEnc 两列 AES-256-GCM 密文（base64(nonce||ct)，对齐 Provider.APIKeyEnc）。
// 写库前调 SealSecrets 加密、读库后调 OpenSecrets 解密（由 repo 层统一负责）。
// 两者的 json tag 均为 `-`，即便整行被误序列化也不会泄漏明文。
type MCPServer struct {
	gorm.Model
	UserID    uint         `gorm:"not null;index" json:"user_id"`
	Name      string       `gorm:"size:128;not null;uniqueIndex:idx_user_mcp,priority:1" json:"name"`
	Transport MCPTransport `gorm:"size:32;not null" json:"transport"`
	Command   string       `gorm:"size:256" json:"command"`
	Args      []string     `gorm:"serializer:json" json:"args"`
	URL       string       `gorm:"size:512" json:"url"`

	// Env / Headers 是瞬态明文（不落库、不出 JSON），生命周期仅限单次请求处理。
	Env     map[string]string `gorm:"-" json:"-"`
	Headers map[string]string `gorm:"-" json:"-"`

	// EnvEnc / HeadersEnc 是落库的 AES-256-GCM 密文（空串表示未配置）。
	EnvEnc     string `gorm:"type:text" json:"-"`
	HeadersEnc string `gorm:"type:text" json:"-"`

	Enabled     bool   `gorm:"not null;default:true" json:"enabled"`
	Description string `gorm:"size:512" json:"description"`
}

// TableName overrides the default GORM table name.
func (MCPServer) TableName() string { return "mcp_servers" }

// SealSecrets 把瞬态明文 Env / Headers 加密进 EnvEnc / HeadersEnc（写库前调用）。
// 空 map（或 nil）落成空串，表示「未配置」，便于区分「没有」与「空对象」。
func (m *MCPServer) SealSecrets(key []byte) error {
	enc, err := sealSecretMap(m.Env, key)
	if err != nil {
		return fmt.Errorf("encrypt env: %w", err)
	}
	m.EnvEnc = enc
	enc, err = sealSecretMap(m.Headers, key)
	if err != nil {
		return fmt.Errorf("encrypt headers: %w", err)
	}
	m.HeadersEnc = enc
	return nil
}

// OpenSecrets 解密 EnvEnc / HeadersEnc 回填瞬态明文 Env / Headers（读库后调用）。
// 密钥不匹配或密文损坏时返回错误（fail loud，避免带着缺失的鉴权头静默连上游）。
func (m *MCPServer) OpenSecrets(key []byte) error {
	envMap, err := openSecretMap(m.EnvEnc, key)
	if err != nil {
		return fmt.Errorf("decrypt env: %w", err)
	}
	headerMap, err := openSecretMap(m.HeadersEnc, key)
	if err != nil {
		return fmt.Errorf("decrypt headers: %w", err)
	}
	m.Env, m.Headers = envMap, headerMap
	return nil
}

// EnvKeys 返回已配置的 env 键名（升序），用于对外掩码回显——只给键不给值。
func (m *MCPServer) EnvKeys() []string { return sortedKeys(m.Env) }

// HeaderKeys 返回已配置的 headers 键名（升序），用于对外掩码回显。
func (m *MCPServer) HeaderKeys() []string { return sortedKeys(m.Headers) }

// HasEnv 表示该配置是否存有 env 密文（不解密即可判断，供列表视图使用）。
func (m *MCPServer) HasEnv() bool { return m.EnvEnc != "" }

// HasHeaders 表示该配置是否存有 headers 密文。
func (m *MCPServer) HasHeaders() bool { return m.HeadersEnc != "" }

// sealSecretMap 把字符串映射序列化成 JSON 后 AES-256-GCM 加密；空映射返回空串。
func sealSecretMap(m map[string]string, key []byte) (string, error) {
	if len(m) == 0 {
		return "", nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return crypto.Encrypt(string(raw), key)
}

// openSecretMap 是 sealSecretMap 的逆操作；空串返回 nil 映射。
func openSecretMap(enc string, key []byte) (map[string]string, error) {
	if enc == "" {
		return nil, nil
	}
	plain, err := crypto.Decrypt(enc, key)
	if err != nil {
		return nil, err
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(plain), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// sortedKeys 返回映射键的升序切片（nil 映射返回空切片，便于 JSON 序列化成 []）。
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Validate 校验配置自洽性：名称必填；transport 必为合法值；
// 不同 transport 对必填字段有不同要求（stdio→command，sse/streamable→url）。
func (m *MCPServer) Validate() error {
	if strings.TrimSpace(m.Name) == "" {
		return errors.New("name is required")
	}
	if _, ok := ParseMCPTransport(string(m.Transport)); !ok {
		return fmt.Errorf("invalid transport %q (must be one of stdio/sse/streamable)", m.Transport)
	}
	switch m.Transport {
	case MCPTransportStdio:
		if strings.TrimSpace(m.Command) == "" {
			return errors.New("command is required for stdio transport")
		}
	case MCPTransportSSE, MCPTransportStreamable:
		if strings.TrimSpace(m.URL) == "" {
			return fmt.Errorf("url is required for %s transport", m.Transport)
		}
	}
	return nil
}
