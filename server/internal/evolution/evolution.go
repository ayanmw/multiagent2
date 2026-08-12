// Package evolution 实现「技能进化飞轮」后端（M5-03）。
//
// 设计目标：平台在自主 Loop / 日常对话结束后，后台异步扫描已结束 session 的
// transcript，经 LLM 提取出候选 SKILL.md（name/description/body），经质量门控
// （长度/结构/去重）后写入 skill_candidates 表，状态为 pending，等待人工审批
// （M5-04 approve 后才发布为托管技能进共享库，本任务不自动发布）。
//
// 分层：
//   - Service.Scan：遍历全部会话 → 建 transcript → Extractor 提取 → 质量门控 → 去重 → 落库；
//   - Extractor 接口：默认 LLMExtractor 走真实引擎，测试可注入 mock；
//   - quality.go：纯函数质量门控与内容哈希（可独立单测）。
//
// 去重双保险：① 按 source_session_key 跳过已提取会话（避免重复扫描）；
// ② 按 content_hash 跳过相同内容的候选（避免同一套路反复成候选）。
package evolution

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"gorm.io/gorm"
)

// 扫描默认参数。
const (
	// DefaultMinMessages 是触发提取的最小消息数（>= 2 轮 user↔assistant 才值得提炼）。
	DefaultMinMessages = 4
	// DefaultMaxTranscriptChars 是送入 LLM 的 transcript 长度上限（控 token / 防超长）。
	DefaultMaxTranscriptChars = 12000
)

// Service 是「技能进化飞轮」的核心服务（M5-03）。
type Service struct {
	db                 *gorm.DB
	extractor          Extractor
	minMessages        int
	maxTranscriptChars int
}

// NewService 构造扫描服务。extractor 为提取器（生产用 LLMExtractor，测试用 mock）。
func NewService(db *gorm.DB, extractor Extractor) *Service {
	return &Service{
		db:                 db,
		extractor:          extractor,
		minMessages:        DefaultMinMessages,
		maxTranscriptChars: DefaultMaxTranscriptChars,
	}
}

// ScanReport 是单次扫描的统计结果。
type ScanReport struct {
	Scanned int `json:"scanned"` // 实际评估的会话数（含过短/无技能/被拦截）
	Created int `json:"created"` // 新写入的 pending 候选数
	Skipped int `json:"skipped"` // 跳过数（过短/无技能/低质量/重复）
	Errors  int `json:"errors"`  // 真实错误数（已计入统计，未中断扫描）
}

// Scan 执行一次全量扫描：遍历全部会话，对达标会话提取候选技能并落库。
// 任何单会话的错误都不会中断整体扫描（错误计入 report.Errors 后继续）。
func (s *Service) Scan(ctx context.Context) (*ScanReport, error) {
	sessions, err := repo.ListSessionsAll(s.db)
	if err != nil {
		return nil, err
	}
	rep := &ScanReport{}
	for _, sess := range sessions {
		created, eerr := s.processSession(ctx, sess)
		if eerr != nil {
			rep.Errors++
			log.Printf("[evolution] 处理会话 %s(uid=%d) 出错: %v", sess.SessionKey, sess.UserID, eerr)
			continue
		}
		rep.Scanned++
		if created {
			rep.Created++
		} else {
			rep.Skipped++
		}
	}
	return rep, nil
}

// processSession 处理单个会话：去重 → 取消息 → 提取 → 质量门控 → 去重 → 落库。
// 返回 created=true 表示成功写入一条 pending 候选；false 表示跳过（非错误原因）。
func (s *Service) processSession(ctx context.Context, sess model.Session) (bool, error) {
	// 去重①：该会话已提取过（非 rejected）则跳过，避免重复扫描。
	exists, err := repo.ExistsCandidateForSession(s.db, sess.UserID, sess.SessionKey)
	if err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}

	msgs, err := repo.ListSessionMessages(s.db, sess.ID)
	if err != nil {
		return false, err
	}
	if len(msgs) < s.minMessages {
		return false, nil
	}

	transcript := s.buildTranscript(msgs)
	if transcript == "" {
		return false, nil
	}

	raw, eerr := s.extractor.Extract(ctx, sess.UserID, transcript)
	if eerr != nil {
		// 「会话无可复用技能」是预期结果，静默跳过而非计入错误。
		if IsNoSkill(eerr) {
			return false, nil
		}
		return false, eerr
	}

	// 质量门控：空泛/无结构/占位内容在此被拦截，不写入审批队列。
	if q := QualityGate(raw.Name, raw.Description, raw.Body); !q.Passed {
		log.Printf("[evolution] 会话 %s 候选未过质量门控: %v", sess.SessionKey, q.Notes)
		return false, nil
	}

	hash := ContentHash(raw.Name, raw.Body)
	// 去重②：相同内容哈希的候选已存在则跳过（跨会话去重）。
	dup, derr := repo.ExistsCandidateByHash(s.db, sess.UserID, hash)
	if derr != nil {
		return false, derr
	}
	if dup {
		return false, nil
	}

	cand := &model.SkillCandidate{
		UserID:           sess.UserID,
		Name:             raw.Name,
		Description:      raw.Description,
		Body:             raw.Body,
		SourceSessionKey: sess.SessionKey,
		ContentHash:      hash,
		Status:           model.SkillCandidatePending,
	}
	if cerr := repo.CreateSkillCandidate(s.db, cand); cerr != nil {
		return false, cerr
	}
	return true, nil
}

// buildTranscript 把会话消息拼成「role: content」文本，并按长度上限截断（控 token）。
func (s *Service) buildTranscript(msgs []model.Message) string {
	var b strings.Builder
	for i := range msgs {
		role := msgs[i].Role
		if role == "" {
			role = "user"
		}
		b.WriteString(role)
		b.WriteString(": ")
		b.WriteString(msgs[i].Content)
		b.WriteString("\n\n")
	}
	out := b.String()
	if n := len([]rune(out)); n > s.maxTranscriptChars {
		out = string([]rune(out)[:s.maxTranscriptChars])
	}
	return out
}

// StartLoop 启动后台周期扫描（M5-03 自主飞轮）。interval<=0 时默认 1 小时。
// ctx 取消即退出（进程优雅关闭时由调用方 cancel）。
func (s *Service) StartLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Hour
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	log.Printf("[evolution] 后台技能进化扫描已启动，间隔 %s", interval)
	for {
		select {
		case <-ctx.Done():
			log.Println("[evolution] 后台技能进化扫描已停止")
			return
		case <-ticker.C:
			rep, err := s.Scan(ctx)
			if err != nil {
				log.Printf("[evolution] 扫描出错: %v", err)
				continue
			}
			log.Printf("[evolution] 扫描完成: scanned=%d created=%d skipped=%d errors=%d",
				rep.Scanned, rep.Created, rep.Skipped, rep.Errors)
		}
	}
}
