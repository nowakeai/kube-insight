package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

var (
	ErrSessionNotFound = errors.New("agent session not found")
	ErrRunNotFound     = errors.New("agent run not found")
)

type Store interface {
	CreateSession(context.Context, CreateSessionInput) (Session, error)
	GetSession(context.Context, string) (Session, error)
	ListSessions(context.Context, ListSessionsOptions) (SessionList, error)
	DeleteSession(context.Context, string) error
	CreateRun(context.Context, string, CreateRunInput) (Run, error)
	GetRun(context.Context, string) (Run, error)
	ListRuns(context.Context, ListRunsOptions) (RunList, error)
	UpdateRunStatus(context.Context, string, RunStatus, string) (Run, error)
	AppendRunEvent(context.Context, string, AppendEventInput) (RunEvent, error)
	ListRunEvents(context.Context, string) ([]RunEvent, error)
}

type MemoryStore struct {
	mu       sync.Mutex
	now      func() time.Time
	sessions map[string]Session
	runs     map[string]Run
	events   map[string][]RunEvent
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		now:      time.Now,
		sessions: map[string]Session{},
		runs:     map[string]Run{},
		events:   map[string][]RunEvent{},
	}
}

func (s *MemoryStore) CreateSession(ctx context.Context, input CreateSessionInput) (Session, error) {
	if err := ctx.Err(); err != nil {
		return Session{}, err
	}
	now := s.now().UTC()
	session := Session{
		ID:        NewSessionID(),
		Title:     input.Title,
		Provider:  input.Provider,
		Model:     input.Model,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[session.ID] = session
	return cloneSession(session), nil
}

func (s *MemoryStore) GetSession(ctx context.Context, id string) (Session, error) {
	if err := ctx.Err(); err != nil {
		return Session{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[id]
	if !ok {
		return Session{}, ErrSessionNotFound
	}
	session.Runs = s.sessionRunsLocked(id)
	return cloneSession(session), nil
}

func (s *MemoryStore) ListSessions(ctx context.Context, opts ListSessionsOptions) (SessionList, error) {
	if err := ctx.Err(); err != nil {
		return SessionList{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sessions := make([]Session, 0, len(s.sessions))
	for _, session := range s.sessions {
		runs := s.sessionRunsLocked(session.ID)
		session.RunCount = len(runs)
		if len(runs) > 0 {
			latestRun := runs[len(runs)-1]
			latestRun.FinalAnswer = ""
			session.LatestRun = cloneRunPtr(latestRun)
		}
		sessions = append(sessions, session)
	}
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].UpdatedAt.Equal(sessions[j].UpdatedAt) {
			return sessions[i].ID > sessions[j].ID
		}
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	if opts.Limit > 0 && len(sessions) > opts.Limit {
		sessions = sessions[:opts.Limit]
	}
	for i := range sessions {
		sessions[i] = cloneSession(sessions[i])
	}
	return SessionList{Sessions: sessions}, nil
}

func (s *MemoryStore) DeleteSession(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[id]; !ok {
		return ErrSessionNotFound
	}
	delete(s.sessions, id)
	for runID, run := range s.runs {
		if run.SessionID != id {
			continue
		}
		delete(s.runs, runID)
		delete(s.events, runID)
	}
	return nil
}

func (s *MemoryStore) sessionRunsLocked(sessionID string) []Run {
	runs := make([]Run, 0)
	for _, run := range s.runs {
		if run.SessionID == sessionID {
			run.FinalAnswer = s.finalAnswerForRunLocked(run.ID)
			runs = append(runs, run)
		}
	}
	sortRunsOldestFirst(runs)
	return runs
}

func (s *MemoryStore) finalAnswerForRunLocked(runID string) string {
	finalAnswer := ""
	for _, event := range s.events[runID] {
		if answer := FinalAnswerFromRunEvent(event); answer != "" {
			finalAnswer = answer
		}
	}
	return finalAnswer
}

func (s *MemoryStore) CreateRun(ctx context.Context, sessionID string, input CreateRunInput) (Run, error) {
	if err := ctx.Err(); err != nil {
		return Run{}, err
	}
	now := s.now().UTC()
	run := Run{
		ID:        NewRunID(),
		SessionID: sessionID,
		Status:    RunQueued,
		Input:     input.Input,
		Provider:  input.Provider,
		Model:     input.Model,
		CreatedAt: now,
		Metadata:  cloneRaw(input.Metadata),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[sessionID]
	if !ok {
		return Run{}, ErrSessionNotFound
	}
	session.UpdatedAt = now
	s.sessions[sessionID] = session
	s.runs[run.ID] = run
	return cloneRun(run), nil
}

func (s *MemoryStore) GetRun(ctx context.Context, id string) (Run, error) {
	if err := ctx.Err(); err != nil {
		return Run{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runs[id]
	if !ok {
		return Run{}, ErrRunNotFound
	}
	return cloneRun(run), nil
}

func (s *MemoryStore) ListRuns(ctx context.Context, opts ListRunsOptions) (RunList, error) {
	if err := ctx.Err(); err != nil {
		return RunList{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	runs := make([]Run, 0, len(s.runs))
	summary := RunSummary{}
	for _, run := range s.runs {
		summarizeRunStatus(&summary, run.Status)
		if opts.Status != "" && run.Status != opts.Status {
			continue
		}
		runs = append(runs, run)
	}
	sortRunsNewestFirst(runs)
	if opts.Limit > 0 && len(runs) > opts.Limit {
		runs = runs[:opts.Limit]
	}
	return RunList{Summary: summary, Runs: cloneRuns(runs)}, nil
}

func (s *MemoryStore) UpdateRunStatus(ctx context.Context, id string, status RunStatus, message string) (Run, error) {
	if err := ctx.Err(); err != nil {
		return Run{}, err
	}
	now := s.now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runs[id]
	if !ok {
		return Run{}, ErrRunNotFound
	}
	run.Status = status
	run.Error = message
	if status == RunRunning && run.StartedAt == nil {
		run.StartedAt = &now
	}
	if statusTerminal(status) && run.CompletedAt == nil {
		run.CompletedAt = &now
	}
	s.runs[id] = run
	if session, ok := s.sessions[run.SessionID]; ok {
		session.UpdatedAt = now
		s.sessions[session.ID] = session
	}
	return cloneRun(run), nil
}

func (s *MemoryStore) AppendRunEvent(ctx context.Context, runID string, input AppendEventInput) (RunEvent, error) {
	if err := ctx.Err(); err != nil {
		return RunEvent{}, err
	}
	now := s.now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.runs[runID]; !ok {
		return RunEvent{}, ErrRunNotFound
	}
	event := RunEvent{
		ID:        NewEventID(),
		RunID:     runID,
		Sequence:  int64(len(s.events[runID]) + 1),
		Type:      input.Type,
		CreatedAt: now,
		Data:      cloneRaw(input.Data),
	}
	s.events[runID] = append(s.events[runID], event)
	return cloneEvent(event), nil
}

func (s *MemoryStore) ListRunEvents(ctx context.Context, runID string) ([]RunEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.runs[runID]; !ok {
		return nil, ErrRunNotFound
	}
	return cloneEvents(s.events[runID]), nil
}

func sortRunsOldestFirst(runs []Run) {
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].CreatedAt.Equal(runs[j].CreatedAt) {
			return runs[i].ID < runs[j].ID
		}
		return runs[i].CreatedAt.Before(runs[j].CreatedAt)
	})
}

func statusTerminal(status RunStatus) bool {
	switch status {
	case RunCompleted, RunFailed, RunCancelled:
		return true
	default:
		return false
	}
}

func newID(prefix string) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("generate %s id: %v", prefix, err))
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}

func NewSessionID() string {
	return newID("sess")
}

func NewRunID() string {
	return newID("run")
}

func NewEventID() string {
	return newID("evt")
}

func NewMessageID() string {
	return newID("msg")
}

func NewArtifactID() string {
	return newID("artifact")
}

func NewCitationID() string {
	return newID("citation")
}

func cloneSession(in Session) Session {
	in.Messages = append([]Message(nil), in.Messages...)
	in.Runs = cloneRuns(in.Runs)
	if in.LatestRun != nil {
		in.LatestRun = cloneRunPtr(*in.LatestRun)
	}
	return in
}

func cloneRun(in Run) Run {
	in.Metadata = cloneRaw(in.Metadata)
	return in
}

func cloneRunPtr(in Run) *Run {
	out := cloneRun(in)
	return &out
}

func cloneRuns(in []Run) []Run {
	out := make([]Run, len(in))
	for i := range in {
		out[i] = cloneRun(in[i])
	}
	return out
}

func cloneEvent(in RunEvent) RunEvent {
	in.Data = cloneRaw(in.Data)
	return in
}

func cloneEvents(in []RunEvent) []RunEvent {
	out := make([]RunEvent, len(in))
	for i := range in {
		out[i] = cloneEvent(in[i])
	}
	return out
}

func cloneRaw(in []byte) []byte {
	if len(in) == 0 {
		return nil
	}
	return append([]byte(nil), in...)
}

func summarizeRunStatus(summary *RunSummary, status RunStatus) {
	summary.Total++
	switch status {
	case RunQueued:
		summary.Queued++
	case RunRunning:
		summary.Running++
	case RunCompleted:
		summary.Completed++
	case RunFailed:
		summary.Failed++
	case RunCancelled:
		summary.Cancelled++
	}
}

func sortRunsNewestFirst(runs []Run) {
	sort.SliceStable(runs, func(i, j int) bool {
		if runs[i].CreatedAt.Equal(runs[j].CreatedAt) {
			return runs[i].ID > runs[j].ID
		}
		return runs[i].CreatedAt.After(runs[j].CreatedAt)
	})
}
