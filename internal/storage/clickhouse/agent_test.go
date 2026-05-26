package clickhouse

import (
	"context"
	"strings"
	"sync"
	"testing"

	"kube-insight/internal/agent"
)

func TestClickHouseAgentStorePersistsSessionsRunsAndEvents(t *testing.T) {
	client := newFakeAgentClickHouseClient()
	store := &Store{Client: client, Database: "ki"}

	session, err := store.CreateSession(context.Background(), agent.CreateSessionInput{Title: "Investigate API", Provider: "openai-compatible", Model: "mimo"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateRun(context.Background(), session.ID, agent.CreateRunInput{Input: "is api healthy", Metadata: []byte(`{"source":"test"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendRunEvent(context.Background(), run.ID, agent.AppendEventInput{Type: agent.EventMessageCreated, Data: []byte(`{"role":"user"}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendRunEvent(context.Background(), run.ID, agent.AppendEventInput{Type: agent.EventFinalAnswer, Data: []byte(`{"content":"ok"}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateRunStatus(context.Background(), run.ID, agent.RunCompleted, ""); err != nil {
		t.Fatal(err)
	}

	reopened := &Store{Client: client, Database: "ki"}
	loadedSession, err := reopened.GetSession(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loadedSession.ID != session.ID || len(loadedSession.Runs) != 1 || loadedSession.Runs[0].Status != agent.RunCompleted || loadedSession.Runs[0].FinalAnswer != "ok" {
		t.Fatalf("loaded session = %#v", loadedSession)
	}
	events, err := reopened.ListRunEvents(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Sequence != 1 || events[1].Sequence != 2 || string(events[1].Data) != `{"content":"ok"}` {
		t.Fatalf("events = %#v", events)
	}
	runs, err := reopened.ListRuns(context.Background(), agent.ListRunsOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if runs.Summary.Total != 1 || runs.Summary.Completed != 1 || len(runs.Runs) != 1 {
		t.Fatalf("runs = %#v", runs)
	}
	sessions, err := reopened.ListSessions(context.Background(), agent.ListSessionsOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions.Sessions) != 1 || sessions.Sessions[0].ID != session.ID || len(sessions.Sessions[0].Runs) != 0 || sessions.Sessions[0].RunCount != 1 || sessions.Sessions[0].LatestRun == nil || sessions.Sessions[0].LatestRun.ID != run.ID || sessions.Sessions[0].LatestRun.FinalAnswer != "" {
		t.Fatalf("sessions = %#v", sessions)
	}
	if client.applyCalls != 2 {
		t.Fatalf("schema should be ensured once per store instance, got %d", client.applyCalls)
	}
}

func TestClickHouseAgentStoreDeleteSessionPlansMutations(t *testing.T) {
	client := newFakeAgentClickHouseClient()
	store := &Store{Client: client, Database: "ki"}
	ctx := context.Background()
	session, err := store.CreateSession(ctx, agent.CreateSessionInput{Title: "delete"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRun(ctx, session.ID, agent.CreateRunInput{Input: "test"}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteSession(ctx, session.ID); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(client.appliedStatements, "\n")
	for _, want := range []string{
		"ALTER TABLE `ki`.agent_run_events DELETE WHERE run_id IN",
		"ALTER TABLE `ki`.agent_runs DELETE WHERE session_id = '" + session.ID + "'",
		"ALTER TABLE `ki`.agent_sessions DELETE WHERE id = '" + session.ID + "'",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing mutation %q in:\n%s", want, joined)
		}
	}
}

func TestClickHouseAgentRetentionPlansDeleteMutations(t *testing.T) {
	client := newFakeAgentClickHouseClient()
	store := &Store{Client: client, Database: "ki"}
	ctx := context.Background()
	session, err := store.CreateSession(ctx, agent.CreateSessionInput{Title: "retry"})
	if err != nil {
		t.Fatal(err)
	}
	run1, err := store.CreateRun(ctx, session.ID, agent.CreateRunInput{Input: "first"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateRunStatus(ctx, run1.ID, agent.RunCompleted, ""); err != nil {
		t.Fatal(err)
	}
	run2, err := store.CreateRun(ctx, session.ID, agent.CreateRunInput{Input: "retry", Metadata: []byte(`{"retryOfRunId":"` + run1.ID + `"}`)})
	if err != nil {
		t.Fatal(err)
	}
	keepArtifact := "artifact_keep"
	if _, err := store.AppendRunEvent(ctx, run2.ID, agent.AppendEventInput{Type: agent.EventArtifact, Data: []byte(`{"artifact":{"id":"` + keepArtifact + `","kind":"markdown"}}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendRunEvent(ctx, run2.ID, agent.AppendEventInput{Type: agent.EventArtifact, Data: []byte(`{"artifact":{"id":"artifact_drop","kind":"markdown"}}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendRunEvent(ctx, run2.ID, agent.AppendEventInput{Type: agent.EventCitation, Data: []byte(`{"citation":{"id":"citation_1","artifactId":"` + keepArtifact + `"}}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateRunStatus(ctx, run2.ID, agent.RunCompleted, ""); err != nil {
		t.Fatal(err)
	}

	report, err := store.ApplyAgentRetention(ctx, agent.DefaultRetentionOptions())
	if err != nil {
		t.Fatal(err)
	}
	if report.SupersededRunsDeleted != 1 || report.UnreferencedArtifactEventsDeleted != 1 {
		t.Fatalf("report = %#v", report)
	}
	joined := strings.Join(client.appliedStatements, "\n")
	for _, want := range []string{"ALTER TABLE `ki`.agent_run_events DELETE WHERE id IN", "ALTER TABLE `ki`.agent_run_events DELETE WHERE run_id IN", "ALTER TABLE `ki`.agent_runs DELETE WHERE id IN"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing mutation %q in:\n%s", want, joined)
		}
	}
}

type fakeAgentClickHouseClient struct {
	mu                sync.Mutex
	applyCalls        int
	appliedStatements []string
	sessions          map[string]map[string]any
	runs              map[string]map[string]any
	eventsByRunID     map[string][]map[string]any
}

func newFakeAgentClickHouseClient() *fakeAgentClickHouseClient {
	return &fakeAgentClickHouseClient{sessions: map[string]map[string]any{}, runs: map[string]map[string]any{}, eventsByRunID: map[string][]map[string]any{}}
}

func (c *fakeAgentClickHouseClient) ApplySchema(_ context.Context, statements []string) (ApplyResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.applyCalls++
	c.appliedStatements = append(c.appliedStatements, statements...)
	return ApplyResult{Applied: len(statements)}, nil
}

func (c *fakeAgentClickHouseClient) InsertRows(_ context.Context, _ string, table string, rows []map[string]any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, row := range rows {
		copyRow := cloneMap(row)
		switch table {
		case "agent_sessions":
			c.sessions[stringValue(row["id"])] = copyRow
		case "agent_runs":
			c.runs[stringValue(row["id"])] = copyRow
		case "agent_run_events":
			runID := stringValue(row["run_id"])
			c.eventsByRunID[runID] = append(c.eventsByRunID[runID], copyRow)
		}
	}
	return nil
}

func (c *fakeAgentClickHouseClient) QueryJSON(_ context.Context, query string) (QueryResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case strings.Contains(query, "FROM `ki`.agent_sessions FINAL") && strings.Contains(query, "WHERE id = "):
		id := quotedValueAfter(query, "WHERE id = ")
		if row := c.sessions[id]; row != nil {
			return QueryResult{Data: []map[string]any{cloneMap(row)}, Rows: 1}, nil
		}
		return QueryResult{}, nil
	case strings.Contains(query, "FROM `ki`.agent_sessions FINAL"):
		rows := []map[string]any{}
		for _, row := range c.sessions {
			rows = append(rows, cloneMap(row))
		}
		return QueryResult{Data: rows, Rows: len(rows)}, nil
	case strings.Contains(query, "FROM `ki`.agent_runs FINAL") && strings.Contains(query, "WHERE id = "):
		id := quotedValueAfter(query, "WHERE id = ")
		if row := c.runs[id]; row != nil {
			return QueryResult{Data: []map[string]any{cloneMap(row)}, Rows: 1}, nil
		}
		return QueryResult{}, nil
	case strings.Contains(query, "FROM `ki`.agent_runs FINAL") && strings.Contains(query, "WHERE session_id = "):
		sessionID := quotedValueAfter(query, "WHERE session_id = ")
		rows := []map[string]any{}
		for _, row := range c.runs {
			if stringValue(row["session_id"]) == sessionID {
				rows = append(rows, cloneMap(row))
			}
		}
		return QueryResult{Data: rows, Rows: len(rows)}, nil
	case strings.Contains(query, "FROM `ki`.agent_runs FINAL") && strings.Contains(query, "GROUP BY session_id"):
		counts := map[string]int{}
		for _, row := range c.runs {
			counts[stringValue(row["session_id"])]++
		}
		rows := []map[string]any{}
		for sessionID, count := range counts {
			rows = append(rows, map[string]any{"session_id": sessionID, "count": count})
		}
		return QueryResult{Data: rows, Rows: len(rows)}, nil
	case strings.Contains(query, "FROM `ki`.agent_runs FINAL") && strings.Contains(query, "GROUP BY status"):
		counts := map[string]int{}
		for _, row := range c.runs {
			counts[stringValue(row["status"])]++
		}
		rows := []map[string]any{}
		for status, count := range counts {
			rows = append(rows, map[string]any{"status": status, "count": count})
		}
		return QueryResult{Data: rows, Rows: len(rows)}, nil
	case strings.Contains(query, "FROM `ki`.agent_runs FINAL") && strings.Contains(query, "LIMIT 1 BY session_id"):
		latestBySessionID := map[string]map[string]any{}
		for _, row := range c.runs {
			sessionID := stringValue(row["session_id"])
			latest := latestBySessionID[sessionID]
			if latest == nil || runRowNewer(row, latest) {
				latestBySessionID[sessionID] = row
			}
		}
		rows := []map[string]any{}
		for _, row := range latestBySessionID {
			rows = append(rows, cloneMap(row))
		}
		return QueryResult{Data: rows, Rows: len(rows)}, nil
	case strings.Contains(query, "FROM `ki`.agent_runs FINAL"):
		rows := []map[string]any{}
		for _, row := range c.runs {
			rows = append(rows, cloneMap(row))
		}
		return QueryResult{Data: rows, Rows: len(rows)}, nil
	case strings.Contains(query, "max(sequence)"):
		runID := quotedValueAfter(query, "WHERE run_id = ")
		return QueryResult{Data: []map[string]any{{"sequence": len(c.eventsByRunID[runID]) + 1}}, Rows: 1}, nil
	case strings.Contains(query, "FROM `ki`.agent_run_events") && strings.Contains(query, "run_id IN"):
		rows := []map[string]any{}
		for _, runEvents := range c.eventsByRunID {
			for _, row := range runEvents {
				eventType := stringValue(row["type"])
				if eventType == string(agent.EventFinalAnswer) || eventType == string(agent.EventRunCompleted) {
					rows = append(rows, cloneMap(row))
				}
			}
		}
		return QueryResult{Data: rows, Rows: len(rows)}, nil
	case strings.Contains(query, "FROM `ki`.agent_run_events"):
		runID := quotedValueAfter(query, "WHERE run_id = ")
		rows := []map[string]any{}
		for _, row := range c.eventsByRunID[runID] {
			rows = append(rows, cloneMap(row))
		}
		return QueryResult{Data: rows, Rows: len(rows)}, nil
	default:
		return QueryResult{}, nil
	}
}

func (c *fakeAgentClickHouseClient) QueryTSV(context.Context, string) (TSVResult, error) {
	return TSVResult{}, nil
}

func runRowNewer(a, b map[string]any) bool {
	aCreatedAt := stringValue(a["created_at"])
	bCreatedAt := stringValue(b["created_at"])
	if aCreatedAt == bCreatedAt {
		return stringValue(a["id"]) > stringValue(b["id"])
	}
	return aCreatedAt > bCreatedAt
}

func (c *fakeAgentClickHouseClient) InsertEvidenceBatch(context.Context, EvidenceBatch) (InsertResult, error) {
	return InsertResult{}, nil
}

func cloneMap(row map[string]any) map[string]any {
	out := make(map[string]any, len(row))
	for key, value := range row {
		out[key] = value
	}
	return out
}

func quotedValueAfter(query, marker string) string {
	idx := strings.Index(query, marker)
	if idx < 0 {
		return ""
	}
	value := strings.TrimSpace(query[idx+len(marker):])
	if !strings.HasPrefix(value, "'") {
		return ""
	}
	value = strings.TrimPrefix(value, "'")
	end := strings.Index(value, "'")
	if end < 0 {
		return ""
	}
	return strings.ReplaceAll(value[:end], "''", "'")
}
