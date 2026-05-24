package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"kube-insight/internal/agent"
)

type createAgentSessionRequest struct {
	Title    string `json:"title"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

type createAgentRunRequest struct {
	Input    string          `json:"input"`
	Provider string          `json:"provider"`
	Model    string          `json:"model"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

func (s *Server) handleCreateAgentSession(w http.ResponseWriter, r *http.Request) {
	var input createAgentSessionRequest
	if err := decodeOptionalJSON(r.Body, &input); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid json body: %w", err))
		return
	}
	session, err := s.agentStore.CreateSession(r.Context(), agent.CreateSessionInput{
		Title:    input.Title,
		Provider: input.Provider,
		Model:    input.Model,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, session)
}

func (s *Server) handleListAgentSessions(w http.ResponseWriter, r *http.Request) {
	opts, err := parseListAgentSessionsOptions(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	sessions, err := s.agentStore.ListSessions(r.Context(), opts)
	if err != nil {
		writeAgentStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sessions)
}

func parseListAgentSessionsOptions(r *http.Request) (agent.ListSessionsOptions, error) {
	query := r.URL.Query()
	opts := agent.ListSessionsOptions{Limit: 50}
	if rawLimit := query.Get("limit"); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil || limit < 0 {
			return agent.ListSessionsOptions{}, fmt.Errorf("limit must be a non-negative integer")
		}
		opts.Limit = limit
	}
	if opts.Limit > 200 {
		opts.Limit = 200
	}
	return opts, nil
}

func (s *Server) handleGetAgentSession(w http.ResponseWriter, r *http.Request) {
	session, err := s.agentStore.GetSession(r.Context(), r.PathValue("session_id"))
	if err != nil {
		writeAgentStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) handleCreateAgentRun(w http.ResponseWriter, r *http.Request) {
	var input createAgentRunRequest
	if err := decodeOptionalJSON(r.Body, &input); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid json body: %w", err))
		return
	}
	if input.Input == "" {
		writeError(w, http.StatusBadRequest, errors.New("input is required"))
		return
	}
	run, err := s.agentStore.CreateRun(r.Context(), r.PathValue("session_id"), agent.CreateRunInput{
		Input:    input.Input,
		Provider: input.Provider,
		Model:    input.Model,
		Metadata: input.Metadata,
	})
	if err != nil {
		writeAgentStoreError(w, err)
		return
	}
	if err := s.recordRunCreated(r.Context(), run); err != nil {
		writeAgentStoreError(w, err)
		return
	}
	s.startAgentRun(run)
	writeJSON(w, http.StatusCreated, run)
}

func (s *Server) handleRetryAgentRun(w http.ResponseWriter, r *http.Request) {
	original, err := s.agentStore.GetRun(r.Context(), r.PathValue("run_id"))
	if err != nil {
		writeAgentStoreError(w, err)
		return
	}
	if original.Status == agent.RunQueued || original.Status == agent.RunRunning {
		writeError(w, http.StatusConflict, errors.New("only terminal agent runs can be retried"))
		return
	}
	run, err := s.agentStore.CreateRun(r.Context(), original.SessionID, agent.CreateRunInput{
		Input:    original.Input,
		Provider: original.Provider,
		Model:    original.Model,
		Metadata: retryAgentRunMetadata(original),
	})
	if err != nil {
		writeAgentStoreError(w, err)
		return
	}
	if err := s.recordRunCreated(r.Context(), run); err != nil {
		writeAgentStoreError(w, err)
		return
	}
	s.startAgentRun(run)
	writeJSON(w, http.StatusCreated, run)
}

func (s *Server) recordRunCreated(ctx context.Context, run agent.Run) error {
	_, err := s.agentStore.AppendRunEvent(ctx, run.ID, agent.AppendEventInput{
		Type: agent.EventRunCreated,
		Data: mustJSON(agent.RunStatusEventData{
			RunID:     run.ID,
			SessionID: run.SessionID,
			Status:    run.Status,
		}),
	})
	return err
}

func retryAgentRunMetadata(original agent.Run) json.RawMessage {
	metadata := map[string]any{}
	if len(original.Metadata) > 0 && json.Valid(original.Metadata) {
		var existing map[string]any
		if err := json.Unmarshal(original.Metadata, &existing); err == nil && existing != nil {
			metadata = existing
		}
	}
	metadata["retryOfRunId"] = original.ID
	return mustJSON(metadata)
}

func (s *Server) handleListAgentRuns(w http.ResponseWriter, r *http.Request) {
	opts, err := parseListAgentRunsOptions(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	runs, err := s.agentStore.ListRuns(r.Context(), opts)
	if err != nil {
		writeAgentStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

func parseListAgentRunsOptions(r *http.Request) (agent.ListRunsOptions, error) {
	query := r.URL.Query()
	opts := agent.ListRunsOptions{Limit: 50}
	if status := query.Get("status"); status != "" {
		runStatus := agent.RunStatus(status)
		if !validRunStatus(runStatus) {
			return agent.ListRunsOptions{}, fmt.Errorf("unsupported status %q", status)
		}
		opts.Status = runStatus
	}
	if rawLimit := query.Get("limit"); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil || limit < 0 {
			return agent.ListRunsOptions{}, fmt.Errorf("limit must be a non-negative integer")
		}
		opts.Limit = limit
	}
	if opts.Limit > 200 {
		opts.Limit = 200
	}
	return opts, nil
}

func validRunStatus(status agent.RunStatus) bool {
	switch status {
	case agent.RunQueued, agent.RunRunning, agent.RunCompleted, agent.RunFailed, agent.RunCancelled:
		return true
	default:
		return false
	}
}

func (s *Server) handleAgentRunEvents(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("run_id")
	follow := agentRunEventsShouldFollow(r)
	events, err := s.agentStore.ListRunEvents(r.Context(), runID)
	if err != nil {
		writeAgentStoreError(w, err)
		return
	}
	var run agent.Run
	if follow {
		run, err = s.agentStore.GetRun(r.Context(), runID)
		if err != nil {
			writeAgentStoreError(w, err)
			return
		}
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	lastSequence, ok := writeServerSentEvents(w, events, 0)
	if !ok {
		return
	}
	flushServerSentEvents(w)
	if !follow || agentRunStatusTerminal(run.Status) {
		return
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
		events, err = s.agentStore.ListRunEvents(r.Context(), runID)
		if err != nil {
			return
		}
		lastSequence, ok = writeServerSentEvents(w, events, lastSequence)
		if !ok {
			return
		}
		run, err = s.agentStore.GetRun(r.Context(), runID)
		if err != nil {
			return
		}
		flushServerSentEvents(w)
		if agentRunStatusTerminal(run.Status) {
			return
		}
	}
}

func (s *Server) startAgentRun(run agent.Run) {
	if s.agentRunner == nil {
		return
	}
	runCtx, cancel := context.WithCancel(context.Background())
	s.registerAgentRunCancel(run.ID, cancel)
	go func() {
		defer s.unregisterAgentRunCancel(run.ID)
		_, err := s.agentRunner.Run(runCtx, agent.EinoRunInput{
			Messages: agentMessagesForRun(run),
			Store:    s.agentStore,
			RunID:    run.ID,
		})
		if err != nil {
			s.recordAgentRunFailure(context.Background(), run.ID, err)
		}
	}()
}

func agentMessagesForRun(run agent.Run) []agent.Message {
	messages := make([]agent.Message, 0, 2)
	if context := agentRunClientContextMessage(run.Metadata); context != "" {
		messages = append(messages, agent.Message{Role: agent.RoleSystem, Content: context})
	}
	messages = append(messages, agent.Message{Role: agent.RoleUser, Content: run.Input})
	return messages
}

func agentRunClientContextMessage(metadata json.RawMessage) string {
	if len(metadata) == 0 || !json.Valid(metadata) {
		return ""
	}
	var record map[string]any
	if err := json.Unmarshal(metadata, &record); err != nil || record == nil {
		return ""
	}
	contextRecord := mapValue(record["clientContext"])
	if contextRecord == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("Client context for this run:\n")
	b.WriteString("- Use this to interpret relative user dates/times such as today, yesterday, last 10 minutes, or this week. It is not proof about Kubernetes state.\n")
	writeClientContextLine(&b, "Client sent at", contextString(contextRecord, "sentAt"))
	writeClientContextLine(&b, "Client local time", contextString(contextRecord, "localTime"))
	writeClientContextLine(&b, "Client time zone", contextString(contextRecord, "timeZone"))
	writeClientContextLine(&b, "Client UTC offset minutes", contextNumberOrString(contextRecord, "timezoneOffsetMinutes"))
	writeClientContextLine(&b, "Client locale", contextString(contextRecord, "locale"))
	writeClientContextLine(&b, "Client languages", contextStringList(contextRecord, "languages"))
	writeClientContextLine(&b, "Page URL", contextString(contextRecord, "pageURL"))
	value := strings.TrimSpace(b.String())
	if value == "Client context for this run:" {
		return ""
	}
	return value
}

func writeClientContextLine(b *strings.Builder, key, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if len(value) > 160 {
		value = value[:157] + "..."
	}
	b.WriteString("- ")
	b.WriteString(key)
	b.WriteString(": ")
	b.WriteString(value)
	b.WriteString(".\n")
}

func mapValue(value any) map[string]any {
	record, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	return record
}

func contextString(record map[string]any, key string) string {
	value, ok := record[key]
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return text
}

func contextNumberOrString(record map[string]any, key string) string {
	value, ok := record[key]
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case int:
		return strconv.Itoa(typed)
	default:
		return ""
	}
}

func contextStringList(record map[string]any, key string) string {
	value, ok := record[key]
	if !ok {
		return ""
	}
	items, ok := value.([]any)
	if !ok {
		return contextString(record, key)
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, ", ")
}

func (s *Server) registerAgentRunCancel(runID string, cancel context.CancelFunc) {
	s.agentRunMu.Lock()
	defer s.agentRunMu.Unlock()
	if s.agentRunCancels == nil {
		s.agentRunCancels = map[string]context.CancelFunc{}
	}
	s.agentRunCancels[runID] = cancel
}

func (s *Server) unregisterAgentRunCancel(runID string) {
	s.agentRunMu.Lock()
	defer s.agentRunMu.Unlock()
	delete(s.agentRunCancels, runID)
}

func (s *Server) cancelAgentRuns() {
	s.agentRunMu.Lock()
	cancels := make([]context.CancelFunc, 0, len(s.agentRunCancels))
	for _, cancel := range s.agentRunCancels {
		cancels = append(cancels, cancel)
	}
	s.agentRunMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (s *Server) cancelAgentRunExecution(runID string) bool {
	s.agentRunMu.Lock()
	cancel := s.agentRunCancels[runID]
	s.agentRunMu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

func (s *Server) recordAgentRunFailure(ctx context.Context, runID string, runErr error) {
	run, err := s.agentStore.GetRun(ctx, runID)
	if err != nil || agentRunStatusTerminal(run.Status) {
		return
	}
	message := "agent run failed"
	if runErr != nil && runErr.Error() != "" {
		message = runErr.Error()
	}
	_, _ = s.agentStore.AppendRunEvent(ctx, run.ID, agent.AppendEventInput{
		Type: agent.EventError,
		Data: mustJSON(agent.ErrorEventData{Message: message}),
	})
	failed, err := s.agentStore.UpdateRunStatus(ctx, run.ID, agent.RunFailed, message)
	if err != nil {
		return
	}
	_, _ = s.agentStore.AppendRunEvent(ctx, run.ID, agent.AppendEventInput{
		Type: agent.EventRunFailed,
		Data: mustJSON(agent.RunStatusEventData{RunID: run.ID, SessionID: failed.SessionID, Status: failed.Status, Error: message}),
	})
}

func agentRunEventsShouldFollow(r *http.Request) bool {
	switch r.URL.Query().Get("follow") {
	case "1", "true":
		return true
	default:
		return false
	}
}

func agentRunStatusTerminal(status agent.RunStatus) bool {
	switch status {
	case agent.RunCompleted, agent.RunFailed, agent.RunCancelled:
		return true
	default:
		return false
	}
}

func (s *Server) handleCancelAgentRun(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("run_id")
	run, err := s.agentStore.GetRun(r.Context(), runID)
	if err != nil {
		writeAgentStoreError(w, err)
		return
	}
	if agentRunStatusTerminal(run.Status) {
		writeJSON(w, http.StatusOK, run)
		return
	}
	s.cancelAgentRunExecution(runID)
	run, err = s.agentStore.UpdateRunStatus(r.Context(), runID, agent.RunCancelled, "")
	if err != nil {
		writeAgentStoreError(w, err)
		return
	}
	_, err = s.agentStore.AppendRunEvent(r.Context(), run.ID, agent.AppendEventInput{
		Type: agent.EventRunCancelled,
		Data: mustJSON(agent.RunStatusEventData{
			RunID:     run.ID,
			SessionID: run.SessionID,
			Status:    run.Status,
		}),
	})
	if err != nil {
		writeAgentStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func decodeOptionalJSON(body io.Reader, out any) error {
	decoder := json.NewDecoder(body)
	if err := decoder.Decode(out); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	return nil
}

func writeAgentStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, agent.ErrSessionNotFound), errors.Is(err, agent.ErrRunNotFound):
		writeError(w, http.StatusNotFound, err)
	default:
		writeError(w, http.StatusInternalServerError, err)
	}
}

func writeServerSentEvents(w io.Writer, events []agent.RunEvent, afterSequence int64) (int64, bool) {
	lastSequence := afterSequence
	for _, event := range events {
		if event.Sequence <= afterSequence {
			continue
		}
		if err := writeServerSentEvent(w, event); err != nil {
			return lastSequence, false
		}
		if event.Sequence > lastSequence {
			lastSequence = event.Sequence
		}
	}
	return lastSequence, true
}

func writeServerSentEvent(w io.Writer, event agent.RunEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.Sequence, event.Type, data)
	return err
}

func flushServerSentEvents(w http.ResponseWriter) {
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func mustJSON(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}
