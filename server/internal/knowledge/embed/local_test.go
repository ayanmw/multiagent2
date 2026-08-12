package embed

import (
	"context"
	"math"
	"testing"
)

func TestLocalEmbedder_Dimensions(t *testing.T) {
	e := NewLocalEmbedder(0) // 0 → 默认维度
	if e.GetDimensions() != DefaultLocalDim {
		t.Fatalf("expected dim %d, got %d", DefaultLocalDim, e.GetDimensions())
	}
	custom := NewLocalEmbedder(128)
	if custom.GetDimensions() != 128 {
		t.Fatalf("expected custom dim 128, got %d", custom.GetDimensions())
	}
}

func TestLocalEmbedder_Deterministic(t *testing.T) {
	e := NewLocalEmbedder(0)
	a, err := e.GetEmbedding(context.Background(), "Go goroutine channel")
	if err != nil {
		t.Fatalf("GetEmbedding: %v", err)
	}
	b, err := e.GetEmbedding(context.Background(), "Go goroutine channel")
	if err != nil {
		t.Fatalf("GetEmbedding: %v", err)
	}
	if len(a) != len(b) {
		t.Fatalf("vector length mismatch: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("non-deterministic embedding at index %d", i)
		}
	}
}

func TestLocalEmbedder_Similarity(t *testing.T) {
	e := NewLocalEmbedder(0)
	// 相似文本（同主题）应有较高相似度；无关文本相似度应明显更低。
	sim := cosine(e.embed("Go 使用 goroutine 实现并发编程"), e.embed("Goroutine 与 channel 是 Go 的并发原语"))
	diff := cosine(e.embed("Go 使用 goroutine 实现并发编程"), e.embed("今天天气晴朗适合户外运动"))

	if sim <= diff {
		t.Fatalf("expected similar texts to score higher: sim=%.3f diff=%.3f", sim, diff)
	}
	if sim <= 0 {
		t.Fatalf("expected positive similarity for related texts, got %.3f", sim)
	}
}

// cosine 计算两个同维向量的余弦相似度（用于单测断言，非生产代码）。
func cosine(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
