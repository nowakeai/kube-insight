package sqlite

import (
	"context"

	"kube-insight/internal/agent"
)

var _ agent.RetentionStore = (*Store)(nil)

func (s *Store) ApplyAgentRetention(ctx context.Context, opts agent.RetentionOptions) (agent.RetentionReport, error) {
	runs, err := s.allAgentRuns(ctx)
	if err != nil {
		return agent.RetentionReport{}, err
	}
	eventsByRunID, err := s.allAgentRunEvents(ctx)
	if err != nil {
		return agent.RetentionReport{}, err
	}
	plan := agent.PlanRetention(runs, eventsByRunID, opts)
	report := plan.Report()
	if opts.DryRun || (len(plan.SupersededRunIDs) == 0 && len(plan.UnreferencedArtifactEventIDs) == 0) {
		return report, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return agent.RetentionReport{}, err
	}
	defer rollback(tx)
	if len(plan.UnreferencedArtifactEventIDs) > 0 {
		if _, err := tx.ExecContext(ctx, `delete from agent_run_events where id in (`+placeholders(len(plan.UnreferencedArtifactEventIDs))+`)`, stringArgs(plan.UnreferencedArtifactEventIDs)...); err != nil {
			return agent.RetentionReport{}, err
		}
	}
	if len(plan.SupersededRunIDs) > 0 {
		if _, err := tx.ExecContext(ctx, `delete from agent_run_events where run_id in (`+placeholders(len(plan.SupersededRunIDs))+`)`, stringArgs(plan.SupersededRunIDs)...); err != nil {
			return agent.RetentionReport{}, err
		}
		if _, err := tx.ExecContext(ctx, `delete from agent_runs where id in (`+placeholders(len(plan.SupersededRunIDs))+`)`, stringArgs(plan.SupersededRunIDs)...); err != nil {
			return agent.RetentionReport{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return agent.RetentionReport{}, err
	}
	return report, nil
}

func (s *Store) allAgentRuns(ctx context.Context) ([]agent.Run, error) {
	rows, err := s.db.QueryContext(ctx, `
select id, session_id, status, input, provider, model, created_at, started_at, completed_at, error, metadata
from agent_runs
order by session_id, created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := []agent.Run{}
	for rows.Next() {
		run, err := scanAgentRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (s *Store) allAgentRunEvents(ctx context.Context) (map[string][]agent.RunEvent, error) {
	rows, err := s.db.QueryContext(ctx, `
select id, run_id, sequence, type, created_at, data
from agent_run_events
order by run_id, sequence, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := map[string][]agent.RunEvent{}
	for rows.Next() {
		event, err := scanAgentRunEvent(rows)
		if err != nil {
			return nil, err
		}
		events[event.RunID] = append(events[event.RunID], event)
	}
	return events, rows.Err()
}
