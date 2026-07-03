package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"kube-insight/internal/agent"
)

const (
	a2aProtocolVersion = "1.0"
	a2aMediaType       = "application/a2a+json"
	a2aRunWaitTimeout  = 30 * time.Second
)

type a2aAgentCard struct {
	Name                  string                 `json:"name"`
	Description           string                 `json:"description"`
	URL                   string                 `json:"url"`
	ProtocolVersion       string                 `json:"protocolVersion"`
	PreferredTransport    string                 `json:"preferredTransport"`
	SupportedInterfaces   []a2aAgentInterface    `json:"supportedInterfaces"`
	Provider              a2aAgentProvider       `json:"provider"`
	Version               string                 `json:"version"`
	DocumentationURL      string                 `json:"documentationUrl,omitempty"`
	Capabilities          a2aAgentCapabilities   `json:"capabilities"`
	DefaultInputModes     []string               `json:"defaultInputModes"`
	DefaultOutputModes    []string               `json:"defaultOutputModes"`
	Skills                []a2aAgentSkill        `json:"skills"`
	SecuritySchemes       map[string]any         `json:"securitySchemes,omitempty"`
	SecurityRequirements  []map[string][]string  `json:"securityRequirements,omitempty"`
	SupportsExtendedCard  bool                   `json:"supportsAuthenticatedExtendedCard,omitempty"`
	AdditionalInformation map[string]interface{} `json:"metadata,omitempty"`
}

type a2aAgentInterface struct {
	URL             string `json:"url"`
	ProtocolBinding string `json:"protocolBinding"`
	ProtocolVersion string `json:"protocolVersion"`
}

type a2aAgentProvider struct {
	URL          string `json:"url"`
	Organization string `json:"organization"`
}

type a2aAgentCapabilities struct {
	Streaming         bool `json:"streaming"`
	PushNotifications bool `json:"pushNotifications"`
	ExtendedAgentCard bool `json:"extendedAgentCard"`
}

type a2aAgentSkill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Examples    []string `json:"examples,omitempty"`
	InputModes  []string `json:"inputModes,omitempty"`
	OutputModes []string `json:"outputModes,omitempty"`
}

type a2aSendMessageRequest struct {
	Tenant        string                     `json:"tenant,omitempty"`
	Message       a2aMessage                 `json:"message"`
	Configuration a2aSendMessageConfig       `json:"configuration,omitempty"`
	Metadata      map[string]json.RawMessage `json:"metadata,omitempty"`
}

type a2aSendMessageConfig struct {
	AcceptedOutputModes []string `json:"acceptedOutputModes,omitempty"`
	HistoryLength       *int     `json:"historyLength,omitempty"`
	ReturnImmediately   bool     `json:"returnImmediately,omitempty"`
}

type a2aSendMessageResponse struct {
	Task    *a2aTask    `json:"task,omitempty"`
	Message *a2aMessage `json:"message,omitempty"`
}

type a2aTask struct {
	ID        string         `json:"id"`
	ContextID string         `json:"contextId,omitempty"`
	Status    a2aTaskStatus  `json:"status"`
	Artifacts []a2aArtifact  `json:"artifacts,omitempty"`
	History   []a2aMessage   `json:"history,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type a2aTaskStatus struct {
	State     string      `json:"state"`
	Message   *a2aMessage `json:"message,omitempty"`
	Timestamp string      `json:"timestamp,omitempty"`
}

type a2aMessage struct {
	MessageID        string          `json:"messageId,omitempty"`
	ContextID        string          `json:"contextId,omitempty"`
	TaskID           string          `json:"taskId,omitempty"`
	Role             string          `json:"role,omitempty"`
	Parts            []a2aPart       `json:"parts,omitempty"`
	Metadata         json.RawMessage `json:"metadata,omitempty"`
	ReferenceTaskIDs []string        `json:"referenceTaskIds,omitempty"`
}

type a2aPart struct {
	Kind      string          `json:"kind,omitempty"`
	Type      string          `json:"type,omitempty"`
	Text      string          `json:"text,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
	MediaType string          `json:"mediaType,omitempty"`
}

type a2aArtifact struct {
	ArtifactID  string         `json:"artifactId"`
	Name        string         `json:"name,omitempty"`
	Description string         `json:"description,omitempty"`
	Parts       []a2aPart      `json:"parts"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type a2aListTasksResponse struct {
	Tasks         []a2aTask `json:"tasks"`
	NextPageToken string    `json:"nextPageToken"`
	PageSize      int       `json:"pageSize"`
	TotalSize     int       `json:"totalSize"`
}

func (s *Server) MountA2AHTTP(mux *http.ServeMux) {
	mux.HandleFunc("GET /.well-known/agent-card.json", s.handleA2AAgentCard)
	mux.HandleFunc("GET /extendedAgentCard", s.handleA2AAgentCard)
	mux.HandleFunc("POST /message:send", s.handleA2ASendMessage)
	mux.HandleFunc("POST /message:stream", s.handleA2AStreamMessage)
	mux.HandleFunc("GET /tasks/{task_id}", s.handleA2AGetTask)
	mux.HandleFunc("GET /tasks", s.handleA2AListTasks)
	mux.HandleFunc("POST /tasks/", s.handleA2ACancelTask)
}

func (s *Server) handleA2AAgentCard(w http.ResponseWriter, r *http.Request) {
	writeA2AJSON(w, http.StatusOK, s.a2aAgentCard(r))
}

func (s *Server) a2aAgentCard(r *http.Request) a2aAgentCard {
	baseURL := requestBaseURL(r)
	return a2aAgentCard{
		Name:               "kube-insight",
		Description:        "Kubernetes retained-evidence investigation agent for health, history, search, topology, and service-impact questions.",
		URL:                baseURL,
		ProtocolVersion:    a2aProtocolVersion,
		PreferredTransport: "HTTP+JSON",
		SupportedInterfaces: []a2aAgentInterface{{
			URL:             baseURL,
			ProtocolBinding: "HTTP+JSON",
			ProtocolVersion: a2aProtocolVersion,
		}},
		Provider: a2aAgentProvider{
			URL:          "https://github.com/nowake-ai/kube-insight",
			Organization: "nowake-ai",
		},
		Version:              "0.0.0",
		DocumentationURL:     baseURL + "/docs",
		Capabilities:         a2aAgentCapabilities{Streaming: true, PushNotifications: false, ExtendedAgentCard: false},
		DefaultInputModes:    []string{"text/plain"},
		DefaultOutputModes:   []string{"text/markdown", "application/json"},
		Skills:               a2aSkills(),
		SupportsExtendedCard: false,
	}
}

func a2aSkills() []a2aAgentSkill {
	return []a2aAgentSkill{
		{
			ID:          "kubernetes-retained-evidence-investigation",
			Name:        "Kubernetes retained evidence investigation",
			Description: "Investigate Kubernetes health, history, recent changes, relationships, and service impact from retained kube-insight evidence.",
			Tags:        []string{"kubernetes", "aiops", "history", "health", "topology", "events"},
			Examples: []string{
				"Why did pods in namespace checkout restart in the last hour?",
				"What changed before service payments became unhealthy?",
				"Find retained events related to image pull failures in namespace platform.",
			},
			InputModes:  []string{"text/plain"},
			OutputModes: []string{"text/markdown", "application/json"},
		},
	}
}

func (s *Server) handleA2ASendMessage(w http.ResponseWriter, r *http.Request) {
	var input a2aSendMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid A2A json body: %w", err))
		return
	}
	text := strings.TrimSpace(input.Message.text())
	if text == "" {
		writeError(w, http.StatusBadRequest, errors.New("A2A message.parts must include text"))
		return
	}
	run, err := s.createA2ARun(r, input, text)
	if err != nil {
		writeAgentStoreError(w, err)
		return
	}
	run = s.waitA2ARun(r, run.ID, input.Configuration.ReturnImmediately)
	task, err := s.a2aTaskFromRun(r.Context(), run, a2aTaskOptions{includeArtifacts: true, historyLength: input.Configuration.HistoryLength})
	if err != nil {
		writeAgentStoreError(w, err)
		return
	}
	writeA2AJSON(w, http.StatusOK, a2aSendMessageResponse{Task: &task})
}

func (s *Server) createA2ARun(r *http.Request, input a2aSendMessageRequest, text string) (agent.Run, error) {
	sessionID, err := s.a2aSessionID(r, input.Message)
	if err != nil {
		return agent.Run{}, err
	}
	run, err := s.agentStore.CreateRun(r.Context(), sessionID, agent.CreateRunInput{
		Input:    text,
		Metadata: a2aRunMetadata(input),
	})
	if err != nil {
		return agent.Run{}, err
	}
	messages := s.agentMessagesForRun(r.Context(), run)
	if err := s.recordRunCreated(r.Context(), run); err != nil {
		return agent.Run{}, err
	}
	if s.agentRunner == nil {
		s.recordAgentRunFailure(r.Context(), run.ID, errors.New("A2A agent runner is not configured"))
	} else {
		s.startAgentRun(run, messages)
	}
	return run, nil
}

func (s *Server) a2aSessionID(r *http.Request, message a2aMessage) (string, error) {
	if message.TaskID != "" {
		run, err := s.agentStore.GetRun(r.Context(), message.TaskID)
		if err != nil {
			return "", err
		}
		if message.ContextID != "" && message.ContextID != run.SessionID {
			return "", fmt.Errorf("A2A message contextId does not match task context")
		}
		return run.SessionID, nil
	}
	if message.ContextID != "" {
		if _, err := s.agentStore.GetSession(r.Context(), message.ContextID); err == nil {
			return message.ContextID, nil
		}
	}
	session, err := s.agentStore.CreateSession(r.Context(), agent.CreateSessionInput{Title: "A2A investigation"})
	if err != nil {
		return "", err
	}
	return session.ID, nil
}

func a2aRunMetadata(input a2aSendMessageRequest) json.RawMessage {
	metadata := map[string]any{
		"source": "a2a",
	}
	if input.Tenant != "" {
		metadata["a2aTenant"] = input.Tenant
	}
	if input.Message.MessageID != "" {
		metadata["a2aMessageId"] = input.Message.MessageID
	}
	if input.Message.ContextID != "" {
		metadata["a2aContextId"] = input.Message.ContextID
	}
	if input.Message.TaskID != "" {
		metadata["a2aTaskId"] = input.Message.TaskID
	}
	if len(input.Metadata) > 0 {
		metadata["a2aMetadata"] = input.Metadata
	}
	return mustJSON(metadata)
}

func (s *Server) waitA2ARun(r *http.Request, runID string, returnImmediately bool) agent.Run {
	run, err := s.agentStore.GetRun(r.Context(), runID)
	if err != nil || returnImmediately || agentRunStatusTerminal(run.Status) {
		return run
	}
	deadline := time.NewTimer(a2aRunWaitTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return run
		case <-deadline.C:
			return run
		case <-ticker.C:
			next, err := s.agentStore.GetRun(r.Context(), runID)
			if err != nil {
				return run
			}
			run = next
			if agentRunStatusTerminal(run.Status) {
				return run
			}
		}
	}
}

func (s *Server) handleA2AGetTask(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("task_id")
	if strings.HasSuffix(taskID, ":subscribe") {
		s.handleA2ASubscribeTask(w, r, strings.TrimSuffix(taskID, ":subscribe"))
		return
	}
	run, err := s.agentStore.GetRun(r.Context(), taskID)
	if err != nil {
		writeAgentStoreError(w, err)
		return
	}
	task, err := s.a2aTaskFromRun(r.Context(), run, a2aTaskOptions{
		includeArtifacts: true,
		historyLength:    a2aHistoryLengthPtr(r),
	})
	if err != nil {
		writeAgentStoreError(w, err)
		return
	}
	writeA2AJSON(w, http.StatusOK, task)
}

func (s *Server) handleA2AListTasks(w http.ResponseWriter, r *http.Request) {
	opts, err := parseA2AListTasksOptions(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	runs, err := s.agentStore.ListRuns(r.Context(), opts.listRuns)
	if err != nil {
		writeAgentStoreError(w, err)
		return
	}
	tasks := make([]a2aTask, 0, len(runs.Runs))
	for _, run := range runs.Runs {
		if opts.contextID != "" && run.SessionID != opts.contextID {
			continue
		}
		task, err := s.a2aTaskFromRun(r.Context(), run, a2aTaskOptions{
			includeArtifacts: opts.includeArtifacts,
			historyLength:    opts.historyLength,
		})
		if err != nil {
			writeAgentStoreError(w, err)
			return
		}
		tasks = append(tasks, task)
	}
	writeA2AJSON(w, http.StatusOK, a2aListTasksResponse{Tasks: tasks, PageSize: len(tasks), TotalSize: len(tasks)})
}

type a2aListTasksOptions struct {
	listRuns         agent.ListRunsOptions
	contextID        string
	historyLength    *int
	includeArtifacts bool
}

func parseA2AListTasksOptions(r *http.Request) (a2aListTasksOptions, error) {
	query := r.URL.Query()
	opts := a2aListTasksOptions{listRuns: agent.ListRunsOptions{Limit: 50}}
	if contextID := strings.TrimSpace(query.Get("contextId")); contextID != "" {
		opts.contextID = contextID
	}
	if status := strings.TrimSpace(query.Get("status")); status != "" {
		runStatus, ok := a2aRunStatusForTaskState(status)
		if !ok {
			return a2aListTasksOptions{}, fmt.Errorf("unsupported A2A task status %q", status)
		}
		opts.listRuns.Status = runStatus
	}
	if rawPageSize := query.Get("pageSize"); rawPageSize != "" {
		pageSize, err := strconvAtoiNonNegative(rawPageSize, "pageSize")
		if err != nil {
			return a2aListTasksOptions{}, err
		}
		opts.listRuns.Limit = pageSize
	}
	if opts.listRuns.Limit > 100 {
		opts.listRuns.Limit = 100
	}
	opts.historyLength = a2aHistoryLengthPtr(r)
	opts.includeArtifacts = query.Get("includeArtifacts") == "true" || query.Get("includeArtifacts") == "1"
	return opts, nil
}

func (s *Server) handleA2ACancelTask(w http.ResponseWriter, r *http.Request) {
	runID, ok := a2aCancelTaskID(r)
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("A2A cancel path must be /tasks/{task_id}:cancel"))
		return
	}
	run, err := s.agentStore.GetRun(r.Context(), runID)
	if err != nil {
		writeAgentStoreError(w, err)
		return
	}
	if !agentRunStatusTerminal(run.Status) {
		s.cancelAgentRunExecution(runID)
		run, err = s.agentStore.UpdateRunStatus(r.Context(), runID, agent.RunCancelled, "")
		if err != nil {
			writeAgentStoreError(w, err)
			return
		}
		_, err = s.agentStore.AppendRunEvent(r.Context(), run.ID, agent.AppendEventInput{
			Type: agent.EventRunCancelled,
			Data: mustJSON(agent.RunStatusEventData{RunID: run.ID, SessionID: run.SessionID, Status: run.Status}),
		})
		if err != nil {
			writeAgentStoreError(w, err)
			return
		}
	}
	task, err := s.a2aTaskFromRun(r.Context(), run, a2aTaskOptions{includeArtifacts: true})
	if err != nil {
		writeAgentStoreError(w, err)
		return
	}
	writeA2AJSON(w, http.StatusOK, task)
}

func a2aCancelTaskID(r *http.Request) (string, bool) {
	const prefix = "/tasks/"
	const suffix = ":cancel"
	if !strings.HasPrefix(r.URL.Path, prefix) || !strings.HasSuffix(r.URL.Path, suffix) {
		return "", false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix), suffix)
	return id, id != ""
}

type a2aTaskOptions struct {
	includeArtifacts bool
	historyLength    *int
}

func (s *Server) a2aTaskFromRun(ctx context.Context, run agent.Run, opts a2aTaskOptions) (a2aTask, error) {
	events, err := s.agentStore.ListRunEvents(ctx, run.ID)
	if err != nil {
		return a2aTask{}, err
	}
	finalAnswer := run.FinalAnswer
	for _, event := range events {
		if answer := agent.FinalAnswerFromRunEvent(event); answer != "" {
			finalAnswer = answer
		}
	}
	task := a2aTask{
		ID:        run.ID,
		ContextID: run.SessionID,
		Status:    a2aTaskStatusForRun(run, finalAnswer),
		Metadata: map[string]any{
			"kubeInsightRunStatus": run.Status,
		},
	}
	if opts.historyLength == nil || *opts.historyLength != 0 {
		task.History = a2aHistoryForRun(run, finalAnswer, opts.historyLength)
	}
	if opts.includeArtifacts && finalAnswer != "" {
		task.Artifacts = []a2aArtifact{{
			ArtifactID:  "final-answer",
			Name:        "Final answer",
			Description: "kube-insight final investigation answer",
			Parts:       []a2aPart{{Kind: "text", Text: finalAnswer, MediaType: "text/markdown"}},
		}}
	}
	if opts.includeArtifacts {
		task.Artifacts = append(task.Artifacts, a2aArtifactsFromEvents(events)...)
	}
	return task, nil
}

func a2aTaskStatusForRun(run agent.Run, finalAnswer string) a2aTaskStatus {
	status := a2aTaskStatus{
		State:     a2aTaskStateForRunStatus(run.Status),
		Timestamp: a2aRunTimestamp(run).Format(time.RFC3339Nano),
	}
	switch run.Status {
	case agent.RunCompleted:
		if finalAnswer != "" {
			status.Message = &a2aMessage{MessageID: run.ID + "-final", ContextID: run.SessionID, TaskID: run.ID, Role: "agent", Parts: []a2aPart{{Kind: "text", Text: finalAnswer, MediaType: "text/markdown"}}}
		}
	case agent.RunFailed:
		if run.Error != "" {
			status.Message = &a2aMessage{MessageID: run.ID + "-error", ContextID: run.SessionID, TaskID: run.ID, Role: "agent", Parts: []a2aPart{{Kind: "text", Text: run.Error, MediaType: "text/plain"}}}
		}
	}
	return status
}

func a2aHistoryForRun(run agent.Run, finalAnswer string, historyLength *int) []a2aMessage {
	history := []a2aMessage{{
		MessageID: run.ID + "-input",
		ContextID: run.SessionID,
		TaskID:    run.ID,
		Role:      "user",
		Parts:     []a2aPart{{Kind: "text", Text: run.Input, MediaType: "text/plain"}},
	}}
	if finalAnswer != "" {
		history = append(history, a2aMessage{
			MessageID: run.ID + "-final",
			ContextID: run.SessionID,
			TaskID:    run.ID,
			Role:      "agent",
			Parts:     []a2aPart{{Kind: "text", Text: finalAnswer, MediaType: "text/markdown"}},
		})
	}
	if historyLength != nil && *historyLength >= 0 && len(history) > *historyLength {
		return history[len(history)-*historyLength:]
	}
	return history
}

func (m a2aMessage) text() string {
	var parts []string
	for _, part := range m.Parts {
		if strings.TrimSpace(part.Text) == "" {
			continue
		}
		parts = append(parts, strings.TrimSpace(part.Text))
	}
	return strings.Join(parts, "\n\n")
}

func a2aTaskStateForRunStatus(status agent.RunStatus) string {
	switch status {
	case agent.RunQueued:
		return "submitted"
	case agent.RunRunning:
		return "working"
	case agent.RunCompleted:
		return "completed"
	case agent.RunFailed:
		return "failed"
	case agent.RunCancelled:
		return "canceled"
	default:
		return "unknown"
	}
}

func a2aRunStatusForTaskState(status string) (agent.RunStatus, bool) {
	switch status {
	case "submitted", "TASK_STATE_SUBMITTED":
		return agent.RunQueued, true
	case "working", "TASK_STATE_WORKING":
		return agent.RunRunning, true
	case "completed", "TASK_STATE_COMPLETED":
		return agent.RunCompleted, true
	case "failed", "TASK_STATE_FAILED":
		return agent.RunFailed, true
	case "canceled", "cancelled", "TASK_STATE_CANCELED", "TASK_STATE_CANCELLED":
		return agent.RunCancelled, true
	default:
		return "", false
	}
}

func a2aRunTimestamp(run agent.Run) time.Time {
	if run.CompletedAt != nil {
		return run.CompletedAt.UTC()
	}
	if run.StartedAt != nil {
		return run.StartedAt.UTC()
	}
	return run.CreatedAt.UTC()
}

func a2aHistoryLengthPtr(r *http.Request) *int {
	raw := r.URL.Query().Get("historyLength")
	if raw == "" {
		return nil
	}
	value, err := strconvAtoiNonNegative(raw, "historyLength")
	if err != nil {
		return nil
	}
	return &value
}

func strconvAtoiNonNegative(raw string, name string) (int, error) {
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", name)
	}
	return value, nil
}

func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwardedProto := r.Header.Get("X-Forwarded-Proto"); forwardedProto != "" {
		scheme = strings.Split(forwardedProto, ",")[0]
	}
	host := r.Host
	if forwardedHost := r.Header.Get("X-Forwarded-Host"); forwardedHost != "" {
		host = strings.Split(forwardedHost, ",")[0]
	}
	return scheme + "://" + strings.TrimSpace(host)
}

func writeA2AJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", a2aMediaType)
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(value)
}
