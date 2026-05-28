package clickhouse

import (
	"context"
	"fmt"
	"time"

	"kube-insight/internal/agent"
)

var _ agent.RetentionStore = (*Store)(nil)

func (s *Store) ApplyAgentRetention(ctx context.Context, opts agent.RetentionOptions) (agent.RetentionReport, error) {
	if err := s.ensureAgentSchema(ctx); err != nil {
		return agent.RetentionReport{}, err
	}
	runs, eventsByRunID, err := s.agentRetentionInputs(ctx)
	if err != nil {
		return agent.RetentionReport{}, err
	}
	plan := agent.PlanRetention(runs, eventsByRunID, opts)
	report := plan.Report()
	scratchReport, err := agent.CleanupScratchStores(ctx, runs, opts, time.Now().UTC())
	if err != nil {
		return agent.RetentionReport{}, err
	}
	agent.MergeScratchRetentionReport(&report, scratchReport)
	if opts.DryRun || (len(plan.SupersededRunIDs) == 0 && len(plan.UnreferencedArtifactEventIDs) == 0) {
		return report, nil
	}
	statements := []string{}
	if len(plan.UnreferencedArtifactEventIDs) > 0 {
		statements = append(statements, fmt.Sprintf("ALTER TABLE %s.agent_run_events DELETE WHERE id IN (%s)", q(s.database()), sqlStringList(plan.UnreferencedArtifactEventIDs)))
	}
	if len(plan.SupersededRunIDs) > 0 {
		statements = append(statements,
			fmt.Sprintf("ALTER TABLE %s.agent_run_events DELETE WHERE run_id IN (%s)", q(s.database()), sqlStringList(plan.SupersededRunIDs)),
			fmt.Sprintf("ALTER TABLE %s.agent_runs DELETE WHERE id IN (%s)", q(s.database()), sqlStringList(plan.SupersededRunIDs)),
		)
	}
	if _, err := s.client().ApplySchema(ctx, statements); err != nil {
		return agent.RetentionReport{}, err
	}
	return report, nil
}

func (s *Store) agentRetentionInputs(ctx context.Context) ([]agent.Run, map[string][]agent.RunEvent, error) {
	runList, err := s.ListRuns(ctx, agent.ListRunsOptions{})
	if err != nil {
		return nil, nil, err
	}
	eventsByRunID := map[string][]agent.RunEvent{}
	for _, run := range runList.Runs {
		events, err := s.ListRunEvents(ctx, run.ID)
		if err != nil {
			return nil, nil, err
		}
		eventsByRunID[run.ID] = events
	}
	return runList.Runs, eventsByRunID, nil
}
