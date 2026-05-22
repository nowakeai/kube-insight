package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"kube-insight/internal/agent"
)

func (s *Store) CreateSession(ctx context.Context, input agent.CreateSessionInput) (agent.Session, error) {
	now := time.Now().UTC()
	session := agent.Session{
		ID:        agent.NewSessionID(),
		Title:     input.Title,
		Provider:  input.Provider,
		Model:     input.Model,
		CreatedAt: now,
		UpdatedAt: now,
	}
	_, err := s.db.ExecContext(ctx, `
insert into agent_sessions(id, title, provider, model, created_at, updated_at)
values (?, ?, ?, ?, ?, ?)`, session.ID, nullable(session.Title), nullable(session.Provider), nullable(session.Model), millis(session.CreatedAt), millis(session.UpdatedAt))
	if err != nil {
		return agent.Session{}, err
	}
	return session, nil
}

func (s *Store) GetSession(ctx context.Context, id string) (agent.Session, error) {
	session, err := scanAgentSession(s.db.QueryRowContext(ctx, `
select id, title, provider, model, created_at, updated_at
from agent_sessions
where id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return agent.Session{}, agent.ErrSessionNotFound
	}
	if err != nil {
		return agent.Session{}, err
	}
	runs, err := s.sessionRuns(ctx, id)
	if err != nil {
		return agent.Session{}, err
	}
	session.Runs = runs
	return session, nil
}

func (s *Store) ListSessions(ctx context.Context, opts agent.ListSessionsOptions) (agent.SessionList, error) {
	query := `
select id, title, provider, model, created_at, updated_at
from agent_sessions
order by updated_at desc, id desc`
	args := []any{}
	if opts.Limit > 0 {
		query += `
limit ?`
		args = append(args, opts.Limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return agent.SessionList{}, err
	}
	sessions := []agent.Session{}
	for rows.Next() {
		session, err := scanAgentSession(rows)
		if err != nil {
			_ = rows.Close()
			return agent.SessionList{}, err
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return agent.SessionList{}, err
	}
	if err := rows.Close(); err != nil {
		return agent.SessionList{}, err
	}
	for i := range sessions {
		runs, err := s.sessionRuns(ctx, sessions[i].ID)
		if err != nil {
			return agent.SessionList{}, err
		}
		sessions[i].Runs = runs
	}
	return agent.SessionList{Sessions: sessions}, nil
}

func (s *Store) CreateRun(ctx context.Context, sessionID string, input agent.CreateRunInput) (agent.Run, error) {
	now := time.Now().UTC()
	run := agent.Run{
		ID:        agent.NewRunID(),
		SessionID: sessionID,
		Status:    agent.RunQueued,
		Input:     input.Input,
		Provider:  input.Provider,
		Model:     input.Model,
		CreatedAt: now,
		Metadata:  cloneBytes(input.Metadata),
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return agent.Run{}, err
	}
	defer rollback(tx)
	if ok, err := agentSessionExists(ctx, tx, sessionID); err != nil {
		return agent.Run{}, err
	} else if !ok {
		return agent.Run{}, agent.ErrSessionNotFound
	}
	_, err = tx.ExecContext(ctx, `
insert into agent_runs(id, session_id, status, input, provider, model, created_at, metadata)
values (?, ?, ?, ?, ?, ?, ?, ?)`, run.ID, run.SessionID, run.Status, run.Input, nullable(run.Provider), nullable(run.Model), millis(run.CreatedAt), nullableBytes(run.Metadata))
	if err != nil {
		return agent.Run{}, err
	}
	if _, err := tx.ExecContext(ctx, `update agent_sessions set updated_at = ? where id = ?`, millis(now), sessionID); err != nil {
		return agent.Run{}, err
	}
	if err := tx.Commit(); err != nil {
		return agent.Run{}, err
	}
	return run, nil
}

func (s *Store) GetRun(ctx context.Context, id string) (agent.Run, error) {
	run, err := s.scanRun(ctx, `
select id, session_id, status, input, provider, model, created_at, started_at, completed_at, error, metadata
from agent_runs
where id = ?`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return agent.Run{}, agent.ErrRunNotFound
	}
	return run, err
}

func (s *Store) ListRuns(ctx context.Context, opts agent.ListRunsOptions) (agent.RunList, error) {
	summary, err := s.runSummary(ctx)
	if err != nil {
		return agent.RunList{}, err
	}
	query := `
select id, session_id, status, input, provider, model, created_at, started_at, completed_at, error, metadata
from agent_runs`
	args := []any{}
	if opts.Status != "" {
		query += `
where status = ?`
		args = append(args, opts.Status)
	}
	query += `
order by created_at desc, id desc`
	if opts.Limit > 0 {
		query += `
limit ?`
		args = append(args, opts.Limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return agent.RunList{}, err
	}
	defer rows.Close()
	runs := []agent.Run{}
	for rows.Next() {
		run, err := scanAgentRun(rows)
		if err != nil {
			return agent.RunList{}, err
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return agent.RunList{}, err
	}
	return agent.RunList{Summary: summary, Runs: runs}, nil
}

func (s *Store) runSummary(ctx context.Context) (agent.RunSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
select status, count(*)
from agent_runs
group by status`)
	if err != nil {
		return agent.RunSummary{}, err
	}
	defer rows.Close()
	summary := agent.RunSummary{}
	for rows.Next() {
		var status agent.RunStatus
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return agent.RunSummary{}, err
		}
		summary.Total += count
		switch status {
		case agent.RunQueued:
			summary.Queued = count
		case agent.RunRunning:
			summary.Running = count
		case agent.RunCompleted:
			summary.Completed = count
		case agent.RunFailed:
			summary.Failed = count
		case agent.RunCancelled:
			summary.Cancelled = count
		}
	}
	return summary, rows.Err()
}

func (s *Store) UpdateRunStatus(ctx context.Context, id string, status agent.RunStatus, message string) (agent.Run, error) {
	now := time.Now().UTC()
	run, err := s.GetRun(ctx, id)
	if err != nil {
		return agent.Run{}, err
	}
	startedAt := nullTime(run.StartedAt)
	completedAt := nullTime(run.CompletedAt)
	if status == agent.RunRunning && run.StartedAt == nil {
		startedAt = sql.NullInt64{Int64: millis(now), Valid: true}
	}
	if agentStatusTerminal(status) && run.CompletedAt == nil {
		completedAt = sql.NullInt64{Int64: millis(now), Valid: true}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return agent.Run{}, err
	}
	defer rollback(tx)
	res, err := tx.ExecContext(ctx, `
update agent_runs
set status = ?, started_at = ?, completed_at = ?, error = ?
where id = ?`, status, startedAt, completedAt, nullable(message), id)
	if err != nil {
		return agent.Run{}, err
	}
	if affected, err := res.RowsAffected(); err != nil {
		return agent.Run{}, err
	} else if affected == 0 {
		return agent.Run{}, agent.ErrRunNotFound
	}
	if _, err := tx.ExecContext(ctx, `update agent_sessions set updated_at = ? where id = ?`, millis(now), run.SessionID); err != nil {
		return agent.Run{}, err
	}
	if err := tx.Commit(); err != nil {
		return agent.Run{}, err
	}
	return s.GetRun(ctx, id)
}

func (s *Store) AppendRunEvent(ctx context.Context, runID string, input agent.AppendEventInput) (agent.RunEvent, error) {
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return agent.RunEvent{}, err
	}
	defer rollback(tx)
	if ok, err := agentRunExists(ctx, tx, runID); err != nil {
		return agent.RunEvent{}, err
	} else if !ok {
		return agent.RunEvent{}, agent.ErrRunNotFound
	}
	var sequence int64
	if err := tx.QueryRowContext(ctx, `select coalesce(max(sequence), 0) + 1 from agent_run_events where run_id = ?`, runID).Scan(&sequence); err != nil {
		return agent.RunEvent{}, err
	}
	event := agent.RunEvent{
		ID:        agent.NewEventID(),
		RunID:     runID,
		Sequence:  sequence,
		Type:      input.Type,
		CreatedAt: now,
		Data:      cloneBytes(input.Data),
	}
	_, err = tx.ExecContext(ctx, `
insert into agent_run_events(id, run_id, sequence, type, created_at, data)
values (?, ?, ?, ?, ?, ?)`, event.ID, event.RunID, event.Sequence, event.Type, millis(event.CreatedAt), nullableBytes(event.Data))
	if err != nil {
		return agent.RunEvent{}, err
	}
	if err := tx.Commit(); err != nil {
		return agent.RunEvent{}, err
	}
	return event, nil
}

func (s *Store) ListRunEvents(ctx context.Context, runID string) ([]agent.RunEvent, error) {
	if ok, err := s.runExists(ctx, runID); err != nil {
		return nil, err
	} else if !ok {
		return nil, agent.ErrRunNotFound
	}
	rows, err := s.db.QueryContext(ctx, `
select id, run_id, sequence, type, created_at, data
from agent_run_events
where run_id = ?
order by sequence`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []agent.RunEvent
	for rows.Next() {
		event, err := scanAgentRunEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) sessionRuns(ctx context.Context, sessionID string) ([]agent.Run, error) {
	rows, err := s.db.QueryContext(ctx, `
select id, session_id, status, input, provider, model, created_at, started_at, completed_at, error, metadata
from agent_runs
where session_id = ?
order by created_at`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runs []agent.Run
	for rows.Next() {
		run, err := scanAgentRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (s *Store) scanRun(ctx context.Context, query string, args ...any) (agent.Run, error) {
	return scanAgentRun(s.db.QueryRowContext(ctx, query, args...))
}

type scanner interface {
	Scan(dest ...any) error
}

func scanAgentSession(row scanner) (agent.Session, error) {
	var session agent.Session
	var createdAt, updatedAt int64
	var title, provider, model sql.NullString
	if err := row.Scan(&session.ID, &title, &provider, &model, &createdAt, &updatedAt); err != nil {
		return agent.Session{}, err
	}
	session.Title = title.String
	session.Provider = provider.String
	session.Model = model.String
	session.CreatedAt = time.UnixMilli(createdAt).UTC()
	session.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return session, nil
}

func scanAgentRun(row scanner) (agent.Run, error) {
	var run agent.Run
	var status string
	var provider, model, message sql.NullString
	var createdAt int64
	var startedAt, completedAt sql.NullInt64
	var metadata []byte
	if err := row.Scan(&run.ID, &run.SessionID, &status, &run.Input, &provider, &model, &createdAt, &startedAt, &completedAt, &message, &metadata); err != nil {
		return agent.Run{}, err
	}
	run.Status = agent.RunStatus(status)
	run.Provider = provider.String
	run.Model = model.String
	run.CreatedAt = time.UnixMilli(createdAt).UTC()
	run.StartedAt = nullableMillisTime(startedAt)
	run.CompletedAt = nullableMillisTime(completedAt)
	run.Error = message.String
	run.Metadata = cloneBytes(metadata)
	return run, nil
}

func scanAgentRunEvent(row scanner) (agent.RunEvent, error) {
	var event agent.RunEvent
	var eventType string
	var createdAt int64
	var data []byte
	if err := row.Scan(&event.ID, &event.RunID, &event.Sequence, &eventType, &createdAt, &data); err != nil {
		return agent.RunEvent{}, err
	}
	event.Type = agent.RunEventType(eventType)
	event.CreatedAt = time.UnixMilli(createdAt).UTC()
	event.Data = cloneBytes(data)
	return event, nil
}

func (s *Store) runExists(ctx context.Context, runID string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `select exists(select 1 from agent_runs where id = ?)`, runID).Scan(&exists)
	return exists, err
}

func agentSessionExists(ctx context.Context, tx *sql.Tx, sessionID string) (bool, error) {
	var exists bool
	err := tx.QueryRowContext(ctx, `select exists(select 1 from agent_sessions where id = ?)`, sessionID).Scan(&exists)
	return exists, err
}

func agentRunExists(ctx context.Context, tx *sql.Tx, runID string) (bool, error) {
	var exists bool
	err := tx.QueryRowContext(ctx, `select exists(select 1 from agent_runs where id = ?)`, runID).Scan(&exists)
	return exists, err
}

func agentStatusTerminal(status agent.RunStatus) bool {
	switch status {
	case agent.RunCompleted, agent.RunFailed, agent.RunCancelled:
		return true
	default:
		return false
	}
}

func nullTime(value *time.Time) sql.NullInt64 {
	if value == nil || value.IsZero() {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: millis(value.UTC()), Valid: true}
}

func nullableMillisTime(value sql.NullInt64) *time.Time {
	if !value.Valid || value.Int64 <= 0 {
		return nil
	}
	t := time.UnixMilli(value.Int64).UTC()
	return &t
}

func nullableBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func cloneBytes(value []byte) []byte {
	if len(value) == 0 {
		return nil
	}
	return append([]byte(nil), value...)
}
