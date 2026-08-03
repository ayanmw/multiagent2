// Package sessionstore 提供 trpc-agent-go 框架 v1.10.0 缺失的
// 「SQLite 持久化 session.Service」实现（见 docs/loop/PLAN.md M2-04 ①）。
//
// 框架 v1.10.0 的 session 包只有 inmemory / noop 两种后端，没有持久化实现；
// 而任务后台扇出（tool/taskrun）的子任务 transcript 读取（`read_task_run_transcript`）
// 依赖一个能跨进程重启保留事件的 session.Service。本包复用项目已有的 GORM + SQLite
// 连接（data/codeagent.db），把 child session 的事件落盘，使后台任务 transcript
// 在进程重启后仍然可读（顺带缓解 M0.5-01 的跨重启记忆）。
//
// 设计：每个 (app_name, user_id, session_id) 对应一行，events 与 state 以 JSON 文本
// 字段存储；AppendEvent 加载→追加→回写。仅 transcript 强一致性所需（CreateSession /
// AppendEvent / GetSession）做持久化，app/user/session 级 State 以内存镜像兜底
// （transcript 不依赖它们，重启后重建即可）。
package sessionstore

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// ErrNoDatabase 表示未提供持久化数据库连接（仅用于退化/测试场景）。
var ErrNoDatabase = errors.New("sessionstore: 未提供数据库连接")

// TaskRunSession 是持久化 session 行的 GORM 模型。
// (app_name, user_id, session_id) 组成唯一约束，对应一个 child session。
type TaskRunSession struct {
	ID         uint   `gorm:"primaryKey;autoIncrement"`
	AppName    string `gorm:"size:255;not null;uniqueIndex:idx_taskrun_sess,priority:1"`
	UserID     string `gorm:"size:255;not null;uniqueIndex:idx_taskrun_sess,priority:2"`
	SessionID  string `gorm:"size:255;not null;uniqueIndex:idx_taskrun_sess,priority:3"`
	StateJSON  string `gorm:"type:text"`
	EventsJSON string `gorm:"type:text"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// TableName 显式指定表名。
func (TaskRunSession) TableName() string { return "taskrun_sessions" }

// SessionService 是 SQLite 持久化的 session.Service 实现。
type SessionService struct {
	db *gorm.DB

	mu         sync.Mutex
	appState   map[string]session.StateMap
	userState  map[string]session.StateMap // key = appName\x00userID
	sessionCtx map[string]session.StateMap // key = appName\x00userID\x00sessionID
}

// New 构造持久化 session service 并自动迁移表结构。
func New(db *gorm.DB) *SessionService {
	if db != nil {
		_ = db.AutoMigrate(&TaskRunSession{})
	}
	return &SessionService{
		db:         db,
		appState:   map[string]session.StateMap{},
		userState:  map[string]session.StateMap{},
		sessionCtx: map[string]session.StateMap{},
	}
}

// loadRow 读取（或初始化）一行。db 为 nil 时退化为纯内存（便于无 DB 环境）。
func (s *SessionService) loadRow(key session.Key) (*TaskRunSession, error) {
	if s.db == nil {
		return nil, ErrNoDatabase
	}
	var row TaskRunSession
	err := s.db.Where("app_name = ? AND user_id = ? AND session_id = ?",
		key.AppName, key.UserID, key.SessionID).First(&row).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return &TaskRunSession{
				AppName:   key.AppName,
				UserID:    key.UserID,
				SessionID: key.SessionID,
				CreatedAt: time.Now(),
			}, nil
		}
		return nil, err
	}
	return &row, nil
}

// saveRow 写入一行（upsert）。
func (s *SessionService) saveRow(row *TaskRunSession) error {
	if s.db == nil {
		return nil
	}
	row.UpdatedAt = time.Now()
	return s.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "app_name"}, {Name: "user_id"}, {Name: "session_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"state_json", "events_json", "updated_at"}),
	}).Create(row).Error
}

func (s *SessionService) toSession(row *TaskRunSession) (*session.Session, error) {
	var events []event.Event
	if row.EventsJSON != "" {
		if err := json.Unmarshal([]byte(row.EventsJSON), &events); err != nil {
			return nil, err
		}
	}
	opts := []session.SessionOptions{session.WithSessionEvents(events)}
	if row.StateJSON != "" {
		var st session.StateMap
		if err := json.Unmarshal([]byte(row.StateJSON), &st); err == nil && len(st) > 0 {
			opts = append(opts, session.WithSessionState(st))
		}
	}
	return session.NewSession(row.AppName, row.UserID, row.SessionID, opts...), nil
}

// CreateSession implements session.Service.
func (s *SessionService) CreateSession(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
	opts ...session.Option,
) (*session.Session, error) {
	if err := key.CheckUserKey(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row, err := s.loadRow(key)
	if err != nil {
		return nil, err
	}
	if len(state) > 0 {
		b, _ := json.Marshal(state)
		row.StateJSON = string(b)
	}
	if err := s.saveRow(row); err != nil {
		return nil, err
	}
	return s.toSession(row)
}

// GetSession implements session.Service.
func (s *SessionService) GetSession(
	ctx context.Context,
	key session.Key,
	opts ...session.Option,
) (*session.Session, error) {
	if err := key.CheckSessionKey(); err != nil {
		return nil, err
	}
	opt := &session.Options{}
	for _, o := range opts {
		o(opt)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row, err := s.loadRow(key)
	if err != nil {
		return nil, err
	}
	if row.ID == 0 && row.EventsJSON == "" && row.StateJSON == "" {
		// 从未落库过：返回 nil（与 inmemory 一致，表示不存在）。
		return nil, nil
	}
	sess, err := s.toSession(row)
	if err != nil {
		return nil, err
	}
	if opt.EventNum > 0 && len(sess.Events) > opt.EventNum {
		sess.Events = sess.Events[len(sess.Events)-opt.EventNum:]
	}
	return sess, nil
}

// ListSessions implements session.Service.
func (s *SessionService) ListSessions(
	ctx context.Context,
	userKey session.UserKey,
	opts ...session.Option,
) ([]*session.Session, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}
	if s.db == nil {
		return []*session.Session{}, nil
	}
	var rows []TaskRunSession
	if err := s.db.Where("app_name = ? AND user_id = ?", userKey.AppName, userKey.UserID).
		Order("updated_at DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]*session.Session, 0, len(rows))
	for i := range rows {
		sess, err := s.toSession(&rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, sess)
	}
	return out, nil
}

// DeleteSession implements session.Service.
func (s *SessionService) DeleteSession(
	ctx context.Context,
	key session.Key,
	opts ...session.Option,
) error {
	if err := key.CheckSessionKey(); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.sessionCtx, sessCtxKey(key))
	s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	return s.db.Where("app_name = ? AND user_id = ? AND session_id = ?",
		key.AppName, key.UserID, key.SessionID).Delete(&TaskRunSession{}).Error
}

// AppendEvent implements session.Service：把有效事件追加进 child session 并落盘。
func (s *SessionService) AppendEvent(
	ctx context.Context,
	sess *session.Session,
	evt *event.Event,
	opts ...session.Option,
) error {
	if sess == nil || evt == nil {
		return nil
	}
	key := session.Key{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID}
	if err := key.CheckSessionKey(); err != nil {
		return err
	}
	// 与 inmemory 一致的事件持久化判定：仅保留有效、非 partial 的响应事件，
	// 避免把空增量/部分帧写进 transcript。
	if evt.Response == nil || evt.IsPartial || !evt.IsValidContent() {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	row, err := s.loadRow(key)
	if err != nil {
		return err
	}
	var events []event.Event
	if row.EventsJSON != "" {
		if err := json.Unmarshal([]byte(row.EventsJSON), &events); err != nil {
			return err
		}
	}
	events = append(events, *evt)
	b, err := json.Marshal(events)
	if err != nil {
		return err
	}
	row.EventsJSON = string(b)
	return s.saveRow(row)
}

// ---- 以下为 app/user/session 级 State 接口：以内存镜像兜底（transcript 不依赖）----

func appKey(appName string) string           { return appName }
func userKey2(appName, userID string) string { return appName + "\x00" + userID }
func sessCtxKey(k session.Key) string        { return k.AppName + "\x00" + k.UserID + "\x00" + k.SessionID }

// UpdateAppState implements session.Service.
func (s *SessionService) UpdateAppState(ctx context.Context, appName string, state session.StateMap) error {
	if appName == "" {
		return session.ErrAppNameRequired
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appState[appKey(appName)] = cloneState(state)
	return nil
}

// DeleteAppState implements session.Service.
func (s *SessionService) DeleteAppState(ctx context.Context, appName, key string) error {
	if appName == "" {
		return session.ErrAppNameRequired
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.appState, appKey(appName))
	return nil
}

// ListAppStates implements session.Service.
func (s *SessionService) ListAppStates(ctx context.Context, appName string) (session.StateMap, error) {
	if appName == "" {
		return nil, session.ErrAppNameRequired
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneState(s.appState[appKey(appName)]), nil
}

// UpdateUserState implements session.Service.
func (s *SessionService) UpdateUserState(ctx context.Context, userKey session.UserKey, state session.StateMap) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.userState[userKey2(userKey.AppName, userKey.UserID)] = cloneState(state)
	return nil
}

// ListUserStates implements session.Service.
func (s *SessionService) ListUserStates(ctx context.Context, userKey session.UserKey) (session.StateMap, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneState(s.userState[userKey2(userKey.AppName, userKey.UserID)]), nil
}

// DeleteUserState implements session.Service.
func (s *SessionService) DeleteUserState(ctx context.Context, userKey session.UserKey, key string) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.userState, userKey2(userKey.AppName, userKey.UserID))
	return nil
}

// UpdateSessionState implements session.Service.
func (s *SessionService) UpdateSessionState(ctx context.Context, key session.Key, state session.StateMap) error {
	if err := key.CheckSessionKey(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionCtx[sessCtxKey(key)] = cloneState(state)
	return nil
}

// CreateSessionSummary implements session.Service（本实现不生成摘要，安全 no-op）。
func (s *SessionService) CreateSessionSummary(ctx context.Context, sess *session.Session, filterKey string, force bool) error {
	return nil
}

// EnqueueSummaryJob implements session.Service（no-op）。
func (s *SessionService) EnqueueSummaryJob(ctx context.Context, sess *session.Session, filterKey string, force bool) error {
	return nil
}

// GetSessionSummaryText implements session.Service（无摘要）。
func (s *SessionService) GetSessionSummaryText(ctx context.Context, sess *session.Session, opts ...session.SummaryOption) (string, bool) {
	return "", false
}

// Close implements session.Service.
func (s *SessionService) Close() error { return nil }

func cloneState(in session.StateMap) session.StateMap {
	if len(in) == 0 {
		return nil
	}
	out := make(session.StateMap, len(in))
	for k, v := range in {
		if v == nil {
			out[k] = nil
			continue
		}
		cp := make([]byte, len(v))
		copy(cp, v)
		out[k] = cp
	}
	return out
}

// compile-time 接口断言。
var _ session.Service = (*SessionService)(nil)
