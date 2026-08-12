package evolution

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// dbCounter 为每个测试生成唯一的共享内存库名，避免多个测试共用同一 SQLite
// 内存实例导致会话数据互相串扰（cache=shared 会跨连接保留数据）。
var dbCounter uint64

// mockExtractor 是测试用提取器：返回预设候选 / 错误 / 无技能。
type mockExtractor struct {
	cand    *RawCandidate
	err     error
	noSkill bool
	calls   int
}

func (m *mockExtractor) Extract(_ context.Context, _ uint, _ string) (*RawCandidate, error) {
	m.calls++
	if m.noSkill {
		return nil, ErrNoSkill
	}
	if m.err != nil {
		return nil, m.err
	}
	return m.cand, nil
}

// newTestDB 用纯 Go 的 glebarez sqlite 开内存库并建所需表（无 CGO，沙箱可跑）。
// 每个测试使用独立 DSN，避免会话数据跨测试串扰。
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:evo_test_%d?mode=memory&cache=shared", atomic.AddUint64(&dbCounter, 1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(&model.Session{}, &model.Message{}, &model.SkillCandidate{}); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	return db
}

func makeSessionWithMessages(t *testing.T, db *gorm.DB, uid uint, key string, n int) model.Session {
	t.Helper()
	sess := model.Session{UserID: uid, SessionKey: key, Title: "t"}
	if err := db.Create(&sess).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}
	for i := 0; i < n; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		if err := db.Create(&model.Message{SessionID: sess.ID, Role: role, Content: "msg"}).Error; err != nil {
			t.Fatalf("create message: %v", err)
		}
	}
	return sess
}

func TestQualityGate(t *testing.T) {
	goodBody := `# 适用场景
当 CI 流水线偶发性失败、且本地无法稳定复现时，需要一套标准化的排查动作，快速区分是环境抖动还是真实代码缺陷。

## 操作步骤
1. 先在 CI 上手动重跑该任务，确认失败是否可复现（偶发 vs 必现）。
2. 拉取完整失败日志，重点看超时、网络抖动、资源不足（OOM）三类信号。
3. 对比上一次成功的构建，diff 改动范围，缩小嫌疑代码。
4. 若是环境导致（如并发抢占），在关键步骤增加退避重试。
5. 复跑至少三次确认稳定后再合并修复。

## 注意事项
- 不要盲目加重试掩盖真实缺陷。
- 记录复现概率，超过阈值再立项根治。`

	cases := []struct {
		name        string
		raw         RawCandidate
		wantPassed  bool
	}{
		{"合格候选", RawCandidate{Name: "fix-ci", Description: "修复 CI 流水线失败的标准流程", Body: goodBody}, true},
		{"名称非法", RawCandidate{Name: "bad name!", Description: "描述足够长描述足够长描述足够长", Body: goodBody}, false},
		{"描述过短", RawCandidate{Name: "goodname", Description: "短", Body: goodBody}, false},
		{"正文过短", RawCandidate{Name: "goodname", Description: "描述足够长描述足够长描述足够长", Body: "太短"}, false},
		{"缺乏结构", RawCandidate{Name: "goodname", Description: "描述足够长描述足够长描述足够长", Body: "这是一段纯散文没有任何标题编号或步骤的内容用来测试结构判定是否生效"}, false},
		{"占位短语", RawCandidate{Name: "goodname", Description: "描述足够长描述足够长描述足够长", Body: "# 标题\nTODO 待补充更多内容后再提交审批"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := QualityGate(c.raw.Name, c.raw.Description, c.raw.Body)
			if r.Passed != c.wantPassed {
				t.Errorf("QualityGate passed=%v want %v (notes=%v)", r.Passed, c.wantPassed, r.Notes)
			}
		})
	}
}

func TestContentHashStableAndDistinct(t *testing.T) {
	h1 := ContentHash("name", "body")
	h2 := ContentHash("name", "body")
	h3 := ContentHash("name", "different body")
	if h1 != h2 {
		t.Errorf("same input should produce same hash: %q vs %q", h1, h2)
	}
	if h1 == h3 {
		t.Errorf("different input should produce different hash")
	}
	if len(h1) != 64 {
		t.Errorf("sha256 hex should be 64 chars, got %d", len(h1))
	}
}

func TestServiceScanCreatesCandidate(t *testing.T) {
	db := newTestDB(t)
	uid := uint(42)
	makeSessionWithMessages(t, db, uid, "sess-a", 6)
	makeSessionWithMessages(t, db, uid, "sess-b", 2) // 过短，应跳过

	cand := &RawCandidate{
		Name:        "fix-flaky-test",
		Description: "修复偶发失败测试的标准排查步骤",
		Body: `# 适用场景
当 CI 流水线偶发性失败、且本地无法稳定复现时，需要一套标准化的排查动作，快速区分是环境抖动还是真实代码缺陷。

## 操作步骤
1. 先在 CI 上手动重跑该任务，确认失败是否可复现（偶发 vs 必现）。
2. 拉取完整失败日志，重点看超时、网络抖动、资源不足（OOM）三类信号。
3. 对比上一次成功的构建，diff 改动范围，缩小嫌疑代码。
4. 若是环境导致（如并发抢占），在关键步骤增加退避重试。
5. 复跑至少三次确认稳定后再合并修复。

## 注意事项
- 不要盲目加重试掩盖真实缺陷。
- 记录复现概率，超过阈值再立项根治。`,
	}
	svc := NewService(db, &mockExtractor{cand: cand})

	rep, err := svc.Scan(context.Background())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if rep.Created != 1 {
		t.Errorf("created=%d want 1 (rep=%+v)", rep.Created, rep)
	}
	if rep.Skipped != 1 {
		t.Errorf("skipped=%d want 1 (rep=%+v)", rep.Skipped, rep)
	}

	// 去重：再次扫描不应产生新候选（会话已提取 + 内容哈希已存在）。
	rep2, err := svc.Scan(context.Background())
	if err != nil {
		t.Fatalf("scan2: %v", err)
	}
	if rep2.Created != 0 {
		t.Errorf("second scan created=%d want 0 (dedup)", rep2.Created)
	}

	// 落库校验：应有一条 pending 候选。
	list, err := repo.ListSkillCandidates(db, uid, string(model.SkillCandidatePending), 10, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("pending candidates=%d want 1", len(list))
	}
	if list[0].SourceSessionKey != "sess-a" {
		t.Errorf("source session key=%q want sess-a", list[0].SourceSessionKey)
	}
}

func TestServiceScanNoSkillSkipped(t *testing.T) {
	db := newTestDB(t)
	uid := uint(7)
	makeSessionWithMessages(t, db, uid, "sess-no", 6)
	svc := NewService(db, &mockExtractor{noSkill: true})

	rep, err := svc.Scan(context.Background())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if rep.Created != 0 {
		t.Errorf("created=%d want 0", rep.Created)
	}
	if rep.Skipped != 1 {
		t.Errorf("skipped=%d want 1", rep.Skipped)
	}
}

func TestServiceScanLowQualityRejected(t *testing.T) {
	db := newTestDB(t)
	uid := uint(9)
	makeSessionWithMessages(t, db, uid, "sess-low", 6)
	// 正文过短 → 未过质量门控 → 不落库。
	svc := NewService(db, &mockExtractor{cand: &RawCandidate{Name: "x", Description: "描述足够长描述足够长描述足够长", Body: "短"}})

	rep, err := svc.Scan(context.Background())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if rep.Created != 0 {
		t.Errorf("created=%d want 0 (low quality intercepted)", rep.Created)
	}
}

func TestServiceScanExtractorErrorCounts(t *testing.T) {
	db := newTestDB(t)
	uid := uint(11)
	makeSessionWithMessages(t, db, uid, "sess-err", 6)
	svc := NewService(db, &mockExtractor{err: errors.New("boom")})

	rep, err := svc.Scan(context.Background())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if rep.Errors != 1 {
		t.Errorf("errors=%d want 1", rep.Errors)
	}
	if rep.Created != 0 {
		t.Errorf("created=%d want 0", rep.Created)
	}
}
