package agent

import (
	"context"
	"encoding/json"
	"sort"
)

type RetentionOptions struct {
	PruneSupersededRuns        bool `json:"pruneSupersededRuns"`
	PruneUnreferencedArtifacts bool `json:"pruneUnreferencedArtifacts"`
	DryRun                     bool `json:"dryRun,omitempty"`
}

type RetentionReport struct {
	DryRun                            bool     `json:"dryRun,omitempty"`
	RunsScanned                       int      `json:"runsScanned"`
	EventsScanned                     int      `json:"eventsScanned"`
	SupersededRunsDeleted             int      `json:"supersededRunsDeleted"`
	SupersededRunEventsDeleted        int      `json:"supersededRunEventsDeleted"`
	UnreferencedArtifactEventsDeleted int      `json:"unreferencedArtifactEventsDeleted"`
	SupersededRunIDs                  []string `json:"supersededRunIds,omitempty"`
	UnreferencedArtifactEventIDs      []string `json:"unreferencedArtifactEventIds,omitempty"`
}

type RetentionStore interface {
	ApplyAgentRetention(context.Context, RetentionOptions) (RetentionReport, error)
}

type RetentionPlan struct {
	Options                      RetentionOptions
	SupersededRunIDs             []string
	UnreferencedArtifactEventIDs []string
	SupersededRunEvents          int
	RunsScanned                  int
	EventsScanned                int
}

func DefaultRetentionOptions() RetentionOptions {
	return RetentionOptions{PruneSupersededRuns: true, PruneUnreferencedArtifacts: true}
}

func PlanRetention(runs []Run, eventsByRunID map[string][]RunEvent, opts RetentionOptions) RetentionPlan {
	plan := RetentionPlan{Options: opts, RunsScanned: len(runs)}
	for _, events := range eventsByRunID {
		plan.EventsScanned += len(events)
	}
	if opts.PruneSupersededRuns {
		plan.SupersededRunIDs = supersededRunIDsWithChildren(runs, supersededRetryRunIDs(runs))
		for _, runID := range plan.SupersededRunIDs {
			plan.SupersededRunEvents += len(eventsByRunID[runID])
		}
	}
	if opts.PruneUnreferencedArtifacts {
		skipRuns := stringSet(plan.SupersededRunIDs)
		pruneRuns := artifactPruneRunIDSet(runs)
		plan.UnreferencedArtifactEventIDs = unreferencedArtifactEventIDs(eventsByRunID, skipRuns, pruneRuns)
	}
	return plan
}

func (p RetentionPlan) Report() RetentionReport {
	return RetentionReport{
		DryRun:                            p.Options.DryRun,
		RunsScanned:                       p.RunsScanned,
		EventsScanned:                     p.EventsScanned,
		SupersededRunsDeleted:             len(p.SupersededRunIDs),
		SupersededRunEventsDeleted:        p.SupersededRunEvents,
		UnreferencedArtifactEventsDeleted: len(p.UnreferencedArtifactEventIDs),
		SupersededRunIDs:                  append([]string(nil), p.SupersededRunIDs...),
		UnreferencedArtifactEventIDs:      append([]string(nil), p.UnreferencedArtifactEventIDs...),
	}
}

func supersededRetryRunIDs(runs []Run) []string {
	runsByID := map[string]Run{}
	runsBySession := map[string][]Run{}
	for _, run := range runs {
		runsByID[run.ID] = run
		runsBySession[run.SessionID] = append(runsBySession[run.SessionID], run)
	}
	deleteIDs := map[string]struct{}{}
	for _, sessionRuns := range runsBySession {
		sortRetrySessionRuns(sessionRuns, runsByID)
		visible := []Run{}
		for _, run := range sessionRuns {
			if retryOfRunID(run) == "" {
				visible = append(visible, run)
				continue
			}
			rootID := retryRootRunID(run, runsByID)
			replaceIndex := -1
			for i, candidate := range visible {
				if retryRootRunID(candidate, runsByID) == rootID {
					replaceIndex = i
					break
				}
			}
			if replaceIndex >= 0 {
				if run.Status == RunCompleted {
					for _, candidate := range visible[replaceIndex:] {
						if statusTerminal(candidate.Status) {
							deleteIDs[candidate.ID] = struct{}{}
						}
					}
				}
				visible = append(visible[:replaceIndex], run)
			} else {
				visible = append(visible, run)
			}
		}
	}
	return sortedSet(deleteIDs)
}

func sortRetrySessionRuns(runs []Run, runsByID map[string]Run) {
	sort.SliceStable(runs, func(i, j int) bool {
		if retryRootRunID(runs[i], runsByID) == runs[j].ID {
			return false
		}
		if retryRootRunID(runs[j], runsByID) == runs[i].ID {
			return true
		}
		if runs[i].CreatedAt.Equal(runs[j].CreatedAt) {
			return runs[i].ID < runs[j].ID
		}
		return runs[i].CreatedAt.Before(runs[j].CreatedAt)
	})
}

func supersededRunIDsWithChildren(runs []Run, rootIDs []string) []string {
	deleteIDs := stringSet(rootIDs)
	if len(deleteIDs) == 0 {
		return nil
	}
	childrenByParentID := map[string][]string{}
	for _, run := range runs {
		parentID := parentRunID(run)
		if parentID == "" {
			continue
		}
		childrenByParentID[parentID] = append(childrenByParentID[parentID], run.ID)
	}
	queue := append([]string(nil), rootIDs...)
	for len(queue) > 0 {
		runID := queue[0]
		queue = queue[1:]
		for _, childID := range childrenByParentID[runID] {
			if _, seen := deleteIDs[childID]; seen {
				continue
			}
			deleteIDs[childID] = struct{}{}
			queue = append(queue, childID)
		}
	}
	return sortedSet(deleteIDs)
}

func artifactPruneRunIDSet(runs []Run) map[string]struct{} {
	runByID := map[string]Run{}
	for _, run := range runs {
		runByID[run.ID] = run
	}
	out := map[string]struct{}{}
	for _, run := range runs {
		if !statusTerminal(run.Status) {
			continue
		}
		if hasNonTerminalParent(run, runByID) {
			continue
		}
		out[run.ID] = struct{}{}
	}
	return out
}

func hasNonTerminalParent(run Run, runByID map[string]Run) bool {
	seen := map[string]struct{}{}
	parentID := parentRunID(run)
	for parentID != "" {
		if _, ok := seen[parentID]; ok {
			return false
		}
		seen[parentID] = struct{}{}
		parent, ok := runByID[parentID]
		if !ok {
			return false
		}
		if !statusTerminal(parent.Status) {
			return true
		}
		parentID = parentRunID(parent)
	}
	return false
}

func unreferencedArtifactEventIDs(eventsByRunID map[string][]RunEvent, skipRuns map[string]struct{}, pruneRuns map[string]struct{}) []string {
	deleteIDs := map[string]struct{}{}
	referencedArtifactIDs := map[string]struct{}{}
	for runID, events := range eventsByRunID {
		if _, skip := skipRuns[runID]; skip {
			continue
		}
		for _, event := range events {
			if event.Type != EventCitation {
				continue
			}
			var data CitationEventData
			if json.Unmarshal(event.Data, &data) == nil && data.Citation.ArtifactID != "" {
				referencedArtifactIDs[data.Citation.ArtifactID] = struct{}{}
			}
		}
	}
	for runID, events := range eventsByRunID {
		if _, skip := skipRuns[runID]; skip {
			continue
		}
		if _, prune := pruneRuns[runID]; !prune {
			continue
		}
		artifactEventByArtifactID := map[string]string{}
		for _, event := range events {
			switch event.Type {
			case EventArtifact, EventArtifactUpdate:
				var data ArtifactEventData
				if json.Unmarshal(event.Data, &data) == nil && data.Artifact.ID != "" {
					artifactEventByArtifactID[data.Artifact.ID] = event.ID
				}
			}
		}
		for artifactID, eventID := range artifactEventByArtifactID {
			if _, referenced := referencedArtifactIDs[artifactID]; !referenced {
				deleteIDs[eventID] = struct{}{}
			}
		}
	}
	return sortedSet(deleteIDs)
}

func (s *MemoryStore) ApplyAgentRetention(ctx context.Context, opts RetentionOptions) (RetentionReport, error) {
	if err := ctx.Err(); err != nil {
		return RetentionReport{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	runs := make([]Run, 0, len(s.runs))
	for _, run := range s.runs {
		runs = append(runs, run)
	}
	eventsByRunID := map[string][]RunEvent{}
	for runID, events := range s.events {
		eventsByRunID[runID] = append([]RunEvent(nil), events...)
	}
	plan := PlanRetention(runs, eventsByRunID, opts)
	report := plan.Report()
	if opts.DryRun {
		return report, nil
	}
	artifactEventIDs := stringSet(plan.UnreferencedArtifactEventIDs)
	supersededRunIDs := stringSet(plan.SupersededRunIDs)
	for runID, events := range s.events {
		if _, deleteRun := supersededRunIDs[runID]; deleteRun {
			continue
		}
		kept := events[:0]
		for _, event := range events {
			if _, deleteEvent := artifactEventIDs[event.ID]; deleteEvent {
				continue
			}
			kept = append(kept, event)
		}
		s.events[runID] = kept
	}
	for _, runID := range plan.SupersededRunIDs {
		delete(s.events, runID)
		delete(s.runs, runID)
	}
	return report, nil
}

func retryOfRunID(run Run) string {
	return runMetadataString(run, "retryOfRunId")
}

func parentRunID(run Run) string {
	return runMetadataString(run, "parentRunId")
}

func runMetadataString(run Run, key string) string {
	if len(run.Metadata) == 0 || !json.Valid(run.Metadata) {
		return ""
	}
	var metadata map[string]any
	if json.Unmarshal(run.Metadata, &metadata) != nil {
		return ""
	}
	value, _ := metadata[key].(string)
	return value
}

func retryRootRunID(run Run, runsByID map[string]Run) string {
	if rootID := retryRootRunIDMetadata(run); rootID != "" {
		return rootID
	}
	root := run
	seen := map[string]struct{}{}
	for {
		retryOf := retryOfRunID(root)
		if retryOf == "" {
			return root.ID
		}
		if _, ok := seen[root.ID]; ok {
			return root.ID
		}
		seen[root.ID] = struct{}{}
		parent, ok := runsByID[retryOf]
		if !ok {
			return retryOf
		}
		root = parent
	}
}

func retryRootRunIDMetadata(run Run) string {
	return runMetadataString(run, "retryRootRunId")
}

func stringSet(values []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, value := range values {
		if value != "" {
			out[value] = struct{}{}
		}
	}
	return out
}

func sortedSet(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
