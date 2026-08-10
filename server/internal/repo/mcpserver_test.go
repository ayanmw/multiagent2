package repo

import (
	"crypto/sha256"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ayanmw/multiagent2/server/internal/config"
	"github.com/ayanmw/multiagent2/server/internal/model"
)

// testEncKey 返回一把确定性的 32 字节 AES-256 主密钥（与 config.Load 同样用 sha256 派生）。
func testEncKey() []byte {
	sum := sha256.Sum256([]byte("mcp-test-encryption-key"))
	return sum[:]
}

func newMCPServerTestDB(t *testing.T) *DB {
	t.Helper()
	return newMCPServerTestDBAt(t, filepath.Join(t.TempDir(), "mcp.db"))
}

func newMCPServerTestDBAt(t *testing.T, path string) *DB {
	t.Helper()
	cfg := &config.Config{DBPath: path, EncryptionKey: testEncKey()}
	db, err := NewDB(cfg)
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, e := db.DB.DB(); e == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

// TestMCPServer_OwnerScopedCRUD 覆盖 M2-02 repo 层：创建/列表/查询/更新/删除，
// 以及 owner 隔离与同名查重（M3-07 起各接口带 encKey，env/headers 加解密透明往返）。
func TestMCPServer_OwnerScopedCRUD(t *testing.T) {
	db := newMCPServerTestDB(t)
	key := testEncKey()
	uid := uint(1)

	// 创建 stdio 配置（含 args/env，验证 serializer:json 与密文往返）。
	m := &model.MCPServer{
		UserID:    uid,
		Name:      "fs",
		Transport: model.MCPTransportStdio,
		Command:   "npx",
		Args:      []string{"-y", "@modelcontextprotocol/server-fs"},
		Env:       map[string]string{"FOO": "bar"},
	}
	if err := CreateMCPServer(db.DB, m, key); err != nil {
		t.Fatalf("create: %v", err)
	}
	if m.ID == 0 {
		t.Fatal("id not set after create")
	}

	// 列表
	list, err := ListMCPServers(db.DB, uid, key)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: err=%v len=%d", err, len(list))
	}
	if list[0].Env["FOO"] != "bar" {
		t.Fatalf("list 未解密 env: %+v", list[0].Env)
	}

	// 按 id 查（owner-scoped）+ JSON 字段往返
	got, err := GetMCPServerByID(db.DB, uid, m.ID, key)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Command != "npx" || len(got.Args) != 2 || got.Args[0] != "-y" {
		t.Fatalf("args 往返不一致: %+v", got)
	}
	if got.Env["FOO"] != "bar" {
		t.Fatalf("env 往返不一致: %+v", got.Env)
	}

	// 跨用户查 → not found
	if _, err := GetMCPServerByID(db.DB, 999, m.ID, key); err != ErrMCPServerNotFound {
		t.Fatalf("跨用户查应 ErrMCPServerNotFound, got %v", err)
	}

	// 更新
	got.Enabled = false
	got.Description = "updated"
	if err := UpdateMCPServer(db.DB, got, key); err != nil {
		t.Fatalf("update: %v", err)
	}
	got2, _ := GetMCPServerByID(db.DB, uid, m.ID, key)
	if got2.Enabled || got2.Description != "updated" {
		t.Fatalf("update 未落库: %+v", got2)
	}
	if got2.Env["FOO"] != "bar" {
		t.Fatalf("update 后 env 丢失: %+v", got2.Env)
	}

	// 同名查重：已存在应返回该行，缺失才是 ErrMCPServerNotFound。
	if _, err := GetMCPServerByName(db.DB, uid, "fs", key); err != nil {
		t.Fatalf("GetMCPServerByName 已存在应返回该行, got %v", err)
	}
	if _, err := GetMCPServerByName(db.DB, uid, "nope", key); err != ErrMCPServerNotFound {
		t.Fatalf("GetMCPServerByName 缺失应 ErrMCPServerNotFound, got %v", err)
	}

	// 删除
	if err := DeleteMCPServer(db.DB, m.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := GetMCPServerByID(db.DB, uid, m.ID, key); err != ErrMCPServerNotFound {
		t.Fatalf("删除后应 not found, got %v", err)
	}
}

// TestMCPServer_SecretsEncryptedAtRest 验收 M3-07 核心目标：
// 库内 env/headers 只有密文，明文 token 不落盘；换一把密钥则解不开。
func TestMCPServer_SecretsEncryptedAtRest(t *testing.T) {
	db := newMCPServerTestDB(t)
	key := testEncKey()
	uid := uint(7)
	const secret = "sk-super-secret-token"

	m := &model.MCPServer{
		UserID:    uid,
		Name:      "remote",
		Transport: model.MCPTransportSSE,
		URL:       "https://example.com/sse",
		Env:       map[string]string{"API_TOKEN": secret},
		Headers:   map[string]string{"Authorization": "Bearer " + secret},
	}
	if err := CreateMCPServer(db.DB, m, key); err != nil {
		t.Fatalf("create: %v", err)
	}

	// 直接读原始列：必须是密文，且整行任何列都不得包含明文。
	var row struct {
		EnvEnc     string `gorm:"column:env_enc"`
		HeadersEnc string `gorm:"column:headers_enc"`
	}
	if err := db.DB.Raw("SELECT env_enc, headers_enc FROM mcp_servers WHERE id = ?", m.ID).
		Scan(&row).Error; err != nil {
		t.Fatalf("raw select: %v", err)
	}
	if row.EnvEnc == "" || row.HeadersEnc == "" {
		t.Fatalf("密文列为空: %+v", row)
	}
	if strings.Contains(row.EnvEnc, secret) || strings.Contains(row.HeadersEnc, secret) {
		t.Fatalf("密文列疑似明文: %+v", row)
	}
	if strings.Contains(row.EnvEnc, "API_TOKEN") || strings.Contains(row.HeadersEnc, "Authorization") {
		t.Fatalf("密文列泄漏键名: %+v", row)
	}

	// 换一把密钥读 → 必须失败，而不是静默返回空配置。
	badKey := sha256.Sum256([]byte("another-key"))
	if _, err := GetMCPServerByID(db.DB, uid, m.ID, badKey[:]); err == nil {
		t.Fatal("错误密钥应解密失败，实际成功")
	}

	// 正确密钥读 → 明文可用，供 toolsearch 真实装载。
	got, err := GetMCPServerByID(db.DB, uid, m.ID, key)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Env["API_TOKEN"] != secret || got.Headers["Authorization"] != "Bearer "+secret {
		t.Fatalf("解密结果不一致: env=%+v headers=%+v", got.Env, got.Headers)
	}
	// 掩码视图只暴露键名与「有无」。
	if !got.HasEnv() || !got.HasHeaders() {
		t.Fatal("HasEnv/HasHeaders 应为 true")
	}
	if len(got.EnvKeys()) != 1 || got.EnvKeys()[0] != "API_TOKEN" {
		t.Fatalf("EnvKeys 不符: %v", got.EnvKeys())
	}
}

// TestMCPServer_LegacyPlaintextMigration 验证已有库的明文 env/headers 在启动迁移中
// 被就地加密，且原明文列被清空（M3-07 迁移路径，幂等）。
func TestMCPServer_LegacyPlaintextMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	key := testEncKey()
	const secret = "legacy-token-value"

	// 1) 用现行 schema 建库，再手工补出「遗留明文列」并写入明文，模拟旧库。
	db := newMCPServerTestDBAt(t, path)
	if err := db.DB.Exec("ALTER TABLE mcp_servers ADD COLUMN `env` text").Error; err != nil {
		t.Fatalf("add legacy env column: %v", err)
	}
	if err := db.DB.Exec("ALTER TABLE mcp_servers ADD COLUMN `headers` text").Error; err != nil {
		t.Fatalf("add legacy headers column: %v", err)
	}
	envJSON, _ := json.Marshal(map[string]string{"API_TOKEN": secret})
	if err := db.DB.Exec(`INSERT INTO mcp_servers
		(created_at, updated_at, user_id, name, transport, url, enabled, env, headers, env_enc, headers_enc)
		VALUES (datetime('now'), datetime('now'), 3, 'legacy', 'sse', 'https://x/sse', 1, ?, 'null', '', '')`,
		string(envJSON)).Error; err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}
	// 还原「M3-08 之前的旧库」前提：那时没有版本表，每次启动都会跑一遍数据修复。
	// M3-08 起迁移按版本只执行一次，若保留版本表则 0003/0004 会被跳过——那测的就
	// 不是真实升级路径了。删掉版本表即等价于「一个 M3-07 时期落盘的库首次升级」。
	if err := db.DB.Exec("DROP TABLE IF EXISTS schema_migrations").Error; err != nil {
		t.Fatalf("drop schema_migrations: %v", err)
	}
	sqlDB, _ := db.DB.DB()
	_ = sqlDB.Close()

	// 2) 重新打开（触发版本化迁移：0003 就地加密 → 0004 物理删除遗留明文列）。
	db2 := newMCPServerTestDBAt(t, path)

	// 遗留明文列应被 0004 彻底删除（AutoMigrate 做不到删列，正是 M3-08 的价值）。
	var ddl string
	if err := db2.DB.Raw(`SELECT sql FROM sqlite_master WHERE type='table' AND name='mcp_servers'`).
		Scan(&ddl).Error; err != nil {
		t.Fatalf("read ddl: %v", err)
	}
	if strings.Contains(ddl, "`env`") || strings.Contains(ddl, "`headers`") {
		t.Fatalf("遗留明文列未被删除: %s", ddl)
	}

	var envEnc string
	if err := db2.DB.Raw("SELECT env_enc FROM mcp_servers WHERE name = 'legacy'").
		Scan(&envEnc).Error; err != nil {
		t.Fatalf("raw select: %v", err)
	}
	if envEnc == "" || strings.Contains(envEnc, secret) {
		t.Fatalf("迁移后密文异常: %q", envEnc)
	}

	// 3) 迁移后仍可正常解密使用。
	list, err := ListMCPServers(db2.DB, 3, key)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: err=%v len=%d", err, len(list))
	}
	if list[0].Env["API_TOKEN"] != secret {
		t.Fatalf("迁移后解密不一致: %+v", list[0].Env)
	}

	// 4) 幂等：再开一次不应报错，也不应破坏已有密文。
	sqlDB2, _ := db2.DB.DB()
	_ = sqlDB2.Close()
	db3 := newMCPServerTestDBAt(t, path)
	list3, err := ListMCPServers(db3.DB, 3, key)
	if err != nil || len(list3) != 1 || list3[0].Env["API_TOKEN"] != secret {
		t.Fatalf("二次迁移破坏数据: err=%v list=%+v", err, list3)
	}
}
