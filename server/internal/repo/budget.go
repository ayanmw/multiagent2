package repo

import (
	"errors"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/ayanmw/multiagent2/server/internal/model"
	"gorm.io/gorm"
)

// budgetEnabled 读取平台级预算护栏总开关（env BUDGET_ENABLED，默认开启）。
// 关闭后所有预算检查直接放行（仅本地调试 / 紧急恢复用）。
func budgetEnabled() bool {
	v := os.Getenv("BUDGET_ENABLED")
	if v == "" {
		return true
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		log.Printf("[WARN] BUDGET_ENABLED 非法，按默认开启处理: %v", err)
		return true
	}
	return b
}

// GetBudgetPolicy 按 (scope, scopeKey) 精确查询一条预算策略。
// 未命中返回 (nil, nil)。
func GetBudgetPolicy(db *gorm.DB, scope, scopeKey string) (*model.BudgetPolicy, error) {
	var p model.BudgetPolicy
	err := db.Where("scope = ? AND scope_key = ?", scope, scopeKey).First(&p).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

// GetEffectiveUserBudgetPolicy 返回作用于某用户的最具体预算策略：
// 先查用户特定策略 (scope=user, scopeKey=uid)，查不到再回退全局默认 (scope=user, scopeKey="")。
func GetEffectiveUserBudgetPolicy(db *gorm.DB, uid uint) (*model.BudgetPolicy, error) {
	p, err := GetBudgetPolicy(db, model.BudgetScopeUser, strconv.FormatUint(uint64(uid), 10))
	if err != nil {
		return nil, err
	}
	if p != nil {
		return p, nil
	}
	return GetBudgetPolicy(db, model.BudgetScopeUser, "")
}

// UpsertBudgetPolicy 按 (scope, scopeKey) 唯一键 upsert 一条预算策略。
func UpsertBudgetPolicy(db *gorm.DB, p *model.BudgetPolicy) error {
	var existing model.BudgetPolicy
	err := db.Where("scope = ? AND scope_key = ?", p.Scope, p.ScopeKey).First(&existing).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return db.Create(p).Error
		}
		return err
	}
	existing.MaxTokens = p.MaxTokens
	existing.Window = p.Window
	return db.Save(&existing).Error
}

// ListBudgetPolicies 返回全部预算策略（管理员视图）。
func ListBudgetPolicies(db *gorm.DB) ([]model.BudgetPolicy, error) {
	var list []model.BudgetPolicy
	if err := db.Order("scope asc, scope_key asc, id asc").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// DeleteBudgetPolicy 删除指定 id 的预算策略。
func DeleteBudgetPolicy(db *gorm.DB, id uint) error {
	return db.Delete(&model.BudgetPolicy{}, id).Error
}

// BudgetEvaluation 是预算评估的结果（M3-04 运行时拦截判定）。
type BudgetEvaluation struct {
	Blocked bool                // 是否预算耗尽，需暂停后续 LLM 调用
	Policy  *model.BudgetPolicy // 触发拦截的策略（Blocked=false 时为 nil）
	Scope   string              // 触发的作用域
	Used    int64               // 该作用域窗口内已用 token
	Max     int64               // 该作用域阈值
}

// windowStart 返回统计窗口的起始时间：daily=今日零点，total=零值（不限）。
func windowStart(window string) time.Time {
	if window == model.BudgetWindowTotal {
		return time.Time{}
	}
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
}

// EvaluateBudgets 评估本次请求命中的全部预算策略（user / session / automation 三级），
// 任一超限即判定为平台级预算耗尽（Blocked=true），应暂停该 session/automation 后续 LLM 调用。
//
// 统计口径（与 M3-03 用量记录一致，复用 repo.SumUsageRecords）：
//   - user 作用域：按 UserID 聚合窗口内 total_tokens；
//   - session 作用域：按 UserID + SessionKey 聚合；
//   - automation 作用域（M4 接入 automation_id 前，按 UserID 近似聚合）暂由 automationID 非空时启用。
//
// 总开关 BUDGET_ENABLED=false 时直接放行。
func EvaluateBudgets(db *gorm.DB, uid uint, sessionKey, automationID string) (BudgetEvaluation, error) {
	notBlocked := BudgetEvaluation{Blocked: false}
	if !budgetEnabled() {
		return notBlocked, nil
	}
	start := func(p *model.BudgetPolicy) time.Time { return windowStart(p.Window) }

	// 1) user 作用域（最具体用户策略 → 全局默认）。
	userPolicy, err := GetEffectiveUserBudgetPolicy(db, uid)
	if err != nil {
		return notBlocked, err
	}
	if userPolicy != nil {
		totals, err := SumUsageRecords(db, UsageRecordFilter{UserID: uid, Start: start(userPolicy)})
		if err != nil {
			return notBlocked, err
		}
		if totals.TotalTokens >= userPolicy.MaxTokens {
			return BudgetEvaluation{Blocked: true, Policy: userPolicy, Scope: model.BudgetScopeUser, Used: totals.TotalTokens, Max: userPolicy.MaxTokens}, nil
		}
	}

	// 2) session 作用域（仅当存在该会话策略时）。
	if sessionKey != "" {
		sessPolicy, err := GetBudgetPolicy(db, model.BudgetScopeSession, sessionKey)
		if err != nil {
			return notBlocked, err
		}
		if sessPolicy != nil {
			totals, err := SumUsageRecords(db, UsageRecordFilter{UserID: uid, SessionKey: sessionKey, Start: start(sessPolicy)})
			if err != nil {
				return notBlocked, err
			}
			if totals.TotalTokens >= sessPolicy.MaxTokens {
				return BudgetEvaluation{Blocked: true, Policy: sessPolicy, Scope: model.BudgetScopeSession, Used: totals.TotalTokens, Max: sessPolicy.MaxTokens}, nil
			}
		}
	}

	// 3) automation 作用域（M4 全面接入前，automationID 非空时按用户近似聚合）。
	if automationID != "" {
		autoPolicy, err := GetBudgetPolicy(db, model.BudgetScopeAutomation, automationID)
		if err != nil {
			return notBlocked, err
		}
		if autoPolicy != nil {
			totals, err := SumUsageRecords(db, UsageRecordFilter{UserID: uid, Start: start(autoPolicy)})
			if err != nil {
				return notBlocked, err
			}
			if totals.TotalTokens >= autoPolicy.MaxTokens {
				return BudgetEvaluation{Blocked: true, Policy: autoPolicy, Scope: model.BudgetScopeAutomation, Used: totals.TotalTokens, Max: autoPolicy.MaxTokens}, nil
			}
		}
	}

	return notBlocked, nil
}
