// Package embed 提供本地离线嵌入器（M5-02 Knowledge RAG 基线）。
//
// 设计取舍：在不依赖任何远程嵌入服务（无网络、无密钥）的前提下，给出可用的
// 语义检索能力。采用「分词 → 哈希技巧（hashing trick）→ 词频(TF) 加权 → L2 归一化」
// 的稠密向量方案：维度数固定（DefaultLocalDim），中英文混合文本均可处理——
// 英文按词（[a-z0-9]+），中文按「字 unigram + 二元 bigram」捕捉局部语义。
// 余弦相似度在该向量上等价于带位置/词频权重的集合重叠度，足以支撑知识库检索召回。
//
// 升级位：嵌入器实现框架 embedder.Embedder 接口，未来可无缝替换为 pgvector /
// OpenAI / 本地 transformers 等真实语义嵌入，检索与存储层无需改动。
package embed

import (
	"context"
	"hash/fnv"
	"math"
	"regexp"
	"strings"
	"unicode"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
)

// DefaultLocalDim 是本地嵌入向量的固定维度（哈希桶数）。
const DefaultLocalDim = 256

// reASCIIWord 匹配连续的 ASCII 字母/数字词。
var reASCIIWord = regexp.MustCompile(`[a-z0-9]+`)

// LocalEmbedder 是离线确定性嵌入器，实现 embedder.Embedder 接口。
type LocalEmbedder struct {
	dim int
}

// 编译期断言：LocalEmbedder 满足框架 embedder.Embedder 接口（升级为真实嵌入器时类型安全）。
var _ embedder.Embedder = (*LocalEmbedder)(nil)

// NewLocalEmbedder 构造本地嵌入器；dim<=0 时使用 DefaultLocalDim。
func NewLocalEmbedder(dim int) *LocalEmbedder {
	if dim <= 0 {
		dim = DefaultLocalDim
	}
	return &LocalEmbedder{dim: dim}
}

// GetDimensions 返回嵌入向量维度。
func (e *LocalEmbedder) GetDimensions() int { return e.dim }

// GetEmbedding 生成文本的嵌入向量（L2 归一化后）。
func (e *LocalEmbedder) GetEmbedding(_ context.Context, text string) ([]float64, error) {
	vec := e.embed(text)
	return vec, nil
}

// GetEmbeddingWithUsage 生成嵌入并返回用量信息（本地嵌入无远程用量，usage 为 nil）。
func (e *LocalEmbedder) GetEmbeddingWithUsage(ctx context.Context, text string) ([]float64, map[string]any, error) {
	vec, err := e.GetEmbedding(ctx, text)
	return vec, nil, err
}

// embed 是核心：分词 → 哈希桶累加 TF → L2 归一化。
func (e *LocalEmbedder) embed(text string) []float64 {
	vec := make([]float64, e.dim)
	counts := map[int]float64{}

	for _, w := range tokenize(text) {
		h := fnv.New32a()
		_, _ = h.Write([]byte(w))
		idx := int(h.Sum32() % uint32(e.dim))
		counts[idx] += 1
	}

	// 子线性 TF 加权（1+log(tf)），抑制高频词主导。
	var norm float64
	for idx, c := range counts {
		v := 1 + math.Log(c)
		vec[idx] = v
		norm += v * v
	}
	if norm > 0 {
		inv := 1 / math.Sqrt(norm)
		for i := range vec {
			vec[i] *= inv
		}
	}
	return vec
}

// tokenize 把文本切分为归一化的 token 列表：
//   - ASCII 词（小写）；
//   - 连续 CJK 串：产出「单字 unigram」与「相邻二元 bigram」，兼顾召回与局部语义。
func tokenize(text string) []string {
	lower := strings.ToLower(text)
	var tokens []string

	// ASCII 词
	for _, w := range reASCIIWord.FindAllString(lower, -1) {
		if len(w) >= 1 {
			tokens = append(tokens, "w:"+w)
		}
	}

	// 遍历 rune，抽取连续 CJK 段
	runes := []rune(text)
	var cjk []rune
	flush := func() {
		if len(cjk) == 0 {
			return
		}
		for i := 0; i < len(cjk); i++ {
			tokens = append(tokens, "c:"+string(cjk[i])) // unigram
			if i+1 < len(cjk) {
				tokens = append(tokens, "c:"+string(cjk[i])+string(cjk[i+1])) // bigram
			}
		}
		cjk = nil
	}
	for _, r := range runes {
		if isCJK(r) {
			cjk = append(cjk, r)
		} else {
			flush()
		}
	}
	flush()
	return tokens
}

// isCJK 判断是否为中日韩统一表意文字（含扩展 A 与标点范围粗略覆盖）。
func isCJK(r rune) bool {
	return unicode.Is(unicode.Ideographic, r) ||
		(r >= 0x3040 && r <= 0x30FF) || // 平假名/片假名
		(r >= 0xAC00 && r <= 0xD7A3) || // 谚文音节
		(r >= 0x3400 && r <= 0x4DBF) || // 扩展 A
		(r >= 0x20000 && r <= 0x2A6DF) // 扩展 B
}
