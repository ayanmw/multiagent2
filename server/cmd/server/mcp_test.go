package main

import (
	"net/http"
	"strconv"
	"testing"
)

// TestMCP_ManagementAPI 覆盖 M2-02 验收：developer 全生命周期 CRUD + owner 隔离 +
// viewer 写 403 + 非法 transport/缺必填 400；仅管理面，不装载工具。
func TestMCP_ManagementAPI(t *testing.T) {
	r, db := newRBACRouter(t)

	// developer（默认 developer 角色，含 mcp:read/write）。
	dev := &e2eClient{t: t, r: r}
	code, reg := dev.do("POST", "/api/auth/register", map[string]any{
		"username": "mcpdev", "email": "mcpdev@example.com", "password": "secret123",
	})
	if code != 201 {
		t.Fatalf("developer 注册失败: %d %v", code, reg)
	}
	dev.tok = reg["token"].(string)

	viewerTok := createViewerToken(t, db, "rbac-secret")

	// --- developer 创建 stdio MCP 配置（201）---
	code, body := dev.do("POST", "/api/mcp", map[string]any{
		"name": "fs", "transport": "stdio", "command": "npx",
		"args": []string{"-y", "@modelcontextprotocol/server-fs"},
		"env":  map[string]string{"FOO": "bar"},
	})
	if code != 201 {
		t.Fatalf("创建 stdio MCP 应 201, 实际 %d, body=%v", code, body)
	}
	id := int(body["id"].(float64))

	// --- 列表（200）且 total=1 ---
	code, body = dev.do("GET", "/api/mcp", nil)
	if code != 200 {
		t.Fatalf("列表应 200, 实际 %d", code)
	}
	if total, _ := body["total"].(float64); total != 1 {
		t.Fatalf("列表 total 应 1, 实际 %v", body["total"])
	}

	// --- 详情（200），env 原样回显 ---
	code, body = dev.do("GET", "/api/mcp/"+strconv.Itoa(id), nil)
	if code != 200 {
		t.Fatalf("详情应 200, 实际 %d", code)
	}
	env, _ := body["env"].(map[string]any)
	if env["FOO"] != "bar" {
		t.Fatalf("详情 env 回显错误: %v", body["env"])
	}

	// --- 更新（200）：关 enabled + 改描述 ---
	code, body = dev.do("PUT", "/api/mcp/"+strconv.Itoa(id), map[string]any{
		"enabled": false, "description": "disabled now",
	})
	if code != 200 {
		t.Fatalf("更新应 200, 实际 %d, body=%v", code, body)
	}
	if body["enabled"] != false || body["description"] != "disabled now" {
		t.Fatalf("更新内容不一致: %v", body)
	}

	// --- viewer 写操作应 403 ---
	v := &e2eClient{t: t, r: r, tok: viewerTok}
	for _, tc := range []struct{ m, p string }{
		{"POST", "/api/mcp"},
		{"PUT", "/api/mcp/" + strconv.Itoa(id)},
		{"DELETE", "/api/mcp/" + strconv.Itoa(id)},
	} {
		code, _ := v.do(tc.m, tc.p, map[string]any{"name": "x", "transport": "stdio", "command": "x"})
		if code != http.StatusForbidden {
			t.Errorf("viewer %s %s: 期望 403, 实际 %d", tc.m, tc.p, code)
		}
	}

	// --- 非法 transport（400）---
	code, _ = dev.do("POST", "/api/mcp", map[string]any{
		"name": "bad", "transport": "http", "command": "x",
	})
	if code != 400 {
		t.Errorf("非法 transport 应 400, 实际 %d", code)
	}

	// --- stdio 缺 command（400）---
	code, _ = dev.do("POST", "/api/mcp", map[string]any{
		"name": "bad2", "transport": "stdio",
	})
	if code != 400 {
		t.Errorf("stdio 缺 command 应 400, 实际 %d", code)
	}

	// --- sse 缺 url（400）---
	code, _ = dev.do("POST", "/api/mcp", map[string]any{
		"name": "bad3", "transport": "sse",
	})
	if code != 400 {
		t.Errorf("sse 缺 url 应 400, 实际 %d", code)
	}

	// --- owner 隔离：第二用户创建后，dev1 访问应 404 ---
	dev2 := &e2eClient{t: t, r: r}
	code, reg2 := dev2.do("POST", "/api/auth/register", map[string]any{
		"username": "mcpdev2", "email": "mcpdev2@example.com", "password": "secret123",
	})
	if code != 201 {
		t.Fatalf("dev2 注册失败: %d %v", code, reg2)
	}
	dev2.tok = reg2["token"].(string)
	code, body = dev2.do("POST", "/api/mcp", map[string]any{
		"name": "fs2", "transport": "sse", "url": "http://example.com/mcp",
	})
	if code != 201 {
		t.Fatalf("dev2 创建应 201, 实际 %d, body=%v", code, body)
	}
	id2 := int(body["id"].(float64))
	code, _ = dev.do("GET", "/api/mcp/"+strconv.Itoa(id2), nil)
	if code != http.StatusNotFound {
		t.Errorf("dev1 访问 dev2 的 MCP 应 404, 实际 %d", code)
	}

	// --- developer 删除（204）---
	code, _ = dev.do("DELETE", "/api/mcp/"+strconv.Itoa(id), nil)
	if code != http.StatusNoContent {
		t.Fatalf("删除应 204, 实际 %d", code)
	}
	code, _ = dev.do("GET", "/api/mcp/"+strconv.Itoa(id), nil)
	if code != http.StatusNotFound {
		t.Fatalf("删除后详情应 404, 实际 %d", code)
	}
}
