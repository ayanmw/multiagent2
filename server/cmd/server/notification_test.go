package main

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"
)

// TestNotifications_ListAndRead 验证通知中心 API（M4-07）：
// ① developer 能看到自己通知；② 未读计数正确；③ 标记已读后未读归零；④ viewer 无权写。
func TestNotifications_ListAndRead(t *testing.T) {
	r, db := newRBACRouter(t)

	// 注册一个 developer（默认 developer 角色，含 notifications:read/write）。
	dev := &e2eClient{t: t, r: r}
	code, reg := dev.do("POST", "/api/auth/register", map[string]any{
		"username": "notifdev", "email": "notifdev@example.com", "password": "secret123",
	})
	if code != 201 {
		t.Fatalf("注册失败: %d %v", code, reg)
	}
	dev.tok = reg["token"].(string)
	uid := uint(reg["user"].(map[string]any)["id"].(float64))

	// 直接经 repo 造两条站内信（成功 + 检查点），模拟自主 Loop 写入。
	if err := repo.CreateNotification(db.DB, &model.Notification{
		UserID: uid, Type: model.NotificationTypeSuccess,
		Title: "自动化已完成", Message: "done", RefKind: model.NotificationRefAutomation, RefID: "1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateNotification(db.DB, &model.Notification{
		UserID: uid, Type: model.NotificationTypeCheckpoint,
		Title: "有待审批命令", Message: "checkpoint", RefKind: model.NotificationRefCheckpoint, RefID: "CP-9",
	}); err != nil {
		t.Fatal(err)
	}

	// 列表：应包含 2 条，unread=2。
	code, body := dev.do("GET", "/api/notifications", nil)
	if code != 200 {
		t.Fatalf("列表失败: %d %v", code, body)
	}
	total := int64(body["total"].(float64))
	unread := int64(body["unread"].(float64))
	if total != 2 || unread != 2 {
		t.Fatalf("期望 total=2/unread=2，实际 %d/%d", total, unread)
	}
	notifs := body["notifications"].([]any)
	if len(notifs) != 2 {
		t.Fatalf("期望返回 2 条通知，实际 %d", len(notifs))
	}

	// 取第一条 id 标记已读。
	firstID := uint(notifs[0].(map[string]any)["id"].(float64))
	code, _ = dev.do("POST", "/api/notifications/"+strconv.FormatUint(uint64(firstID), 10)+"/read", nil)
	if code != 200 {
		t.Fatalf("标记已读失败: %d", code)
	}
	code, body = dev.do("GET", "/api/notifications", nil)
	if code != 200 {
		t.Fatalf("二次列表失败: %d %v", code, body)
	}
	if int64(body["unread"].(float64)) != 1 {
		t.Fatalf("标记后 unread 应为 1，实际 %d", int64(body["unread"].(float64)))
	}

	// 全部已读。
	code, _ = dev.do("POST", "/api/notifications/read-all", nil)
	if code != 200 {
		t.Fatalf("全部已读失败: %d", code)
	}
	code, body = dev.do("GET", "/api/notifications", nil)
	if int64(body["unread"].(float64)) != 0 {
		t.Fatalf("全部已读后 unread 应为 0，实际 %d", int64(body["unread"].(float64)))
	}

	// viewer 无权写（标记已读应 403）。
	viewerTok := createViewerToken(t, db, "rbac-secret")
	req, _ := http.NewRequest(http.MethodPost, "/api/notifications/read-all", nil)
	req.Header.Set("Authorization", "Bearer "+viewerTok)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("viewer 标记已读应 403，得到 %d", w.Code)
	}
}
