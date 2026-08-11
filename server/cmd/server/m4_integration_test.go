package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/api"
	"github.com/ayanmw/multiagent2/server/internal/artifact"
	"github.com/ayanmw/multiagent2/server/internal/config"
	"github.com/ayanmw/multiagent2/server/internal/engine"
	"github.com/ayanmw/multiagent2/server/internal/metrics"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/notify"
	"github.com/ayanmw/multiagent2/server/internal/provider"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/ayanmw/multiagent2/server/internal/scheduler"
	"github.com/gin-gonic/gin"
)

// TestM4_Autonomy_E2E 是 M4（自主化）在 HTTP 层的集成验证，覆盖「建定时 Automation →
// 到点自动跑 Loop → 产出结果 → 跨重启恢复 → 完成通知」全链路：
//
//   - ② 定时触发（M4-02）：建 cron Automation → 把 next_run 置过去 → 调度器同步 TickSync
//     触发 → 经统一 Gateway（cronRunner）跑 Loop → 运行标记 done + 站内信「完成」通知。
//   - ③ Webhook 入口（M4-03 + M4-07）：POST /api/webhooks/:token → 202 立即返回 →
//     异步跑 Loop → 完成通知。
//   - ④ 跨天恢复（M4-05）：插入一条 running 运行 → RecoverUnfinishedRuns 复用真实 Gateway
//     续跑 → 标记 done + 通知，且会话确实落库 user+assistant 消息（证明 Loop 真的执行）。
//
// 全程不调真实 LLM（newM1HTTPMockLLM 脚本化回声，单轮收敛产出结果）。
//
// 关于 TeamOverride：生产 main.go 的 goalTeam 强制开启子代理 + 目标契约（Goal Session 语义），
// 团队/目标契约本身已在 M1-11/M1-12 单元与引擎集成测试覆盖；本测试聚焦 M4 自主化「编排链路」
// （调度器→统一 Gateway→用量计量→会话串行锁→运行记录→通知→恢复），故 TeamOverride 采用
// 单代理模式经同一 Gateway 跑 Loop，与生产路径共享除团队装配外的全部代码。
//
// 本测试位于 cmd/server，依赖 repo.NewDB（glebarez/sqlite 纯 Go 驱动，无 CGO），
// 故在 CGO_ENABLED=0 沙箱下亦可编译运行（与 M0/M1/M3 集成测试同源基础设施）。
func TestM4_Autonomy_E2E(t *testing.T) {
	gin.SetMode(gin.TestMode)

	if err := metrics.Init(metrics.Config{Enabled: true}); err != nil {
		t.Fatalf("metrics.Init: %v", err)
	}
	defer func() { _ = metrics.Init(metrics.Config{Enabled: false}) }()

	mockLLM := newM1HTTPMockLLM()
	defer mockLLM.Close()

	dbPath := filepath.Join(t.TempDir(), "m4-e2e.db")
	enc := sha256.Sum256([]byte("m4-e2e-enc-key"))
	wsRoot := filepath.Join(t.TempDir(), "ws")
	cfg := &config.Config{
		DBPath:        dbPath,
		Port:          "0",
		JWTSecret:     "m4-e2e-secret",
		EncryptionKey: enc[:],
		WorkspaceRoot: wsRoot,
	}
	db, err := repo.NewDB(cfg)
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer func() {
		if sqlDB, e := db.DB.DB(); e == nil {
			_ = sqlDB.Close()
		}
	}()

	disc := provider.NewDiscoverer(cfg.EncryptionKey, time.Minute)
	stateStore := artifact.NewMemoryStore()
	// 自主化 Loop 在测试中以单代理模式运行（聚焦 M4 编排链路，团队/目标契约见 M1）。
	goalTeam := engine.TeamConfig{}
	gw := buildGateway(db, cfg, stateStore, true, nil, nil, nil)
	r := buildRouter(db, cfg, disc, stateStore, true, nil, nil, nil, gw)
	c := &e2eClient{t: t, r: r}

	// 通知出口（M4-07：站内信落库，无出站 webhook 回调）。
	notifier := notify.NewService(db.DB, "", nil)
	api.SetRecoveryNotifier(notifier) // 恢复链路复用同一通知出口

	// 统一 Loop 运行器（cron / webhook 共用，仅标记触发来源 Channel）。
	cronRunner := api.NewAutomationLoopRunner(gw, goalTeam, api.ChannelCron)
	webhookRunner := api.NewAutomationLoopRunner(gw, goalTeam, api.ChannelWebhook)

	// Webhook 路由不归 buildRouter 管理（生产在 main.go 单独注册），测试复用同一 runner 注册。
	webhookLimiter := api.NewWebhookRateLimiter(100, time.Minute)
	r.POST("/api/webhooks/:token", api.NewWebhookHandler(db.DB, webhookRunner, webhookLimiter).WithNotifier(notifier).Handle)

	// 1) 注册 → 建 Provider → 同步 → 启用+默认模型（自主 Loop 解析模型所需）。
	code, reg := c.do("POST", "/api/auth/register", map[string]any{
		"username": "m4dev", "email": "m4dev@example.com", "password": "secret123",
	})
	if code != http.StatusCreated {
		t.Fatalf("注册失败: %d %v", code, reg)
	}
	c.tok = reg["token"].(string)
	uid := uint(reg["user"].(map[string]any)["id"].(float64))

	code, prov := c.do("POST", "/api/providers", map[string]any{
		"name": "m4-openai", "protocol": "openai", "base_url": mockLLM.URL + "/v1", "api_key": "k",
	})
	if code != http.StatusCreated {
		t.Fatalf("建 Provider 失败: %d %v", code, prov)
	}
	pid := uint(prov["id"].(float64))
	code, sync := c.do("POST", fmt.Sprintf("/api/providers/%d/models/sync", pid), nil)
	if code != http.StatusOK {
		t.Fatalf("同步模型失败: %d %v", code, sync)
	}
	models := sync["models"].([]any)
	mid := uint(models[0].(map[string]any)["id"].(float64))
	c.do("PUT", fmt.Sprintf("/api/providers/%d/models/%d", pid, mid),
		map[string]any{"enabled": true, "is_default": true})
	t.Logf("✅ Provider/模型就绪 mid=%d", mid)

	// 2) 定时 Automation（M4-01 创建 + M4-02 触发）。
	code, body := c.do("POST", "/api/automations", map[string]any{
		"name":         "nightly-report",
		"trigger_type": "cron",
		"cron_expr":    "0 2 * * *",
		"goal_prompt":  "生成昨日运营日报",
	})
	if code != http.StatusCreated {
		t.Fatalf("创建 cron Automation 失败: %d %v", code, body)
	}
	cronID := int(body["id"].(float64))

	// 把 next_run 置为过去，使同步扫描立即触发（避免等待真实 cron 周期）。
	a, aerr := repo.GetAutomationByID(db.DB, uid, uint(cronID))
	if aerr != nil {
		t.Fatalf("查 Automation 失败: %v", aerr)
	}
	past := time.Now().Add(-2 * time.Hour)
	a.NextRun = &past
	if uerr := repo.UpdateAutomation(db.DB, a); uerr != nil {
		t.Fatalf("设置 next_run 失败: %v", uerr)
	}

	sched := scheduler.New(db.DB, cronRunner)
	sched.Notifier = notifier
	triggered := sched.TickSync(context.Background())
	if triggered < 1 {
		t.Fatalf("调度器应触发至少 1 个到期自动化，实际 %d", triggered)
	}
	t.Logf("✅ 调度器触发 %d 个自动化", triggered)

	// 运行应标记 done + 站内信「完成」通知 + 预建 session + LastRun 更新。
	waitRunDone(t, db, uid, uint(cronID), 5*time.Second)
	assertRunHasSession(t, db, uid, uint(cronID))
	assertNotification(t, db, uid, model.NotificationTypeSuccess)
	a2, _ := repo.GetAutomationByID(db.DB, uid, uint(cronID))
	if a2.LastRun == nil {
		t.Fatalf("cron Automation LastRun 应已更新")
	}
	t.Logf("✅ 定时自动化：Loop 完成→运行 done→完成通知→LastRun 更新→会话已建")

	// 3) Webhook 入口（M4-03 + M4-07）。
	code, body = c.do("POST", "/api/automations", map[string]any{
		"name":         "pr-watcher",
		"trigger_type": "webhook",
		"goal_prompt":  "处理新开 PR",
	})
	if code != http.StatusCreated {
		t.Fatalf("创建 webhook Automation 失败: %d %v", code, body)
	}
	whID := int(body["id"].(float64))
	var whToken string
	if err := db.DB.Raw("SELECT webhook_token FROM automations WHERE id = ?", whID).Scan(&whToken).Error; err != nil {
		t.Fatalf("读取 webhook_token: %v", err)
	}
	if whToken == "" {
		t.Fatalf("webhook Automation 应自动生成令牌")
	}

	code, wbody := c.do("POST", "/api/webhooks/"+whToken, nil)
	if code != http.StatusAccepted {
		t.Fatalf("Webhook 应返回 202，实际 %d, body=%v", code, wbody)
	}
	if wbody["automation_id"].(float64) != float64(whID) {
		t.Fatalf("Webhook 响应 automation_id 不符: %v", wbody)
	}
	t.Logf("✅ Webhook 入口：202 接受 + 异步 Loop 已派发")

	// 异步 Loop 完成后应标记 done + 完成通知（轮询等待）。
	waitRunDone(t, db, uid, uint(whID), 10*time.Second)
	assertNotification(t, db, uid, model.NotificationTypeSuccess)
	t.Logf("✅ Webhook 异步 Loop：完成→运行 done→完成通知")

	// 4) 跨天恢复（M4-05）：插入一条 running 运行，经真实 Gateway 续跑并标记 done。
	run := &model.AutomationRun{
		AutomationID: uint(cronID), UserID: uid, SessionKey: "sess-recover-m4",
		Channel: model.RunChannelCron, Status: model.RunStatusRunning,
	}
	if err := repo.CreateAutomationRun(db.DB, run); err != nil {
		t.Fatalf("插入 running 运行失败: %v", err)
	}
	// 恢复复用真实 Gateway 作为 RecoveryRunner，证明 Loop 确实被重新执行。
	recovered := api.RecoverUnfinishedRuns(context.Background(), db.DB, gw, goalTeam, 3, nil)
	if recovered != 1 {
		t.Fatalf("应恢复 1 条运行，实际 %d", recovered)
	}
	var finalRun model.AutomationRun
	if err := db.DB.First(&finalRun, run.ID).Error; err != nil {
		t.Fatalf("reload run: %v", err)
	}
	if finalRun.Status != model.RunStatusDone {
		t.Fatalf("恢复后运行应标记 done，实际 %q", finalRun.Status)
	}
	// 恢复续跑经 Gateway 实际执行：会话应有 user+assistant 消息。
	recSess, rerr := repo.GetSessionByKey(db.DB, uid, "sess-recover-m4")
	if rerr != nil {
		t.Fatalf("查恢复会话失败: %v", rerr)
	}
	msgs, merr := repo.ListSessionMessages(db.DB, recSess.ID)
	if merr != nil {
		t.Fatalf("查恢复会话消息失败: %v", merr)
	}
	if len(msgs) < 2 {
		t.Fatalf("恢复续跑应落库 user+assistant 消息，实际 %d 条", len(msgs))
	}
	assertNotification(t, db, uid, model.NotificationTypeSuccess)
	t.Logf("✅ 跨天恢复：running 运行经 Gateway 续跑→标记 done→会话落库 %d 条消息", len(msgs))

	t.Log("🎉 M4 自主化全链路 E2E 通过：定时触发→Loop完成→完成通知→Webhook异步→跨天恢复续跑")
}

// waitRunDone 轮询直到指定 automation 出现一条 status=done 的运行（或超时）。
func waitRunDone(t *testing.T, db *repo.DB, uid, automationID uint, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		runs, err := repo.ListAutomationRuns(db.DB, uid)
		if err == nil {
			for _, r := range runs {
				if r.AutomationID == automationID && r.Status == model.RunStatusDone {
					return
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待 automation %d 运行 done 超时（%s）", automationID, timeout)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// assertRunHasSession 断言指定 automation 的运行记录携带非空 session_key（调度/Webhook 预建会话）。
func assertRunHasSession(t *testing.T, db *repo.DB, uid, automationID uint) {
	t.Helper()
	runs, err := repo.ListAutomationRuns(db.DB, uid)
	if err != nil {
		t.Fatalf("查询运行失败: %v", err)
	}
	for _, r := range runs {
		if r.AutomationID == automationID && r.SessionKey != "" {
			return
		}
	}
	t.Fatalf("automation %d 的运行缺少 session_key", automationID)
}

// assertNotification 断言指定用户存在至少一条指定类型的通知（站内信落库验证）。
func assertNotification(t *testing.T, db *repo.DB, uid uint, ntype string) {
	t.Helper()
	notes, _, err := repo.ListNotifications(db.DB, uid, 100, 0)
	if err != nil {
		t.Fatalf("查询通知失败: %v", err)
	}
	for _, n := range notes {
		if n.UserID == uid && n.Type == ntype {
			return
		}
	}
	t.Fatalf("未找到 user=%d type=%s 的通知（共 %d 条）", uid, ntype, len(notes))
}
