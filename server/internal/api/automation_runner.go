package api

import (
	"context"
	"path/filepath"
	"strconv"
	"time"

	taskrunruntime "trpc.group/trpc-go/trpc-agent-go/agent/taskrun"
	"trpc.group/trpc-go/trpc-agent-go/session"

	"github.com/ayanmw/multiagent2/server/internal/artifact"
	"github.com/ayanmw/multiagent2/server/internal/crypto"
	"github.com/ayanmw/multiagent2/server/internal/engine"
	"github.com/ayanmw/multiagent2/server/internal/executor"
	"github.com/ayanmw/multiagent2/server/internal/metrics"
	"github.com/ayanmw/multiagent2/server/internal/model"
	"github.com/ayanmw/multiagent2/server/internal/repo"
	"github.com/ayanmw/multiagent2/server/internal/scheduler"
	"gorm.io/gorm"
)

// AutomationLoopConfig 是自主化 Loop 运行器所需的全部依赖（与 ChatHandler 对齐，
// 额外强制开启子代理 + 目标契约，使 automation 以「Goal Session」形式跑完整 Loop）。
type AutomationLoopConfig struct {
	DB                 *gorm.DB
	EncKey             []byte
	EngineTimeout      time.Duration
	WorkspaceRoot      string
	Team               engine.TeamConfig
	StateStore         artifact.Store
	EnableState        bool
	SkillRoot          string
	SkillDataDir       string
	SkillWarmStart     bool
	SkillMaxChars      int
	TaskRunController  taskrunruntime.Controller
	TaskRunSession     session.Service
	ToolSearchEnabled  bool
	ToolSearchProvider engine.ToolSearchProvider
	CheckpointEnabled  bool
}

// NewAutomationLoopRunner 构造生产用 Loop 运行器（实现 scheduler.AutomationRunner）。
// 调度器（M4-02）到点调用其 Run 方法：为 automation 创建/复用会话，把 goal_prompt
// 作为首条用户消息驱动 Goal Loop 跑到目标收敛，并落库助手回复与 token 用量。
func NewAutomationLoopRunner(cfg AutomationLoopConfig) scheduler.AutomationRunner {
	return &engineLoopRunner{cfg: cfg}
}

// engineLoopRunner 是 scheduler.AutomationRunner 的生产实现，复用 ChatHandler 的
// 模型解析、引擎构造与用量计量逻辑，仅在团队模式上强制开启目标契约。
type engineLoopRunner struct {
	cfg AutomationLoopConfig
}

// Run 执行一次自动化：解析该用户默认模型 + Provider，构造「目标契约」引擎，
// 把 goal_prompt 作为首条消息跑 Loop，落库助手回复与 token 用量。
func (r *engineLoopRunner) Run(ctx context.Context, a *model.Automation, sessionKey string) error {
	uid := a.UserID

	// 解析默认模型与 Provider（与 ChatHandler 一致）。
	m, p, err := resolveChatModel(r.cfg.DB, uid, 0)
	if err != nil {
		return err
	}
	apiKey := ""
	if p.APIKeyEnc != "" {
		dec, derr := crypto.Decrypt(p.APIKeyEnc, r.cfg.EncKey)
		if derr != nil {
			return derr
		}
		apiKey = dec
	}

	// 持久化会话并写入 goal_prompt 作为首条用户消息（与正常对话同路径）。
	sess, serr := repo.GetOrCreateSession(r.cfg.DB, uid, sessionKey)
	if serr != nil {
		return serr
	}
	if aerr := repo.AppendMessage(r.cfg.DB, sess.ID, "user", a.GoalPrompt); aerr != nil {
		return aerr
	}

	// 工作目录：回退默认 WorkspaceRoot/<uid>。
	workdir, dErr := ensureWorkdir(r.cfg.WorkspaceRoot, uid, "")
	if dErr != nil {
		return dErr
	}

	// 强制开启子代理 + 目标契约（Goal Session 语义）：目标未收敛不许结束，
	// 使无人值守 Loop 能持续推进到 complete/blocked 才停。
	team := r.cfg.Team
	team.EnableSubAgents = true
	team.EnableGoal = true

	// 人工检查点落库回调（M3-05）：无人值守命中 ask 危险命令时生成 checkpoint 并暂停。
	var checkpointer executor.Checkpointer
	if r.cfg.CheckpointEnabled {
		checkpointer = func(req executor.CheckpointRequest) (string, error) {
			cp := &model.Checkpoint{
				SessionID: sessionKey,
				UserID:    uid,
				Command:   req.Command,
				Workdir:   req.Workdir,
				Reason:    req.Reason,
				Context:   req.Context,
				Status:    model.CheckpointPending,
			}
			if cerr := repo.CreateCheckpoint(r.cfg.DB, cp); cerr != nil {
				return "", cerr
			}
			return cp.DisplayID(), nil
		}
	}

	eng, err := engine.New(engine.ModelConfig{
		ModelID:            m.ModelID,
		BaseURL:            p.BaseURL,
		APIKey:             apiKey,
		Protocol:           string(p.Protocol),
		Timeout:            r.cfg.EngineTimeout,
		Team:               team,
		Workdir:            workdir,
		EnableState:        r.cfg.EnableState,
		StateStore:         r.cfg.StateStore,
		SkillWarmStart:     r.cfg.SkillWarmStart,
		SkillRoots:         []string{r.cfg.SkillRoot, filepath.Join(r.cfg.SkillDataDir, strconv.FormatUint(uint64(uid), 10))},
		SkillKeywords:      nil,
		SkillMaxChars:      r.cfg.SkillMaxChars,
		TaskRunController:  r.cfg.TaskRunController,
		TaskRunSession:     r.cfg.TaskRunSession,
		ToolSearchEnabled:  r.cfg.ToolSearchEnabled,
		ToolSearchProvider: r.cfg.ToolSearchProvider,
		ToolSearchUserID:   uid,
		Auditor:            repo.NewDBAuditor(r.cfg.DB, uid),
		Checkpointer:       checkpointer,
	})
	if err != nil {
		return err
	}
	defer eng.Close()

	history := loadChatHistory(r.cfg.DB, sess.ID, 1)
	llmStart := time.Now()
	reply, err := eng.Chat(engine.WithUserID(ctx, strconv.FormatUint(uint64(uid), 10)), sessionKey, a.GoalPrompt, history)
	// M3-09：记录 LLM 调用数 / 时延 / 错误率。
	metrics.RecordLLMCall(ctx, p.Name, m.Name, time.Since(llmStart), err)
	if err != nil {
		return err
	}

	// M3-03：记录 token 用量（按 user / session / provider / model 归属）。
	recordEngineUsage(r.cfg.DB, eng, uid, sess, p, m, buildPromptText(history, a.GoalPrompt), reply)

	// 仅在正常结束时落库助手消息（与 ChatHandler 一致）。
	if perr := repo.AppendMessage(r.cfg.DB, sess.ID, "assistant", reply); perr != nil {
		return perr
	}
	return nil
}
