package clickhouse

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"kube-insight/internal/agent"
)

var _ agent.Store = (*Store)(nil)

func (s *Store) CreateSession(ctx context.Context, input agent.CreateSessionInput) (agent.Session, error) {
	if err := s.ensureAgentSchema(ctx); err != nil {
		return agent.Session{}, err
	}
	now := time.Now().UTC()
	session := agent.Session{
		ID:        agent.NewSessionID(),
		Title:     input.Title,
		Provider:  input.Provider,
		Model:     input.Model,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.client().InsertRows(ctx, s.database(), "agent_sessions", []map[string]any{agentSessionRow(session)}); err != nil {
		return agent.Session{}, err
	}
	return session, nil
}

func (s *Store) GetSession(ctx context.Context, id string) (agent.Session, error) {
	if err := s.ensureAgentSchema(ctx); err != nil {
		return agent.Session{}, err
	}
	result, err := s.client().QueryJSON(ctx, fmt.Sprintf(`
SELECT id, title, provider, model, created_at, updated_at
FROM %s.agent_sessions FINAL
WHERE id = %s
LIMIT 1`, q(s.database()), quoteString(id)))
	if err != nil {
		return agent.Session{}, err
	}
	if len(result.Data) == 0 {
		return agent.Session{}, agent.ErrSessionNotFound
	}
	session := agentSessionFromRow(result.Data[0])
	runs, err := s.sessionRuns(ctx, id)
	if err != nil {
		return agent.Session{}, err
	}
	session.Runs = runs
	return session, nil
}

func (s *Store) ListSessions(ctx context.Context, opts agent.ListSessionsOptions) (agent.SessionList, error) {
	if err := s.ensureAgentSchema(ctx); err != nil {
		return agent.SessionList{}, err
	}
	limit := ""
	if opts.Limit > 0 {
		limit = fmt.Sprintf("\nLIMIT %d", opts.Limit)
	}
	result, err := s.client().QueryJSON(ctx, fmt.Sprintf(`
SELECT id, title, provider, model, created_at, updated_at
FROM %s.agent_sessions FINAL
ORDER BY updated_at DESC, id DESC%s`, q(s.database()), limit))
	if err != nil {
		return agent.SessionList{}, err
	}
	sessions := make([]agent.Session, 0, len(result.Data))
	for _, row := range result.Data {
		session := agentSessionFromRow(row)
		runs, err := s.sessionRuns(ctx, session.ID)
		if err != nil {
			return agent.SessionList{}, err
		}
		session.Runs = runs
		sessions = append(sessions, session)
	}
	return agent.SessionList{Sessions: sessions}, nil
}

func (s *Store) CreateRun(ctx context.Context, sessionID string, input agent.CreateRunInput) (agent.Run, error) {
	if err := s.ensureAgentSchema(ctx); err != nil {
		return agent.Run{}, err
	}
	session, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return agent.Run{}, err
	}
	now := time.Now().UTC()
	run := agent.Run{
		ID:        agent.NewRunID(),
		SessionID: sessionID,
		Status:    agent.RunQueued,
		Input:     input.Input,
		Provider:  input.Provider,
		Model:     input.Model,
		CreatedAt: now,
		Metadata:  cloneRawMessage(input.Metadata),
	}
	if err := s.client().InsertRows(ctx, s.database(), "agent_runs", []map[string]any{agentRunRow(run, now)}); err != nil {
		return agent.Run{}, err
	}
	session.UpdatedAt = now
	if err := s.client().InsertRows(ctx, s.database(), "agent_sessions", []map[string]any{agentSessionRow(session)}); err != nil {
		return agent.Run{}, err
	}
	return run, nil
}

func (s *Store) GetRun(ctx context.Context, id string) (agent.Run, error) {
	if err := s.ensureAgentSchema(ctx); err != nil {
		return agent.Run{}, err
	}
	result, err := s.client().QueryJSON(ctx, fmt.Sprintf(`
SELECT id, session_id, status, input, provider, model, created_at, started_at, completed_at, error, metadata
FROM %s.agent_runs FINAL
WHERE id = %s
LIMIT 1`, q(s.database()), quoteString(id)))
	if err != nil {
		return agent.Run{}, err
	}
	if len(result.Data) == 0 {
		return agent.Run{}, agent.ErrRunNotFound
	}
	return agentRunFromRow(result.Data[0]), nil
}

func (s *Store) ListRuns(ctx context.Context, opts agent.ListRunsOptions) (agent.RunList, error) {
	if err := s.ensureAgentSchema(ctx); err != nil {
		return agent.RunList{}, err
	}
	summary, err := s.runSummary(ctx)
	if err != nil {
		return agent.RunList{}, err
	}
	where := ""
	if opts.Status != "" {
		where = "WHERE status = " + quoteString(string(opts.Status))
	}
	limit := ""
	if opts.Limit > 0 {
		limit = fmt.Sprintf("\nLIMIT %d", opts.Limit)
	}
	result, err := s.client().QueryJSON(ctx, fmt.Sprintf(`
SELECT id, session_id, status, input, provider, model, created_at, started_at, completed_at, error, metadata
FROM %s.agent_runs FINAL
%s
ORDER BY created_at DESC, id DESC%s`, q(s.database()), where, limit))
	if err != nil {
		return agent.RunList{}, err
	}
	runs := make([]agent.Run, 0, len(result.Data))
	for _, row := range result.Data {
		runs = append(runs, agentRunFromRow(row))
	}
	return agent.RunList{Summary: summary, Runs: runs}, nil
}

func (s *Store) UpdateRunStatus(ctx context.Context, id string, status agent.RunStatus, message string) (agent.Run, error) {
	if err := s.ensureAgentSchema(ctx); err != nil {
		return agent.Run{}, err
	}
	run, err := s.GetRun(ctx, id)
	if err != nil {
		return agent.Run{}, err
	}
	now := time.Now().UTC()
	run.Status = status
	run.Error = message
	if status == agent.RunRunning && run.StartedAt == nil {
		startedAt := now
		run.StartedAt = &startedAt
	}
	if agentStatusTerminal(status) && run.CompletedAt == nil {
		completedAt := now
		run.CompletedAt = &completedAt
	}
	if err := s.client().InsertRows(ctx, s.database(), "agent_runs", []map[string]any{agentRunRow(run, now)}); err != nil {
		return agent.Run{}, err
	}
	if session, err := s.GetSession(ctx, run.SessionID); err == nil {
		session.UpdatedAt = now
		if err := s.client().InsertRows(ctx, s.database(), "agent_sessions", []map[string]any{agentSessionRow(session)}); err != nil {
			return agent.Run{}, err
		}
	}
	return run, nil
}

func (s *Store) AppendRunEvent(ctx context.Context, runID string, input agent.AppendEventInput) (agent.RunEvent, error) {
	if err := s.ensureAgentSchema(ctx); err != nil {
		return agent.RunEvent{}, err
	}
	if _, err := s.GetRun(ctx, runID); err != nil {
		return agent.RunEvent{}, err
	}
	sequence, err := s.nextRunEventSequence(ctx, runID)
	if err != nil {
		return agent.RunEvent{}, err
	}
	event := agent.RunEvent{
		ID:        agent.NewEventID(),
		RunID:     runID,
		Sequence:  sequence,
		Type:      input.Type,
		CreatedAt: time.Now().UTC(),
		Data:      cloneRawMessage(input.Data),
	}
	if err := s.client().InsertRows(ctx, s.database(), "agent_run_events", []map[string]any{agentRunEventRow(event)}); err != nil {
		return agent.RunEvent{}, err
	}
	return event, nil
}

func (s *Store) ListRunEvents(ctx context.Context, runID string) ([]agent.RunEvent, error) {
	if err := s.ensureAgentSchema(ctx); err != nil {
		return nil, err
	}
	if _, err := s.GetRun(ctx, runID); err != nil {
		return nil, err
	}
	result, err := s.client().QueryJSON(ctx, fmt.Sprintf(`
SELECT id, run_id, sequence, type, created_at, data
FROM %s.agent_run_events
WHERE run_id = %s
ORDER BY sequence ASC, id ASC`, q(s.database()), quoteString(runID)))
	if err != nil {
		return nil, err
	}
	events := make([]agent.RunEvent, 0, len(result.Data))
	for _, row := range result.Data {
		events = append(events, agentRunEventFromRow(row))
	}
	return events, nil
}

func (s *Store) ensureAgentSchema(ctx context.Context) error {
	s.agentSchemaMu.Lock()
	defer s.agentSchemaMu.Unlock()
	if s.agentSchemaReady {
		return nil
	}
	if _, err := s.client().ApplySchema(ctx, CreateAgentTableStatements(SchemaOptions{Database: s.database()})); err != nil {
		return err
	}
	s.agentSchemaReady = true
	return nil
}

func (s *Store) sessionRuns(ctx context.Context, sessionID string) ([]agent.Run, error) {
	result, err := s.client().QueryJSON(ctx, fmt.Sprintf(`
SELECT id, session_id, status, input, provider, model, created_at, started_at, completed_at, error, metadata
FROM %s.agent_runs FINAL
WHERE session_id = %s
ORDER BY created_at ASC, id ASC`, q(s.database()), quoteString(sessionID)))
	if err != nil {
		return nil, err
	}
	runs := make([]agent.Run, 0, len(result.Data))
	for _, row := range result.Data {
		runs = append(runs, agentRunFromRow(row))
	}
	return runs, nil
}

func (s *Store) runSummary(ctx context.Context) (agent.RunSummary, error) {
	result, err := s.client().QueryJSON(ctx, fmt.Sprintf(`
SELECT status, count() AS count
FROM %s.agent_runs FINAL
GROUP BY status`, q(s.database())))
	if err != nil {
		return agent.RunSummary{}, err
	}
	summary := agent.RunSummary{}
	for _, row := range result.Data {
		count := int(int64Value(row["count"]))
		summary.Total += count
		switch agent.RunStatus(stringValue(row["status"])) {
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
	return summary, nil
}

func (s *Store) nextRunEventSequence(ctx context.Context, runID string) (int64, error) {
	result, err := s.client().QueryJSON(ctx, fmt.Sprintf(`
SELECT coalesce(max(sequence), 0) + 1 AS sequence
FROM %s.agent_run_events
WHERE run_id = %s`, q(s.database()), quoteString(runID)))
	if err != nil {
		return 0, err
	}
	if len(result.Data) == 0 {
		return 1, nil
	}
	sequence := int64Value(result.Data[0]["sequence"])
	if sequence <= 0 {
		return 1, nil
	}
	return sequence, nil
}

func agentSessionRow(session agent.Session) map[string]any {
	return map[string]any{
		"id":         session.ID,
		"title":      session.Title,
		"provider":   session.Provider,
		"model":      session.Model,
		"created_at": clickHouseTime(session.CreatedAt),
		"updated_at": clickHouseTime(session.UpdatedAt),
	}
}

func agentRunRow(run agent.Run, updatedAt time.Time) map[string]any {
	return map[string]any{
		"id":           run.ID,
		"session_id":   run.SessionID,
		"status":       string(run.Status),
		"input":        run.Input,
		"provider":     run.Provider,
		"model":        run.Model,
		"created_at":   clickHouseTime(run.CreatedAt),
		"started_at":   nullableClickHouseTime(run.StartedAt),
		"completed_at": nullableClickHouseTime(run.CompletedAt),
		"error":        run.Error,
		"metadata":     rawMessageString(run.Metadata),
		"updated_at":   clickHouseTime(updatedAt),
	}
}

func agentRunEventRow(event agent.RunEvent) map[string]any {
	return map[string]any{
		"id":         event.ID,
		"run_id":     event.RunID,
		"sequence":   event.Sequence,
		"type":       string(event.Type),
		"created_at": clickHouseTime(event.CreatedAt),
		"data":       rawMessageString(event.Data),
	}
}

func agentSessionFromRow(row map[string]any) agent.Session {
	return agent.Session{
		ID:        stringValue(row["id"]),
		Title:     stringValue(row["title"]),
		Provider:  stringValue(row["provider"]),
		Model:     stringValue(row["model"]),
		CreatedAt: timeValue(row["created_at"]),
		UpdatedAt: timeValue(row["updated_at"]),
	}
}

func agentRunFromRow(row map[string]any) agent.Run {
	return agent.Run{
		ID:          stringValue(row["id"]),
		SessionID:   stringValue(row["session_id"]),
		Status:      agent.RunStatus(stringValue(row["status"])),
		Input:       stringValue(row["input"]),
		Provider:    stringValue(row["provider"]),
		Model:       stringValue(row["model"]),
		CreatedAt:   timeValue(row["created_at"]),
		StartedAt:   nullableTimeValue(row["started_at"]),
		CompletedAt: nullableTimeValue(row["completed_at"]),
		Error:       stringValue(row["error"]),
		Metadata:    rawMessageFromString(row["metadata"]),
	}
}

func agentRunEventFromRow(row map[string]any) agent.RunEvent {
	return agent.RunEvent{
		ID:        stringValue(row["id"]),
		RunID:     stringValue(row["run_id"]),
		Sequence:  int64Value(row["sequence"]),
		Type:      agent.RunEventType(stringValue(row["type"])),
		CreatedAt: timeValue(row["created_at"]),
		Data:      rawMessageFromString(row["data"]),
	}
}

func nullableClickHouseTime(value *time.Time) any {
	if value == nil || value.IsZero() {
		return nil
	}
	return clickHouseTime(*value)
}

func nullableTimeValue(value any) *time.Time {
	if value == nil {
		return nil
	}
	t := timeValue(value)
	if t.IsZero() {
		return nil
	}
	return &t
}

func rawMessageString(value json.RawMessage) string {
	if len(value) == 0 {
		return ""
	}
	return string(value)
}

func rawMessageFromString(value any) json.RawMessage {
	text := stringValue(value)
	if text == "" {
		return nil
	}
	return cloneRawMessage(json.RawMessage(text))
}

func cloneRawMessage(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return nil
	}
	out := make([]byte, len(value))
	copy(out, value)
	return out
}

func agentStatusTerminal(status agent.RunStatus) bool {
	switch status {
	case agent.RunCompleted, agent.RunFailed, agent.RunCancelled:
		return true
	default:
		return false
	}
}
