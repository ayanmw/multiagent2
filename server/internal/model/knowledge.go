package model

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

// KnowledgeBase 是用户的私有知识库（M5-02 Knowledge RAG）。
// 一个知识库可索引多份文档（按 chunk 切片后存入向量库），对话时按用户归属检索注入。
type KnowledgeBase struct {
	ID             uint           `gorm:"primaryKey" json:"id"`
	UserID         uint           `gorm:"index;not null" json:"user_id"` // 归属用户（owner 隔离）
	Name           string         `gorm:"size:191;not null" json:"name"`
	Description    string         `gorm:"size:512" json:"description"`
	EmbeddingModel string         `gorm:"size:64;not null;default:local-hashing" json:"embedding_model"` // 嵌入模型标识（本地基线 local-hashing，预留 pgvector/远程升级位）
	DocCount       int            `gorm:"not null;default:0" json:"doc_count"`                          // 文档（源）数
	ChunkCount     int            `gorm:"not null;default:0" json:"chunk_count"`                        // 切片（向量）数
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	DeletedAt      gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName 固定表名，避免 GORM 复数化规则变化影响既有库。
func (KnowledgeBase) TableName() string { return "knowledge_bases" }

// Validate 校验知识库创建/更新参数的合法性。
func (kb *KnowledgeBase) Validate() error {
	if kb.Name == "" {
		return errors.New("知识库名称不能为空")
	}
	if len(kb.Name) > 191 {
		return errors.New("知识库名称过长")
	}
	if len(kb.Description) > 512 {
		return errors.New("知识库描述过长")
	}
	if kb.EmbeddingModel == "" {
		kb.EmbeddingModel = "local-hashing"
	}
	return nil
}
