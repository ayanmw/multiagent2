// Package source 提供把「内存中已构建好的文档切片」喂给框架 knowledge 管线的静态源
// （M5-02 Knowledge RAG）。
//
// 上层 Manager 负责做切片与元数据；本源仅持有 []*document.Document 并原样返回，
// 使框架 knowledge.AddSource 能把它们逐个嵌入并写入向量库，无需落地文件。
package source

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

// StaticSource 是持有预构建文档的内存源，实现 knowledge/source.Source 接口。
type StaticSource struct {
	name      string
	docType   string
	metadata  map[string]any
	documents []*document.Document
}

// New 构造静态源。name 作为知识库内文档（来源）标识；documents 为已切片的文档列表。
func New(name string, documents []*document.Document) *StaticSource {
	return &StaticSource{
		name:      name,
		docType:   "text",
		metadata:  map[string]any{"kb_source": name},
		documents: documents,
	}
}

// ReadDocuments 返回预构建的文档切片（实现 source.Source）。
func (s *StaticSource) ReadDocuments(_ context.Context) ([]*document.Document, error) {
	return s.documents, nil
}

// Name 返回来源名称（实现 source.Source）。
func (s *StaticSource) Name() string { return s.name }

// Type 返回来源类型（实现 source.Source）。
func (s *StaticSource) Type() string { return s.docType }

// GetMetadata 返回来源元数据（实现 source.Source）。
func (s *StaticSource) GetMetadata() map[string]any {
	out := make(map[string]any, len(s.metadata))
	for k, v := range s.metadata {
		out[k] = v
	}
	return out
}
