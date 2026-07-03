package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"kube-insight/internal/agent"
)

type a2aStreamTaskEvent struct {
	Kind  string  `json:"kind"`
	Task  a2aTask `json:"task"`
	Final bool    `json:"final,omitempty"`
}

func (s *Server) handleA2AStreamMessage(w http.ResponseWriter, r *http.Request) {
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
	s.streamA2ATask(w, r, run.ID, input.Configuration.HistoryLength)
}

func (s *Server) handleA2ASubscribeTask(w http.ResponseWriter, r *http.Request, taskID string) {
	if taskID == "" {
		writeError(w, http.StatusNotFound, errors.New("A2A subscribe path must be /tasks/{task_id}:subscribe"))
		return
	}
	if _, err := s.agentStore.GetRun(r.Context(), taskID); err != nil {
		writeAgentStoreError(w, err)
		return
	}
	s.streamA2ATask(w, r, taskID, a2aHistoryLengthPtr(r))
}

func (s *Server) streamA2ATask(w http.ResponseWriter, r *http.Request, runID string, historyLength *int) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	cursor, ok := s.writeA2ATaskSnapshot(w, r, runID, agentRunEventCursor{}, historyLength)
	if !ok {
		return
	}
	flushServerSentEvents(w)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
		run, err := s.agentStore.GetRun(r.Context(), runID)
		if err != nil {
			return
		}
		events, err := s.agentStore.ListRunEvents(r.Context(), runID)
		if err != nil {
			return
		}
		nextCursor := newestAgentRunEventCursor(events)
		if nextCursor != cursor {
			cursor, ok = s.writeA2ATaskSnapshot(w, r, runID, cursor, historyLength)
			if !ok {
				return
			}
			flushServerSentEvents(w)
		}
		if agentRunStatusTerminal(run.Status) && runEventsContainTerminalEvent(events) {
			return
		}
	}
}

func (s *Server) writeA2ATaskSnapshot(w http.ResponseWriter, r *http.Request, runID string, cursor agentRunEventCursor, historyLength *int) (agentRunEventCursor, bool) {
	run, err := s.agentStore.GetRun(r.Context(), runID)
	if err != nil {
		return cursor, false
	}
	task, err := s.a2aTaskFromRun(r.Context(), run, a2aTaskOptions{includeArtifacts: true, historyLength: historyLength})
	if err != nil {
		return cursor, false
	}
	events, err := s.agentStore.ListRunEvents(r.Context(), runID)
	if err != nil {
		return cursor, false
	}
	nextCursor := newestAgentRunEventCursor(events)
	event := a2aStreamTaskEvent{
		Kind:  "task",
		Task:  task,
		Final: agentRunStatusTerminal(run.Status) && runEventsContainTerminalEvent(events),
	}
	if err := writeA2AServerSentEvent(w, "task", nextCursor, event); err != nil {
		return cursor, false
	}
	return nextCursor, true
}

func newestAgentRunEventCursor(events []agent.RunEvent) agentRunEventCursor {
	cursor := agentRunEventCursor{}
	for _, event := range events {
		if agentRunEventAfter(event, cursor) {
			cursor = agentRunEventCursor{Sequence: event.Sequence, ID: event.ID}
		}
	}
	return cursor
}

func writeA2AServerSentEvent(w http.ResponseWriter, eventName string, cursor agentRunEventCursor, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", cursor.Sequence, eventName, data)
	return err
}
