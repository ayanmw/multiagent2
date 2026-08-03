package skillrepo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSkill 直接落一盘 SKILL.md（绕开 Manager，用于构造共享技能与前置状态）。
func writeSkill(t *testing.T, dir, name, body string) {
	t.Helper()
	d := filepath.Join(dir, name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newTestManager(t *testing.T) (string, string, *Manager) {
	t.Helper()
	shared := t.TempDir()
	data := t.TempDir()
	return shared, data, NewManager(shared, data)
}

func TestManager_CreateListGet(t *testing.T) {
	shared, _, mgr := newTestManager(t)
	// 共享技能（直接落盘到 shared 根）。
	writeSkill(t, shared, "shared-one", "---\nname: shared-one\ndescription: 共享技能一\n---\n共享正文")
	// 用户私有技能（经 API 创建）。
	const uid = "42"
	if err := mgr.Create(uid, "mine", "---\nname: mine\ndescription: 我的技能\n---\n私有正文"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	list, err := mgr.List(uid)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("期望 2 个技能，实际 %d: %+v", len(list), list)
	}
	// 私有优先排在共享前。
	if list[0].Scope != ScopePrivate || list[0].Name != "mine" {
		t.Fatalf("私有技能应排前: %+v", list)
	}
	if list[1].Scope != ScopeShared || list[1].ReadOnly != true {
		t.Fatalf("共享技能应为只读: %+v", list)
	}

	// Get 私有优先：私有与共享同名时取私有（这里名不同，分别验证）。
	d, err := mgr.Get(uid, "mine")
	if err != nil || d.Scope != ScopePrivate || d.Body != "私有正文" {
		t.Fatalf("Get mine: %+v err=%v", d, err)
	}
	s, err := mgr.Get(uid, "shared-one")
	if err != nil || s.Scope != ScopeShared || s.Body != "共享正文" {
		t.Fatalf("Get shared-one: %+v err=%v", s, err)
	}
}

func TestManager_OwnerIsolation(t *testing.T) {
	shared, data, mgr := newTestManager(t)
	_ = shared
	// 仅 uid=7 有私有技能。
	if err := mgr.Create("7", "secret", "仅 7 可见"); err != nil {
		t.Fatal(err)
	}
	// uid=9 列技能不应看到 7 的私有技能（roots 不含 data/9 之外的私有目录）。
	list9, err := mgr.List("9")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range list9 {
		if s.Name == "secret" {
			t.Fatalf("uid=9 不应看到 uid=7 的私有技能: %+v", list9)
		}
	}
	if _, err := mgr.Get("9", "secret"); err == nil {
		t.Fatalf("uid=9 不应读到 uid=7 的私有技能")
	}
	// uid=7 自己能读到。
	if d, err := mgr.Get("7", "secret"); err != nil || d.Body != "仅 7 可见" {
		t.Fatalf("uid=7 应读到 secret: %+v err=%v", d, err)
	}
	_ = data
}

func TestManager_CreateUpdateDelete(t *testing.T) {
	_, data, mgr := newTestManager(t)
	const uid = "1"
	if err := mgr.Create(uid, "foo", "v1"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Update(uid, "foo", "v2"); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if d, err := mgr.Get(uid, "foo"); err != nil || d.Body != "v2" {
		t.Fatalf("Update 后应为 v2: %+v err=%v", d, err)
	}
	// 更新不存在的技能 → 404。
	if err := mgr.Update(uid, "nope", "x"); err == nil {
		t.Fatalf("更新不存在技能应报错")
	}
	if err := mgr.Delete(uid, "foo"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := mgr.Get(uid, "foo"); err == nil {
		t.Fatalf("删除后应读不到")
	}
	// 删除不存在的技能 → 404。
	if err := mgr.Delete(uid, "foo"); err == nil {
		t.Fatalf("删除不存在技能应报错")
	}
	_ = data
}

func TestManager_InvalidName(t *testing.T) {
	_, _, mgr := newTestManager(t)
	cases := []string{"", "..", "a/b", "a\\b", "../../etc", "有中文"}
	for _, n := range cases {
		if ValidSkillName(n) {
			t.Fatalf("应判定非法技能名: %q", n)
		}
		if err := mgr.Create("1", n, "x"); err == nil {
			t.Fatalf("非法名 Create 应报错: %q", n)
		}
	}
	if !ValidSkillName("good-name_1") {
		t.Fatalf("合法名被拒: good-name_1")
	}
}

func TestWarmStartBlockRoots_InjectsAndCaps(t *testing.T) {
	shared := t.TempDir()
	data := t.TempDir()
	writeSkill(t, shared, "git-flow", "---\nname: git-flow\ndescription: git 工作流技能\n---\n这是 git 技能正文，较长内容用于测试长度上限控制。")
	writeSkill(t, shared, "docker-tips", "---\nname: docker-tips\ndescription: docker 技巧\n---\ndocker 正文")
	private := filepath.Join(data, "5")
	writeSkill(t, private, "my-skill", "---\nname: my-skill\ndescription: 我的私有技能\n---\n私有技能正文")

	roots := []string{shared, private}
	// 无关键词：注入全部（受 maxChars 限制）。
	block, err := WarmStartBlockRoots(roots, nil, 6000)
	if err != nil {
		t.Fatalf("WarmStartBlockRoots: %v", err)
	}
	if !strings.Contains(block, "git-flow") || !strings.Contains(block, "my-skill") {
		t.Fatalf("应包含所有技能名: %s", block)
	}
	if !strings.Contains(block, "这是 git 技能正文") {
		t.Fatalf("应含命中技能正文: %s", block)
	}

	// 关键词过滤：仅含 docker。
	block2, err := WarmStartBlockRoots(roots, []string{"docker"}, 6000)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(block2, "git 技能正文") {
		t.Fatalf("关键词 docker 不应展开 git 技能正文: %s", block2)
	}
	if !strings.Contains(block2, "docker 正文") {
		t.Fatalf("应展开 docker 技能正文: %s", block2)
	}

	// 长度上限（极小的上限）：标题/索引仍给，但正文因超长不展开。
	block3, err := WarmStartBlockRoots(roots, nil, 20)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(block3, "这是 git 技能正文") {
		t.Fatalf("极小 maxChars 不应展开技能正文: %s", block3)
	}
	if !strings.Contains(block3, "可用技能") {
		t.Fatalf("极小上限仍应给出索引标题: %s", block3)
	}
}

func TestWarmStartBlockRoots_Empty(t *testing.T) {
	empty := t.TempDir()
	block, err := WarmStartBlockRoots([]string{empty}, nil, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if block != "" {
		t.Fatalf("无技能时应返回空串，实际: %q", block)
	}
}

func TestManager_WarmStartBlock_UsesPrivateRoot(t *testing.T) {
	shared, data, mgr := newTestManager(t)
	writeSkill(t, shared, "pub", "---\nname: pub\ndescription: 公开\n---\n公开正文")
	const uid = "3"
	writeSkill(t, filepath.Join(data, uid), "priv", "---\nname: priv\ndescription: 私有\n---\n私有正文")

	block, err := mgr.WarmStartBlock(uid, nil, 6000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(block, "pub") || !strings.Contains(block, "priv") {
		t.Fatalf("应同时含共享与私有技能: %s", block)
	}
	// 另一个用户看不到该用户私有技能（roots 仅含 data/9）。
	block9, err := mgr.WarmStartBlock("9", nil, 6000)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(block9, "私有正文") {
		t.Fatalf("其他用户 warm-start 不应含本用户私有技能正文: %s", block9)
	}
}
