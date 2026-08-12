package main

import (
	"net/http"
	"testing"
)

// TestKnowledgeAPI_FullLifecycle 验证 M5-02 后端：知识库 CRUD + 文档索引 + 检索 + owner 隔离。
// 走真实 Gin 路由（RBAC knowledge:read/write），使用 glebarez 纯 Go SQLite（无需 gcc）。
func TestKnowledgeAPI_FullLifecycle(t *testing.T) {
	r, db := newRBACRouter(t)

	// developer 经正常注册得到（含 knowledge:read/write）。
	dev := &e2eClient{t: t, r: r}
	code, reg := dev.do("POST", "/api/auth/register", map[string]any{
		"username": "kbdev", "email": "kbdev@example.com", "password": "secret123",
	})
	if code != 201 {
		t.Fatalf("developer 注册失败: %d %v", code, reg)
	}
	dev.tok = reg["token"].(string)

	// viewer 仅有 knowledge:read（写应 403）。
	viewerTok := createViewerToken(t, db, "rbac-secret")
	v := &e2eClient{t: t, r: r, tok: viewerTok}

	// 1) 创建知识库（developer 应 200）。
	code, body := dev.do("POST", "/api/knowledge", map[string]any{
		"name": "我的知识库", "description": "测试",
	})
	if code != http.StatusOK {
		t.Fatalf("创建知识库应 200, 实际 %d, body=%v", code, body)
	}
	kbID := uint(body["id"].(float64))

	// viewer 创建应 403。
	if code, _ := v.do("POST", "/api/knowledge", map[string]any{"name": "x"}); code != http.StatusForbidden {
		t.Fatalf("viewer 创建知识库应 403, 实际 %d", code)
	}

	// 2) 列出知识库（developer 应 1 条）。
	code, body = dev.do("GET", "/api/knowledge", nil)
	if code != http.StatusOK {
		t.Fatalf("列出知识库应 200, 实际 %d", code)
	}
	if int(body["total"].(float64)) != 1 {
		t.Fatalf("期望 1 个知识库, 实际 %v", body["total"])
	}

	// 3) 索引文档（developer 应返回切片数 > 0）。
	code, body = dev.do("POST", "/api/knowledge/"+itoa(kbID)+"/documents", map[string]any{
		"name":         "go.md",
		"content":      "Go 语言使用 goroutine 实现并发，channel 用于 goroutine 间通信。",
		"content_type": "text",
	})
	if code != http.StatusOK {
		t.Fatalf("索引文档应 200, 实际 %d, body=%v", code, body)
	}
	if int(body["indexed_chunks"].(float64)) <= 0 {
		t.Fatalf("期望 indexed_chunks > 0, 实际 %v", body["indexed_chunks"])
	}

	// 4) 文档列表（developer 应 1 个来源）。
	code, body = dev.do("GET", "/api/knowledge/"+itoa(kbID)+"/documents", nil)
	if code != http.StatusOK {
		t.Fatalf("文档列表应 200, 实际 %d", code)
	}
	if int(body["total"].(float64)) != 1 {
		t.Fatalf("期望 1 个文档, 实际 %v", body["total"])
	}

	// 5) 检索（developer 应命中相关切片）。
	code, body = dev.do("POST", "/api/knowledge/"+itoa(kbID)+"/search", map[string]any{
		"query": "Go goroutine 并发", "top_k": 3,
	})
	if code != http.StatusOK {
		t.Fatalf("检索应 200, 实际 %d, body=%v", code, body)
	}
	if int(body["total"].(float64)) == 0 {
		t.Fatalf("期望至少 1 条检索命中, 实际 %v", body["total"])
	}

	// 6) owner 隔离：viewer 访问 developer 的知识库应 404（无归属）。
	if code, _ := v.do("GET", "/api/knowledge/"+itoa(kbID), nil); code != http.StatusNotFound {
		t.Fatalf("viewer 访问他人知识库应 404, 实际 %d", code)
	}

	// 7) 删除文档（developer 应成功）。
	code, body = dev.do("DELETE", "/api/knowledge/"+itoa(kbID)+"/documents/go.md", nil)
	if code != http.StatusOK {
		t.Fatalf("删除文档应 200, 实际 %d, body=%v", code, body)
	}

	// 8) 删除知识库（developer 应成功）。
	code, body = dev.do("DELETE", "/api/knowledge/"+itoa(kbID), nil)
	if code != http.StatusOK {
		t.Fatalf("删除知识库应 200, 实际 %d, body=%v", code, body)
	}
	if deleted, ok := body["deleted"].(bool); !ok || !deleted {
		t.Fatalf("删除知识库应返回 deleted=true, 实际 %v", body)
	}
	// 删除后列出应为 0。
	code, body = dev.do("GET", "/api/knowledge", nil)
	if code != http.StatusOK || int(body["total"].(float64)) != 0 {
		t.Fatalf("删除后期望 0 个知识库, 实际 code=%d total=%v", code, body["total"])
	}
}

// itoa 是测试内的小工具：uint → 十进制字符串。
func itoa(u uint) string {
	if u == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for u > 0 {
		i--
		buf[i] = byte('0' + u%10)
		u /= 10
	}
	return string(buf[i:])
}
