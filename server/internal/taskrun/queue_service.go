package taskrun

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	taskrunruntime "trpc.group/trpc-go/trpc-agent-go/agent/taskrun"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"

	"github.com/ayanmw/multiagent2/server/internal/taskrun/queue"
)

// ---------------------------------------------------------------------------
// QueueService：多节点 taskrun 控制器（M8-03）
// ---------------------------------------------------------------------------
//
// 突破框架 inprocess.Service 的单进程限制：run 记录外置到共享队列（queue.Queue，
// 默认 SQLite 文件），本节点只做「poller 领取 + 执行 + 状态回写」：
//
//   - 每节点一个 poller goroutine，周期性（PollInterval）执行 RequeueExpired（重拾
//     lease 过期的崩溃任务）+ ClaimNext（原子领取 queued 任务）；
//   - 领取到的任务在本节点 goroutine 执行（与 inprocess 每 Spawn 一 goroutine 对齐），
//     执行中由 leaseKeepAlive 周期性续约，cancelWatcher 检测跨节点取消；
//   - 节点崩溃（进程消失、未续约）→ lease 过期 → 其他节点 RequeueExpired 重拾重跑
//     （attempts+1，超 MaxAttempts 置 failed；canceling 置 canceled 不重试）。
//
// 与 WithWorkerIdentity 包装兼容：QueueService 不依赖 SpawnRequest.RunContext 本地
// 闭包（跨节点不可用），执行时从 Run 持久化字段（OwnerUserID/ParentSessionID）重建
// worker 身份（WithWorkerUserID/WithWorkerParentSession 注入 ctx），worker 工厂在
// selectAgent 阶段即可解析归属用户。

// QueueOptions 配置 QueueService。
type QueueOptions struct {
	// PollInterval 是 poller 轮询间隔（默认 1s）。越小越灵敏（取消/重拾延迟），
	// 越大越省数据库查询；生产建议 1-5s。
	PollInterval time.Duration
	// LeaseDuration 是单次领取的租约时长（默认 30s）。长任务执行中由 leaseKeepAlive
	// 每 LeaseDuration/2 续约一次；节点崩溃后任务在 lease 过期时被重拾。
	LeaseDuration time.Duration
	// MaxAttempts 是崩溃重拾上限（默认 3）。超过后任务置 failed 不再执行。
	MaxAttempts int
	// Observer 是可选的 run 生命周期观察者（M2-05 worktree merge 钩子等）。
	Observer taskrunruntime.Observer
	// Clock 是时钟注入点（测试用）；nil 用 time.Now。
	Clock func() time.Time
	// RunFunc 是 worker 执行函数注入点：nil 时由 NewQueueController 用内部 runner
	// 提供默认实现（queueRunnerRun）；测试注入 fake 即可离线验证多节点语义。
	RunFunc func(ctx context.Context, run taskrunruntime.Run) (result string, err error)
}

// QueueOption 是 QueueOptions 的函数式配置项。
type QueueOption func(*QueueOptions)

// WithQueuePollInterval 设置 poller 轮询间隔。
func WithQueuePollInterval(d time.Duration) QueueOption {
	return func(o *QueueOptions) {
		if d > 0 {
			o.PollInterval = d
		}
	}
}

// WithQueueLease 设置 lease 租约时长。
func WithQueueLease(d time.Duration) QueueOption {
	return func(o *QueueOptions) {
		if d > 0 {
			o.LeaseDuration = d
		}
	}
}

// WithQueueMaxAttempts 设置崩溃重拾上限。
func WithQueueMaxAttempts(n int) QueueOption {
	return func(o *QueueOptions) {
		if n >= 0 {
			o.MaxAttempts = n
		}
	}
}

// WithQueueRunFunc 注入 worker 执行函数（测试/自定义执行器）。
func WithQueueRunFunc(fn func(ctx context.Context, run taskrunruntime.Run) (string, error)) QueueOption {
	return func(o *QueueOptions) {
		if fn != nil {
			o.RunFunc = fn
		}
	}
}

// QueueService 实现 taskrunruntime.Controller，基于外部队列的多节点运行。
type QueueService struct {
	queue  queue.Queue
	nodeID string // 本节点唯一标识（hostname-pid），用于 lease 归属
	opts   QueueOptions

	baseCtx   context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	wake      chan struct{} // Spawn 后唤醒 poller 立即领取
	startOnce sync.Once

	mu      sync.Mutex
	running map[string]context.CancelFunc // 本节点正在执行的 runID → cancel
}

var _ taskrunruntime.Controller = (*QueueService)(nil)

// NewQueueService 创建外部队列控制器。nodeID 为空时自动生成（uuid 前缀）。
func NewQueueService(q queue.Queue, nodeID string, opts QueueOptions) *QueueService {
	if opts.PollInterval <= 0 {
		opts.PollInterval = time.Second
	}
	if opts.LeaseDuration <= 0 {
		opts.LeaseDuration = 30 * time.Second
	}
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = 3
	}
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	if opts.RunFunc == nil {
		// 直接 NewQueueService（而非 NewQueueController）时的兜底：真实装配
		// 由 NewQueueController 注入 runner 执行函数。
		opts.RunFunc = func(ctx context.Context, run taskrunruntime.Run) (string, error) {
			return "", fmt.Errorf("taskrun: 未配置 worker 执行函数（请用 NewQueueController 装配或注入 QueueOptions.RunFunc）")
		}
	}
	if nodeID == "" {
		nodeID = "node-" + uuid.NewString()[:8]
	}
	return &QueueService{
		queue:   q,
		nodeID:  nodeID,
		opts:    opts,
		wake:    make(chan struct{}, 1),
		running: make(map[string]context.CancelFunc),
	}
}

// Start 启动 poller goroutine（每节点调用一次；重复调用为 no-op）。
func (s *QueueService) Start(ctx context.Context) {
	if s == nil {
		return
	}
	s.startOnce.Do(func() {
		if ctx == nil {
			ctx = context.Background()
		}
		s.baseCtx, s.cancel = context.WithCancel(ctx)
		s.wg.Add(1)
		go s.pollLoop()
		log.Printf("[taskrun/queue] 节点 %s poller 已启动（poll=%v lease=%v maxAttempts=%d）",
			s.nodeID, s.opts.PollInterval, s.opts.LeaseDuration, s.opts.MaxAttempts)
	})
}

// Close 停止 poller 并取消本节点正在执行的任务。未完成的 running 任务保留 lease，
// 由其他节点在 lease 过期后重拾（优雅退出不丢任务）。
func (s *QueueService) Close() error {
	if s == nil {
		return nil
	}
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
	return nil
}

// ---------------------------------------------------------------------------
// Controller 接口实现
// ---------------------------------------------------------------------------

// Spawn 实现 Controller：校验 + 落队列（queued）+ 通知 poller 领取。
// 与 inprocess 的差异：不启动本地执行 goroutine，任务由任意节点 poller 领取执行，
// 因此 ChildSessionID/RequestID/Timeout 在入队时确定性生成并持久化（跨节点可重建）。
func (s *QueueService) Spawn(ctx context.Context, req taskrunruntime.SpawnRequest) (taskrunruntime.Run, error) {
	if s == nil || s.queue == nil {
		return taskrunruntime.Run{}, fmt.Errorf("taskrun: 外部队列控制器未初始化")
	}
	if s.baseCtx == nil {
		return taskrunruntime.Run{}, taskrunruntime.ErrNotStarted
	}
	if err := validateQueueSpawnRequest(req); err != nil {
		return taskrunruntime.Run{}, err
	}
	now := s.opts.Clock()
	runID := strings.TrimSpace(req.ID)
	if runID == "" {
		runID = uuid.NewString()
	}
	childSessionID := strings.TrimSpace(req.ChildSessionID)
	if childSessionID == "" {
		childSessionID = queueChildSessionID(runID, now)
	}
	requestID := strings.TrimSpace(req.RequestID)
	if requestID == "" {
		requestID = queueRequestID(runID, now)
	}
	run := taskrunruntime.Run{
		ID:              runID,
		OwnerUserID:     strings.TrimSpace(req.OwnerUserID),
		ParentSessionID: strings.TrimSpace(req.ParentSessionID),
		ParentAppName:   strings.TrimSpace(req.ParentAppName),
		AppName:         queueAppNameForSpawn(req),
		AgentName:       strings.TrimSpace(req.AgentName),
		Task:            strings.TrimSpace(req.Task),
		ChildSessionID:  childSessionID,
		RequestID:       requestID,
		Status:          taskrunruntime.StatusQueued,
		Metadata:        cloneQueueMetadata(req.Metadata),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if req.Timeout > 0 {
		if run.Metadata == nil {
			run.Metadata = make(map[string]string)
		}
		run.Metadata[queue.TimeoutKey] = strconv.FormatInt(int64(req.Timeout), 10)
	}
	if err := s.queue.Enqueue(ctx, queue.Row{Run: run}); err != nil {
		if errors.Is(err, queue.ErrRunAlreadyExists) {
			return taskrunruntime.Run{}, taskrunruntime.ErrRunAlreadyExists
		}
		return taskrunruntime.Run{}, err
	}
	s.notify(ctx, run)
	// 唤醒 poller 立即领取（非阻塞；无消费者也不卡）。
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return run, nil
}

// List 实现 Controller。
func (s *QueueService) List(ctx context.Context, filter taskrunruntime.ListFilter) ([]taskrunruntime.Run, error) {
	if s == nil || s.queue == nil {
		return nil, nil
	}
	return s.queue.List(ctx, filter)
}

// Get 实现 Controller。
func (s *QueueService) Get(ctx context.Context, runID string) (*taskrunruntime.Run, error) {
	if s == nil || s.queue == nil {
		return nil, taskrunruntime.ErrRunNotFound
	}
	row, err := s.queue.Get(ctx, runID)
	if err != nil {
		if errors.Is(err, queue.ErrRunNotFound) {
			return nil, taskrunruntime.ErrRunNotFound
		}
		return nil, err
	}
	run := row.Run
	return &run, nil
}

// Cancel 实现 Controller：
//   - queued（未被领取）→ 直接 canceled（保证不会被执行）；
//   - running → canceling（若本节点正在执行则立即取消 ctx；其他节点由 cancelWatcher
//     轮询检测后取消，执行结束收敛为 canceled）；
//   - 终态/canceling → 原样返回（changed=false）。
func (s *QueueService) Cancel(ctx context.Context, runID string) (*taskrunruntime.Run, bool, error) {
	if s == nil || s.queue == nil {
		return nil, false, taskrunruntime.ErrRunNotFound
	}
	runID = strings.TrimSpace(runID)
	row, err := s.queue.Get(ctx, runID)
	if err != nil {
		if errors.Is(err, queue.ErrRunNotFound) {
			return nil, false, taskrunruntime.ErrRunNotFound
		}
		return nil, false, err
	}
	run := row.Run
	if run.Status.IsTerminal() || run.Status == taskrunruntime.StatusCanceling {
		return &run, false, nil
	}
	now := s.opts.Clock()
	switch run.Status {
	case taskrunruntime.StatusQueued:
		run.Status = taskrunruntime.StatusCanceled
		run.UpdatedAt = now
		run.FinishedAt = &now
		ok, err := s.queue.UpdateStatusIf(ctx, runID, taskrunruntime.StatusQueued, taskrunruntime.StatusCanceled, &queue.Row{Run: run})
		if err != nil {
			return nil, false, err
		}
		if !ok {
			// 恰好被其他节点领取（已 running）：重读后走 canceling 分支。
			return s.Cancel(ctx, runID)
		}
		s.notify(ctx, run)
		return &run, true, nil
	default: // running
		run.Status = taskrunruntime.StatusCanceling
		run.UpdatedAt = now
		ok, err := s.queue.UpdateStatusIf(ctx, runID, taskrunruntime.StatusRunning, taskrunruntime.StatusCanceling, &queue.Row{Run: run})
		if err != nil {
			return nil, false, err
		}
		if !ok {
			// 状态已变（可能已终态/已取消）：重读后返回。
			return s.Cancel(ctx, runID)
		}
		s.mu.Lock()
		cancel := s.running[runID]
		s.mu.Unlock()
		if cancel != nil {
			cancel() // 本节点执行中：立即取消（watchdog 兜底跨节点）
		}
		s.notify(ctx, run)
		return &run, true, nil
	}
}

// Wait 实现 Controller：轮询队列直至终态（跨节点同样有效）。
func (s *QueueService) Wait(ctx context.Context, runID string) (*taskrunruntime.Run, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || s.queue == nil {
		return nil, taskrunruntime.ErrRunNotFound
	}
	runID = strings.TrimSpace(runID)
	interval := s.opts.PollInterval / 4
	if interval < 20*time.Millisecond {
		interval = 20 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		row, err := s.queue.Get(ctx, runID)
		if err != nil {
			if errors.Is(err, queue.ErrRunNotFound) {
				return nil, taskrunruntime.ErrRunNotFound
			}
			return nil, err
		}
		if row.Run.Status.IsTerminal() {
			run := row.Run
			return &run, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

// ---------------------------------------------------------------------------
// poller 执行循环
// ---------------------------------------------------------------------------

func (s *QueueService) pollLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(s.opts.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.baseCtx.Done():
			return
		case <-s.wake:
			s.pollOnce()
		case <-ticker.C:
			s.pollOnce()
		}
	}
}

func (s *QueueService) pollOnce() {
	ctx := s.baseCtx
	// 1) 重拾 lease 过期的任务（节点崩溃恢复；canceling→canceled 不重试）。
	if n, err := s.queue.RequeueExpired(ctx, s.opts.Clock(), s.opts.MaxAttempts); err != nil {
		log.Printf("[taskrun/queue] %s RequeueExpired 失败: %v", s.nodeID, err)
	} else if n > 0 {
		log.Printf("[taskrun/queue] %s 重拾 %d 个 lease 过期任务", s.nodeID, n)
	}
	// 2) 领取并执行（本循环领完当前 queued，每个任务一个 goroutine 并行执行）。
	for {
		row, err := s.queue.ClaimNext(ctx, s.nodeID, s.opts.Clock().Add(s.opts.LeaseDuration))
		if err != nil {
			log.Printf("[taskrun/queue] %s ClaimNext 失败: %v", s.nodeID, err)
			return
		}
		if row == nil {
			return
		}
		log.Printf("[taskrun/queue] %s 领取 run=%s task=%q", s.nodeID, row.Run.ID, row.Run.Task)
		s.wg.Add(1)
		go s.executeLeased(row)
	}
}

// executeLeased 执行一个已领取（running + lease）的任务，并收敛终态。
func (s *QueueService) executeLeased(row *queue.Row) {
	defer s.wg.Done()
	run := row.Run
	runID := run.ID

	timeout := queueRunTimeout(run)
	runCtx, cancel := context.WithCancel(s.baseCtx)
	if timeout > 0 {
		runCtx, cancel = context.WithTimeout(runCtx, timeout)
	}
	defer cancel()

	s.mu.Lock()
	s.running[runID] = cancel
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.running, runID)
		s.mu.Unlock()
	}()

	// lease 续约 + 跨节点取消检测（随本执行结束而停止）。
	stop := make(chan struct{})
	defer close(stop)
	go s.leaseKeepAlive(runID, cancel, stop)
	go s.cancelWatcher(runID, cancel, stop)

	// worker 身份注入（v1.10.0：worker 工厂在 selectAgent 阶段拿不到 invocation，
	// 从 Run 持久化字段重建并注入 ctx，与 WithWorkerIdentity 同款约定）。
	execCtx := WithWorkerUserID(WithWorkerParentSession(runCtx, run.ParentSessionID), run.OwnerUserID)
	s.notify(runCtx, run) // 执行中通知（observer 忽略非终态）

	result, runErr := s.opts.RunFunc(execCtx, run)
	if runErr != nil {
		log.Printf("[taskrun/queue] %s run=%s 执行结束 err=%v", s.nodeID, runID, runErr)
	}

	// 终态判定（与 inprocess.finishedRunView 对齐）：canceled / failed(超时) / failed / completed。
	terminal, termErr := queueTerminalStatus(runErr)
	now := s.opts.Clock()
	run.Result = queueTrimResult(result)
	run.UpdatedAt = now
	run.FinishedAt = &now
	run.Status = terminal
	run.Error = termErr

	// 乐观写：优先 running→terminal；若已被并发改为 canceling（Cancel 指令），收敛为
	// canceled；若已被重拾/处理（状态既非 running 也非 canceling），放弃写入避免覆盖。
	finalRow := queue.Row{Run: run}
	ok, err := s.queue.UpdateStatusIf(context.Background(), runID, taskrunruntime.StatusRunning, terminal, &finalRow)
	if !ok || err != nil {
		canceledRow := finalRow
		canceledRow.Run.Status = taskrunruntime.StatusCanceled
		ok2, err2 := s.queue.UpdateStatusIf(context.Background(), runID, taskrunruntime.StatusCanceling, taskrunruntime.StatusCanceled, &canceledRow)
		if err2 != nil {
			log.Printf("[taskrun/queue] %s run=%s 终态写失败: %v", s.nodeID, runID, err2)
			return
		}
		if !ok2 {
			log.Printf("[taskrun/queue] %s run=%s 终态写跳过（状态已被并发改变）", s.nodeID, runID)
			return
		}
		terminal = taskrunruntime.StatusCanceled
	}
	run.Status = terminal
	s.notify(context.Background(), run)
	log.Printf("[taskrun/queue] %s run=%s 终态=%s", s.nodeID, runID, terminal)
}

// leaseKeepAlive 周期性续约 lease；续约失败（状态已变/队列不可用）时取消本节点执行，
// 避免「本节点继续跑 + 其他节点已重拾」的双跑。
func (s *QueueService) leaseKeepAlive(runID string, cancel context.CancelFunc, stop <-chan struct{}) {
	interval := s.opts.LeaseDuration / 2
	if interval < 50*time.Millisecond {
		interval = 50 * time.Millisecond
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-s.baseCtx.Done():
			return
		case <-t.C:
			row, err := s.queue.Get(s.baseCtx, runID)
			if err != nil || row == nil {
				cancel() // 队列不可用/任务消失：放弃，交其他节点重拾
				return
			}
			if row.Run.Status != taskrunruntime.StatusRunning {
				return // 状态已变（canceling/终态）——由 cancelWatcher/执行收尾处理
			}
			row.WorkerID = s.nodeID
			row.LeaseExpiresAt = s.opts.Clock().Add(s.opts.LeaseDuration)
			ok, err := s.queue.UpdateStatusIf(s.baseCtx, runID, taskrunruntime.StatusRunning, taskrunruntime.StatusRunning, row)
			if err != nil {
				log.Printf("[taskrun/queue] %s run=%s 续约失败: %v（稍后重试）", s.nodeID, runID, err)
				continue
			}
			if !ok {
				// lease 已失效（被 RequeueExpired 重拾/取消）→ 停止本节点执行。
				log.Printf("[taskrun/queue] %s run=%s lease 失效，停止执行", s.nodeID, runID)
				cancel()
				return
			}
		}
	}
}

// cancelWatcher 轮询检测跨节点取消：DB 状态变为 canceling 时取消本节点执行 ctx，
// 使 RunFunc 返回 context.Canceled → 终态收敛为 canceled。
func (s *QueueService) cancelWatcher(runID string, cancel context.CancelFunc, stop <-chan struct{}) {
	interval := s.opts.PollInterval / 2
	if interval < 50*time.Millisecond {
		interval = 50 * time.Millisecond
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-s.baseCtx.Done():
			return
		case <-t.C:
			row, err := s.queue.Get(s.baseCtx, runID)
			if err != nil || row == nil {
				return
			}
			switch {
			case row.Run.Status == taskrunruntime.StatusCanceling:
				log.Printf("[taskrun/queue] %s run=%s 收到取消指令，终止执行", s.nodeID, runID)
				cancel()
				return
			case row.Run.Status.IsTerminal():
				return // 已被其他节点处理
			}
		}
	}
}

// notify 转发 run 生命周期事件给 Observer（M2-05 worktree 钩子）。
func (s *QueueService) notify(ctx context.Context, run taskrunruntime.Run) {
	if s == nil || s.opts.Observer == nil {
		return
	}
	s.opts.Observer.OnRunUpdate(ctx, run)
}

// ---------------------------------------------------------------------------
// NewQueueController 工厂与默认执行函数
// ---------------------------------------------------------------------------

// NewQueueController 组装外部队列控制器（M8-03）：
//   - worker Runner：NewRunnerWithAgentFactory(AppName, defaultAgentName, factory)，
//     挂可选持久化 session.Service（transcript 跨节点可读）；
//   - QueueService：poller 领取/执行/lease 重拾，实现 taskrunruntime.Controller。
//
// 多节点部署：每个副本调用本函数创建自己的 Controller（共享同一 queue 存储），
// 任务经原子领取恰好被一个节点执行；节点崩溃由 lease 重拾机制恢复。
func NewQueueController(ctx context.Context, defaultAgentName string, factory runner.AgentFactory, q queue.Queue, sessionSvc session.Service, observer taskrunruntime.Observer, opts ...QueueOption) (taskrunruntime.Controller, error) {
	if factory == nil {
		return nil, fmt.Errorf("taskrun: 未提供 worker 代理工厂")
	}
	if q == nil {
		return nil, fmt.Errorf("taskrun: 未提供外部队列")
	}
	workerOpts := []runner.Option{}
	if sessionSvc != nil {
		workerOpts = append(workerOpts, runner.WithSessionService(sessionSvc))
	}
	workerRunner := runner.NewRunnerWithAgentFactory(AppName, defaultAgentName, factory, workerOpts...)

	qopts := QueueOptions{Observer: observer}
	for _, o := range opts {
		if o != nil {
			o(&qopts)
		}
	}
	if qopts.RunFunc == nil {
		qopts.RunFunc = func(rctx context.Context, run taskrunruntime.Run) (string, error) {
			return queueRunnerRun(rctx, workerRunner, run)
		}
	}
	hostname, _ := os.Hostname()
	nodeID := fmt.Sprintf("node-%s-%d", hostname, os.Getpid())
	svc := NewQueueService(q, nodeID, qopts)
	svc.Start(ctx)
	return svc, nil
}

// queueRunnerRun 是默认 worker 执行函数：把 Run 重建为 runner.Run 调用（对齐
// inprocess.runChild），流式消费事件累加回复文本；执行结束返回文本与错误。
func queueRunnerRun(ctx context.Context, r runner.Runner, run taskrunruntime.Run) (string, error) {
	if r == nil {
		return "", fmt.Errorf("taskrun: 未提供 worker runner")
	}
	runOpts := []agent.RunOption{}
	if run.AppName != "" {
		runOpts = append(runOpts, agent.WithAppName(run.AppName))
	}
	runOpts = append(runOpts,
		agent.WithRequestID(run.RequestID),
		agent.MergeRuntimeState(map[string]any{
			taskrunruntime.RuntimeStateKeyRun:             true,
			taskrunruntime.RuntimeStateKeyRunID:           run.ID,
			taskrunruntime.RuntimeStateKeyParentSessionID: run.ParentSessionID,
		}),
	)
	if run.AgentName != "" {
		runOpts = append(runOpts, agent.WithAgentByName(run.AgentName))
	}
	events, err := r.Run(ctx, run.OwnerUserID, run.ChildSessionID, model.NewUserMessage(run.Task), runOpts...)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	var runErr error
	sawFull := false
	for evt := range events {
		if evt == nil || evt.Response == nil {
			continue
		}
		if evt.Response.Error != nil {
			runErr = errors.New(evt.Response.Error.Message)
			continue
		}
		switch evt.Response.Object {
		case model.ObjectTypeChatCompletion:
			if len(evt.Response.Choices) == 0 {
				continue
			}
			if content := evt.Response.Choices[0].Message.Content; content != "" {
				sb.Reset()
				sb.WriteString(content)
				sawFull = true
			}
		case model.ObjectTypeChatCompletionChunk:
			if sawFull {
				continue // 流式终帧的完整文本=增量之和，跳过避免重复
			}
			for _, ch := range evt.Response.Choices {
				if ch.Delta.Content != "" {
					sb.WriteString(ch.Delta.Content)
				}
			}
		}
	}
	if runErr != nil {
		return sb.String(), runErr
	}
	if err := ctx.Err(); err != nil {
		return sb.String(), err
	}
	return sb.String(), nil
}

// ---------------------------------------------------------------------------
// 内部辅助
// ---------------------------------------------------------------------------

const (
	queueChildSessionPrefix = "taskrun:"
	queueRequestIDPrefix    = "taskrun:"
	queueDefaultResultRunes = 20000 // 与 inprocess 同量级的 Result 存储上限
)

// validateQueueSpawnRequest 与 inprocess.validateSpawnRequest 对齐。
func validateQueueSpawnRequest(req taskrunruntime.SpawnRequest) error {
	if strings.TrimSpace(req.OwnerUserID) == "" {
		return fmt.Errorf("taskrun: empty owner")
	}
	if strings.TrimSpace(req.ParentSessionID) == "" {
		return fmt.Errorf("taskrun: empty parent session id")
	}
	if strings.TrimSpace(req.Task) == "" {
		return fmt.Errorf("taskrun: empty task")
	}
	return nil
}

// queueAppNameForSpawn 与 inprocess.appNameForSpawn 对齐：优先 RunOptions 里的
// WithAppName，其次 req.AppName。
func queueAppNameForSpawn(req taskrunruntime.SpawnRequest) string {
	runOpts := agent.NewRunOptions(req.RunOptions...)
	if app := strings.TrimSpace(runOpts.AppName); app != "" {
		return app
	}
	return strings.TrimSpace(req.AppName)
}

func queueChildSessionID(runID string, now time.Time) string {
	return fmt.Sprintf("%s%s:%d", queueChildSessionPrefix, strings.TrimSpace(runID), now.UnixNano())
}

func queueRequestID(runID string, now time.Time) string {
	return fmt.Sprintf("%s%s:%d", queueRequestIDPrefix, strings.TrimSpace(runID), now.UnixNano())
}

// queueRunTimeout 从 Metadata 恢复 SpawnRequest.Timeout（queue.TimeoutKey）。
func queueRunTimeout(run taskrunruntime.Run) time.Duration {
	if run.Metadata == nil {
		return 0
	}
	v, ok := run.Metadata[queue.TimeoutKey]
	if !ok {
		return 0
	}
	ns, err := strconv.ParseInt(v, 10, 64)
	if err != nil || ns <= 0 {
		return 0
	}
	return time.Duration(ns)
}

// queueTerminalStatus 判定终态（与 inprocess.finishedRunView 对齐）。
func queueTerminalStatus(runErr error) (taskrunruntime.Status, string) {
	switch {
	case errors.Is(runErr, context.Canceled):
		return taskrunruntime.StatusCanceled, ""
	case errors.Is(runErr, context.DeadlineExceeded):
		return taskrunruntime.StatusFailed, "run 超时: " + runErr.Error()
	case runErr != nil:
		return taskrunruntime.StatusFailed, runErr.Error()
	default:
		return taskrunruntime.StatusCompleted, ""
	}
}

func queueTrimResult(text string) string {
	trimmed := strings.TrimSpace(text)
	runes := []rune(trimmed)
	if len(runes) <= queueDefaultResultRunes {
		return trimmed
	}
	return string(runes[:queueDefaultResultRunes])
}

func cloneQueueMetadata(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
