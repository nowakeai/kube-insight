package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
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
	CreateRun(context.Context, string, CreateRunInput) (Run, error)
	GetRun(context.Context, string) (Run, error)
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
		ID:        newID("sess"),
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
	for _, run := range s.runs {
		if run.SessionID == id {
			session.Runs = append(session.Runs, run)
		}
	}
	return cloneSession(session), nil
}

func (s *MemoryStore) CreateRun(ctx context.Context, sessionID string, input CreateRunInput) (Run, error) {
	if err := ctx.Err(); err != nil {
		return Run{}, err
	}
	now := s.now().UTC()
	run := Run{
		ID:        newID("run"),
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
		ID:        newID("evt"),
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

func cloneSession(in Session) Session {
	in.Messages = append([]Message(nil), in.Messages...)
	in.Runs = cloneRuns(in.Runs)
	return in
}

func cloneRun(in Run) Run {
	in.Metadata = cloneRaw(in.Metadata)
	return in
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
