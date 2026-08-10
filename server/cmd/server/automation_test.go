package main

import (
	"net/http"
	"strconv"
	"testing"
)

// TestAutomation_CRUD 覆盖 M4-01 验收：developer 全生命周期 CRUD + owner 隔离 +
// viewer 写 403 + 非法/缺必填字段 400。仅数据模型与持久化（M4-02/03 消费）。
func TestAutomation_CRUD(t *testing.T) {
	r, db := newRBACRouter(t)

	// developer（默认 developer 角色，含 automations:read/write）。
	dev := &e2eClient{t: t, r: r}
	code, reg := dev.do("POST", "/api/auth/register", map[string]any{
		"username": "autodev", "email": "autodev@example.com", "password": "secret123",
	})
	if code != 201 {
		t.Fatalf("developer 注册失败: %d %v", code, reg)
	}
	dev.tok = reg["token"].(string)

	viewerTok := createViewerToken(t, db, "rbac-secret")

	// --- developer 创建 cron Automation（201）---
	code, body := dev.do("POST", "/api/automations", map[string]any{
		"name":         "nightly-report",
		"trigger_type": "cron",
		"cron_expr":    "0 2 * * *",
		"goal_prompt":  "生成昨日运营日报",
	})
	if code != 201 {
		t.Fatalf("创建 cron Automation 应 201, 实际 %d, body=%v", code, body)
	}
	id := int(body["id"].(float64))
	if body["trigger_type"] != "cron" || body["cron_expr"] != "0 2 * * *" || body["enabled"] != true {
		t.Fatalf("创建返回内容不符: %v", body)
	}
	if _, leaked := body["webhook_token"]; leaked {
		t.Fatalf("不应回显 webhook_token: %v", body)
	}

	// --- webhook Automation 创建应自动生成令牌（不回显）---
	code, body = dev.do("POST", "/api/automations", map[string]any{
		"name":         "pr-watcher",
		"trigger_type": "webhook",
		"goal_prompt":  "处理新开 PR",
	})
	if code != 201 {
		t.Fatalf("创建 webhook Automation 应 201, 实际 %d, body=%v", code, body)
	}
	idWebhook := int(body["id"].(float64))
	if body["trigger_type"] != "webhook" {
		t.Fatalf("webhook 类型不符: %v", body)
	}
	// 库内应已写入非空令牌（M4-03 按令牌匹配外部事件）。
	var tok string
	if err := db.DB.Raw("SELECT webhook_token FROM automations WHERE id = ?", idWebhook).Scan(&tok).Error; err != nil {
		t.Fatalf("读取 webhook_token: %v", err)
	}
	if tok == "" {
		t.Fatalf("webhook Automation 应自动生成令牌")
	}

	// --- 列表（200）且 total=2 ---
	code, body = dev.do("GET", "/api/automations", nil)
	if code != 200 {
		t.Fatalf("列表应 200, 实际 %d", code)
	}
	if total, _ := body["total"].(float64); total != 2 {
		t.Fatalf("列表 total 应 2, 实际 %v", body["total"])
	}

	// --- 详情（200）---
	code, body = dev.do("GET", "/api/automations/"+strconv.Itoa(id), nil)
	if code != 200 {
		t.Fatalf("详情应 200, 实际 %d", code)
	}
	if body["name"] != "nightly-report" {
		t.Fatalf("详情 name 不符: %v", body)
	}

	// --- 更新（200）：改 cron_expr + 关 enabled ---
	code, body = dev.do("PUT", "/api/automations/"+strconv.Itoa(id), map[string]any{
		"cron_expr": "*/5 * * * *", "enabled": false,
	})
	if code != 200 {
		t.Fatalf("更新应 200, 实际 %d, body=%v", code, body)
	}
	if body["cron_expr"] != "*/5 * * * *" || body["enabled"] != false {
		t.Fatalf("更新内容不一致: %v", body)
	}

	// --- viewer 写操作应 403 ---
	v := &e2eClient{t: t, r: r, tok: viewerTok}
	for _, tc := range []struct{ m, p string }{
		{"POST", "/api/automations"},
		{"PUT", "/api/automations/" + strconv.Itoa(id)},
		{"DELETE", "/api/automations/" + strconv.Itoa(id)},
	} {
		code, _ := v.do(tc.m, tc.p, map[string]any{
			"name": "x", "trigger_type": "cron", "cron_expr": "* * * * *", "goal_prompt": "p",
		})
		if code != http.StatusForbidden {
			t.Errorf("viewer %s %s: 期望 403, 实际 %d", tc.m, tc.p, code)
		}
	}
	// viewer 读操作应放行（200）。
	if code, _ := v.do("GET", "/api/automations", nil); code != http.StatusOK {
		t.Errorf("viewer GET /api/automations: 期望 200, 实际 %d", code)
	}

	// --- 非法 trigger_type（400）---
	code, _ = dev.do("POST", "/api/automations", map[string]any{
		"name": "bad", "trigger_type": "interval", "goal_prompt": "p",
	})
	if code != 400 {
		t.Errorf("非法 trigger_type 应 400, 实际 %d", code)
	}

	// --- cron 缺 cron_expr（400）---
	code, _ = dev.do("POST", "/api/automations", map[string]any{
		"name": "bad2", "trigger_type": "cron", "goal_prompt": "p",
	})
	if code != 400 {
		t.Errorf("cron 缺 cron_expr 应 400, 实际 %d", code)
	}

	// --- 缺 goal_prompt（400，binding 校验）---
	code, _ = dev.do("POST", "/api/automations", map[string]any{
		"name": "bad3", "trigger_type": "cron", "cron_expr": "* * * * *",
	})
	if code != 400 {
		t.Errorf("缺 goal_prompt 应 400, 实际 %d", code)
	}

	// --- owner 隔离：第二用户创建后，dev1 访问应 404 ---
	dev2 := &e2eClient{t: t, r: r}
	code, reg2 := dev2.do("POST", "/api/auth/register", map[string]any{
		"username": "autodev2", "email": "autodev2@example.com", "password": "secret123",
	})
	if code != 201 {
		t.Fatalf("dev2 注册失败: %d %v", code, reg2)
	}
	dev2.tok = reg2["token"].(string)
	code, body = dev2.do("POST", "/api/automations", map[string]any{
		"name": "other", "trigger_type": "cron", "cron_expr": "0 0 * * *", "goal_prompt": "g",
	})
	if code != 201 {
		t.Fatalf("dev2 创建应 201, 实际 %d, body=%v", code, body)
	}
	id2 := int(body["id"].(float64))
	code, _ = dev.do("GET", "/api/automations/"+strconv.Itoa(id2), nil)
	if code != http.StatusNotFound {
		t.Errorf("dev1 访问 dev2 的 Automation 应 404, 实际 %d", code)
	}

	// --- developer 删除（204）---
	code, _ = dev.do("DELETE", "/api/automations/"+strconv.Itoa(id), nil)
	if code != http.StatusNoContent {
		t.Fatalf("删除应 204, 实际 %d", code)
	}
	code, _ = dev.do("GET", "/api/automations/"+strconv.Itoa(id), nil)
	if code != http.StatusNotFound {
		t.Fatalf("删除后详情应 404, 实际 %d", code)
	}
}
