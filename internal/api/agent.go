package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

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
	_, err = s.agentStore.AppendRunEvent(r.Context(), run.ID, agent.AppendEventInput{
		Type: agent.EventRunCreated,
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
	writeJSON(w, http.StatusCreated, run)
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
	events, err := s.agentStore.ListRunEvents(r.Context(), r.PathValue("run_id"))
	if err != nil {
		writeAgentStoreError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	for _, event := range events {
		if err := writeServerSentEvent(w, event); err != nil {
			return
		}
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (s *Server) handleCancelAgentRun(w http.ResponseWriter, r *http.Request) {
	run, err := s.agentStore.UpdateRunStatus(r.Context(), r.PathValue("run_id"), agent.RunCancelled, "")
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

func writeServerSentEvent(w io.Writer, event agent.RunEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.Sequence, event.Type, data)
	return err
}

func mustJSON(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}
