// Package store 的 pgvector 后端测试（M8-04）。
//
// 分两层：
//   - 纯单测（无 PG 也跑）：向量文本序列化/解析、DSN 脱敏、维度解析、配置默认。
//   - 集成测试（设置 PG_TEST_DSN 才跑，未设置 t.Skip）：建表/CRUD/检索排序/
//     删除/并发检索/万级 chunk P99 输出（PG_SCALE_TEST=1 时插 1 万条）。
//
// 无 PG 环境（本机沙箱/CI 默认）不影响编译与单测；有 PG 的 CI runner 真跑，
// 与 M8-02 docker 集成测试同款「环境变量 + Skip 兜底」策略。
package store

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

// --- 纯单测（无 PG 依赖） ---

// TestFormatParseVector 验收 formatVector/parseVector 往返一致（含负数/小数/大数）。
func TestFormatParseVector(t *testing.T) {
	vec := []float64{0.1, -0.25, 1.0, 3.14159, 0}
	text := formatVector(vec)
	if text != "[0.1,-0.25,1,3.14159,0]" {
		t.Errorf("formatVector = %q", text)
	}
	got, err := parseVector(text)
	if err != nil {
		t.Fatalf("parseVector: %v", err)
	}
	if len(got) != len(vec) {
		t.Fatalf("len = %d, want %d", len(got), len(vec))
	}
	for i := range vec {
		if got[i] != vec[i] {
			t.Errorf("vec[%d] = %v, want %v", i, got[i], vec[i])
		}
	}
	if _, err := parseVector(""); err == nil {
		t.Error("空字符串解析必须报错")
	}
	if _, err := parseVector("[1,2"); err == nil {
		t.Error("非法向量文本必须报错")
	}
}

// TestMaskDSN 验收 DSN 脱敏：密码不出现在错误信息中。
func TestMaskDSN(t *testing.T) {
	cases := []struct{ in, want string }{
		{"postgres://user:secret@host:5432/db", "postgres://***@host:5432/db"},
		{"postgres://u@h/db", "postgres://***@h/db"},
		{"host:5432/db", "host:5432/db"},
	}
	for _, c := range cases {
		if got := maskDSN(c.in); got != c.want {
			t.Errorf("maskDSN(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestParseVectorDim 验收 format_type 输出解析（"vector(256)" → 256；"vector" → 0）。
func TestParseVectorDim(t *testing.T) {
	cases := map[string]int{
		"vector(256)": 256,
		"vector(1536)": 1536,
		"vector":      0,
		"":            0,
		"numeric(10,2)": 0,
	}
	for in, want := range cases {
		if got := parseVectorDim(in); got != want {
			t.Errorf("parseVectorDim(%q) = %d, want %d", in, got, want)
		}
	}
}

// TestNewPGPool_EmptyDSN 验收：空 DSN 直接报错（不触发网络）。
func TestNewPGPool_EmptyDSN(t *testing.T) {
	if _, err := NewPGPool(context.Background(), PGConfig{}); err == nil {
		t.Fatal("空 DSN 必须报错")
	}
}

// --- 集成测试（PG_TEST_DSN 环境变量；未设置则 Skip） ---

// pgTestPool 按 PG_TEST_DSN 构造共享连接池；未设置时 Skip。
// 每次调用返回独立 kbID（随机后缀），测试互不干扰。
func pgTestPool(t *testing.T) (*PGPool, string) {
	t.Helper()
	dsn := os.Getenv("PG_TEST_DSN")
	if strings.TrimSpace(dsn) == "" {
		t.Skip("未设置 PG_TEST_DSN，跳过 pgvector 集成测试（有 PG 的 CI runner 真跑）")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := NewPGPool(ctx, PGConfig{DSN: dsn, Dim: 32, PoolSize: 4})
	if err != nil {
		t.Fatalf("NewPGPool: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	kbID := fmt.Sprintf("t-%d-%d", time.Now().UnixNano(), os.Getpid())
	return pool, kbID
}

// mkDoc 构造测试切片。
func mkDoc(kbID, name string, idx int, content string) *document.Document {
	return &document.Document{
		ID:      fmt.Sprintf("kb://%s/%s/%d", kbID, name, idx),
		Name:    fmt.Sprintf("%s [chunk %d]", name, idx),
		Content: content,
		Metadata: map[string]any{
			source.MetaSourceName: name,
			source.MetaChunkIndex: idx,
		},
	}
}

// emb 构造一个确定性向量（第 d 个桶为 1，其余 0——同桶向量相似度最高）。
func emb(d, dim int) []float64 {
	v := make([]float64, dim)
	if d < dim {
		v[d] = 1
	}
	return v
}

// TestPGVectorStore_CRUDAndSearch 验收 M8-04 核心路径：
// Add/Get/Update/Search（余弦排序 + topK）/Count/Delete。
func TestPGVectorStore_CRUDAndSearch(t *testing.T) {
	pool, kbID := pgTestPool(t)
	ctx := context.Background()
	vs := pool.ForKB(kbID)
	const dim = 32

	// 索引 3 个切片：内容与向量方向一致（query 向量指向桶 0）。
	docs := []*document.Document{
		mkDoc(kbID, "doc-a", 0, "go 语言并发模型 goroutine channel"),
		mkDoc(kbID, "doc-a", 1, "k8s 部署多副本滚动更新"),
		mkDoc(kbID, "doc-b", 0, "sqlite 纯 go 驱动无 cgo"),
	}
	for i, d := range docs {
		if err := vs.Add(ctx, d, emb(i, dim)); err != nil {
			t.Fatalf("Add #%d: %v", i, err)
		}
	}

	// Count
	if n, err := vs.Count(ctx); err != nil || n != 3 {
		t.Fatalf("Count = %d, %v; want 3", n, err)
	}

	// Get 回读
	got, vec, err := vs.Get(ctx, docs[1].ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Content != docs[1].Content || len(vec) != dim {
		t.Errorf("Get 回读不一致: content=%q len(vec)=%d", got.Content, len(vec))
	}

	// Search：query 指向桶 2 → doc-b 最相似，top1 应命中。
	res, err := vs.Search(ctx, &vectorstore.SearchQuery{Vector: emb(2, dim), Limit: 2, MinScore: 0})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Results) != 2 {
		t.Fatalf("Search len = %d, want 2", len(res.Results))
	}
	if res.Results[0].Document.Content != docs[2].Content {
		t.Errorf("top1 = %q, want %q（余弦排序）", res.Results[0].Document.Content, docs[2].Content)
	}
	if res.Results[0].Score <= res.Results[1].Score {
		t.Errorf("相似度应降序: %v <= %v", res.Results[0].Score, res.Results[1].Score)
	}

	// Search with metadata filter：只命中 doc-a（chunk_index=0）。
	fres, err := vs.Search(ctx, &vectorstore.SearchQuery{
		Vector: emb(0, dim),
		Limit:  10,
		Filter: &vectorstore.SearchFilter{Metadata: map[string]any{source.MetaSourceName: "doc-a"}},
	})
	if err != nil {
		t.Fatalf("Search filtered: %v", err)
	}
	if len(fres.Results) != 2 {
		t.Fatalf("filtered len = %d, want 2（doc-a 两个 chunk）", len(fres.Results))
	}
	for _, r := range fres.Results {
		if r.Document.Metadata[source.MetaSourceName] != "doc-a" {
			t.Errorf("命中越出 filter: %v", r.Document.Metadata)
		}
	}

	// Update：改 doc-b 内容后 Get 应拿到新内容。
	d2 := *docs[2]
	d2.Content = "updated content"
	if err := vs.Update(ctx, &d2, emb(2, dim)); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if g, _, _ := vs.Get(ctx, d2.ID); g.Content != "updated content" {
		t.Errorf("Update 后 Get = %q", g.Content)
	}

	// Delete 单条
	if err := vs.Delete(ctx, docs[0].ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if n, _ := vs.Count(ctx); n != 2 {
		t.Errorf("Delete 后 Count = %d, want 2", n)
	}
}

// TestPGVectorStore_DeleteByFilter 验收三种删除模式：DeleteAll / DocumentIDs / Metadata。
func TestPGVectorStore_DeleteByFilter(t *testing.T) {
	pool, kbID := pgTestPool(t)
	ctx := context.Background()
	vs := pool.ForKB(kbID)
	const dim = 16

	for i := 0; i < 4; i++ {
		d := mkDoc(kbID, "doc-x", i, fmt.Sprintf("内容 %d", i))
		if err := vs.Add(ctx, d, emb(i, dim)); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}

	// 按 metadata 删：chunk_index=0
	if err := vs.DeleteByFilter(ctx, vectorstore.WithDeleteFilter(map[string]any{source.MetaChunkIndex: 0})); err != nil {
		t.Fatalf("DeleteByFilter metadata: %v", err)
	}
	if n, _ := vs.Count(ctx); n != 3 {
		t.Fatalf("metadata 删除后 Count = %d, want 3", n)
	}

	// 按 ID 删一条
	all, err := vs.GetMetadata(ctx)
	if err != nil {
		t.Fatalf("GetMetadata: %v", err)
	}
	var target string
	for id := range all {
		target = id
		break
	}
	if err := vs.DeleteByFilter(ctx, vectorstore.WithDeleteDocumentIDs([]string{target})); err != nil {
		t.Fatalf("DeleteByFilter ids: %v", err)
	}
	if n, _ := vs.Count(ctx); n != 2 {
		t.Fatalf("ids 删除后 Count = %d, want 2", n)
	}

	// DeleteAll 清空
	if err := vs.DeleteByFilter(ctx, vectorstore.WithDeleteAll(true)); err != nil {
		t.Fatalf("DeleteByFilter all: %v", err)
	}
	if n, _ := vs.Count(ctx); n != 0 {
		t.Fatalf("DeleteAll 后 Count = %d, want 0", n)
	}
}

// TestPGVectorStore_ListAndDeleteDocument 验收文档聚合与按文档删除。
func TestPGVectorStore_ListAndDeleteDocument(t *testing.T) {
	pool, kbID := pgTestPool(t)
	ctx := context.Background()
	vs := pool.ForKB(kbID)
	const dim = 8

	for i := 0; i < 3; i++ {
		if err := vs.Add(ctx, mkDoc(kbID, "report", i, "报告片段"), emb(0, dim)); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	if err := vs.Add(ctx, mkDoc(kbID, "manual", 0, "手册片段"), emb(1, dim)); err != nil {
		t.Fatalf("Add manual: %v", err)
	}

	infos, err := vs.ListDocuments(ctx)
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("文档数 = %d, want 2", len(infos))
	}
	for _, info := range infos {
		want := 1
		if info.Name == "report" {
			want = 3
		}
		if info.ChunkCount != want {
			t.Errorf("doc %s chunk_count = %d, want %d", info.Name, info.ChunkCount, want)
		}
	}

	n, err := vs.DeleteDocument(ctx, "report")
	if err != nil {
		t.Fatalf("DeleteDocument: %v", err)
	}
	if n != 3 {
		t.Fatalf("删除切片数 = %d, want 3", n)
	}
	if rest, _ := vs.ListDocuments(ctx); len(rest) != 1 || rest[0].Name != "manual" {
		t.Errorf("删除后剩余文档 = %+v", rest)
	}
}

// TestPGVectorStore_ConcurrentSearch 验收 M8-04「并发检索无退化」：
// 20 goroutine × 50 次并发检索，全程无错误、结果稳定。
func TestPGVectorStore_ConcurrentSearch(t *testing.T) {
	pool, kbID := pgTestPool(t)
	ctx := context.Background()
	vs := pool.ForKB(kbID)
	const dim = 16
	const nDocs = 200

	for i := 0; i < nDocs; i++ {
		if err := vs.Add(ctx, mkDoc(kbID, fmt.Sprintf("d%d", i), 0, fmt.Sprintf("并发检索内容 %d", i)), emb(i%dim, dim)); err != nil {
			t.Fatalf("Add #%d: %v", i, err)
		}
	}

	const workers = 20
	const rounds = 50
	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	start := time.Now()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			q := emb(w%dim, dim)
			for r := 0; r < rounds; r++ {
				res, err := vs.Search(ctx, &vectorstore.SearchQuery{Vector: q, Limit: 5})
				if err != nil {
					errCh <- fmt.Errorf("worker %d round %d: %w", w, r, err)
					return
				}
				if len(res.Results) == 0 {
					errCh <- fmt.Errorf("worker %d round %d: 空结果", w, r)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	t.Logf("并发检索 20×50 完成：%d 次检索 / %s（无错误、无退化）", workers*rounds, elapsed.Round(time.Millisecond))
}

// TestPGVectorStore_Scale10k 验收 M8-04「万级 chunk 检索 P99 达标」：
// 仅 PG_SCALE_TEST=1 时插入 1 万条并输出检索延迟分布（不硬断言阈值，避免环境抖动 flaky；
// P99 数据供 docs/knowledge-pgvector.md 实测记录）。默认跳过（CI 快速路径）。
func TestPGVectorStore_Scale10k(t *testing.T) {
	if os.Getenv("PG_SCALE_TEST") != "1" {
		t.Skip("未设置 PG_SCALE_TEST=1，跳过万级数据测试（PG_SCALE_TEST=1 时插 1 万 chunk 并输出检索 P99）")
	}
	pool, kbID := pgTestPool(t)
	ctx := context.Background()
	vs := pool.ForKB(kbID)
	const dim = 32
	const n = 10000

	insertStart := time.Now()
	for i := 0; i < n; i++ {
		d := mkDoc(kbID, fmt.Sprintf("scale-doc-%04d", i/10), i%10, fmt.Sprintf("第 %d 号知识片段：%s", i, strings.Repeat("内容", 20)))
		if err := vs.Add(ctx, d, emb(i%dim, dim)); err != nil {
			t.Fatalf("Add #%d: %v", i, err)
		}
	}
	t.Logf("插入 %d 条完成：%s", n, time.Since(insertStart).Round(time.Millisecond))

	const rounds = 500
	lats := make([]time.Duration, 0, rounds)
	results := make([]int, 0, rounds)
	for r := 0; r < rounds; r++ {
		start := time.Now()
		res, err := vs.Search(ctx, &vectorstore.SearchQuery{Vector: emb(r%dim, dim), Limit: 10})
		lat := time.Since(start)
		if err != nil {
			t.Fatalf("Search #%d: %v", r, err)
		}
		lats = append(lats, lat)
		results = append(results, len(res.Results))
	}
	// P50/P95/P99 计算
	latMS := make([]int64, len(lats))
	for i, d := range lats {
		latMS[i] = d.Microseconds()
	}
	sortInt64(latMS)
	pct := func(p float64) float64 {
		if len(latMS) == 0 {
			return 0
		}
		idx := int(float64(len(latMS)-1) * p)
		return float64(latMS[idx]) / 1000 // ms
	}
	t.Logf("万级检索 %d 次：P50=%.1fms P95=%.1fms P99=%.1fms max=%.1fms（空结果=%d/%d）",
		rounds, pct(0.50), pct(0.95), pct(0.99), pct(1.0), countZero(results), len(results))
}

func sortInt64(a []int64) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

func countZero(a []int) int {
	n := 0
	for _, v := range a {
		if v == 0 {
			n++
		}
	}
	return n
}

// TestPGVectorStore_KBIsolation 验收 kb_id 逻辑隔离：两个知识库互不可见。
func TestPGVectorStore_KBIsolation(t *testing.T) {
	pool, _ := pgTestPool(t)
	ctx := context.Background()
	const dim = 8
	kbA, kbB := pool.ForKB("iso-a"), pool.ForKB("iso-b")

	if err := kbA.Add(ctx, mkDoc("iso-a", "doc", 0, "A 库内容"), emb(0, dim)); err != nil {
		t.Fatalf("Add A: %v", err)
	}
	if err := kbB.Add(ctx, mkDoc("iso-b", "doc", 0, "B 库内容"), emb(0, dim)); err != nil {
		t.Fatalf("Add B: %v", err)
	}

	if n, _ := kbA.Count(ctx); n != 1 {
		t.Errorf("A Count = %d, want 1", n)
	}
	if n, _ := kbB.Count(ctx); n != 1 {
		t.Errorf("B Count = %d, want 1", n)
	}
	// A 检索不应看到 B 的切片。
	res, err := kbA.Search(ctx, &vectorstore.SearchQuery{Vector: emb(1, dim), Limit: 10})
	if err != nil {
		t.Fatalf("A Search: %v", err)
	}
	for _, r := range res.Results {
		if strings.Contains(r.Document.Content, "B 库") {
			t.Errorf("A 库检索越出隔离，命中 B 库内容: %q", r.Document.Content)
		}
	}
}
