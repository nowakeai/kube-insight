package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"path"
	"strings"
	"time"
)

type RetentionOptions struct {
	MaxAge               time.Duration
	MinVersionsPerObject int
	FilterDecisionMaxAge time.Duration
	Rules                []RetentionRuleOptions
	SkipWhenDatabaseBusy bool
}

type RetentionRuleOptions struct {
	Name                 string
	Profiles             []string
	RetentionPolicies    []string
	Resources            []string
	MaxAge               time.Duration
	MinVersionsPerObject int
}

type RetentionReport struct {
	StartedAt              time.Time             `json:"startedAt"`
	FinishedAt             time.Time             `json:"finishedAt"`
	Cutoff                 time.Time             `json:"cutoff,omitempty"`
	FilterDecisionCutoff   time.Time             `json:"filterDecisionCutoff,omitempty"`
	Versions               int64                 `json:"versions"`
	Observations           int64                 `json:"observations"`
	Facts                  int64                 `json:"facts"`
	Changes                int64                 `json:"changes"`
	Edges                  int64                 `json:"edges"`
	Blobs                  int64                 `json:"blobs"`
	FilterDecisions        int64                 `json:"filterDecisions"`
	Rules                  []RetentionRuleReport `json:"rules,omitempty"`
	BytesBefore            int64                 `json:"bytesBefore"`
	BytesAfter             int64                 `json:"bytesAfter"`
	BytesReclaimedEstimate int64                 `json:"bytesReclaimedEstimate"`
	Skipped                bool                  `json:"skipped"`
	Reason                 string                `json:"reason,omitempty"`
}

type RetentionRuleReport struct {
	Name              string    `json:"name,omitempty"`
	Cutoff            time.Time `json:"cutoff"`
	Versions          int64     `json:"versions"`
	Profiles          []string  `json:"profiles,omitempty"`
	RetentionPolicies []string  `json:"retentionPolicies,omitempty"`
	Resources         []string  `json:"resources,omitempty"`
}

func (s *Store) ApplyRetention(ctx context.Context, options ...RetentionOptions) (RetentionReport, error) {
	opts := RetentionOptions{MinVersionsPerObject: 1}
	if len(options) > 0 {
		opts = options[0]
		if opts.MinVersionsPerObject < 0 {
			opts.MinVersionsPerObject = 0
		}
	}
	report := RetentionReport{StartedAt: time.Now().UTC()}
	before, err := databaseFilesBytes(s.path)
	if err != nil {
		return RetentionReport{}, err
	}
	report.BytesBefore = before
	if opts.MaxAge <= 0 && opts.FilterDecisionMaxAge <= 0 && !hasRetentionRules(opts.Rules) {
		report.Skipped = true
		report.Reason = "no_policy"
		report.BytesAfter = report.BytesBefore
		report.FinishedAt = time.Now().UTC()
		return report, nil
	}

	err = s.applyRetention(ctx, opts, &report)
	if err != nil {
		if ignoreBusyMaintenanceError(err, MaintenanceOptions{SkipWhenDatabaseBusy: opts.SkipWhenDatabaseBusy}) {
			report.Skipped = true
			report.Reason = "busy"
			report.BytesAfter = report.BytesBefore
			report.FinishedAt = time.Now().UTC()
			return report, nil
		}
		return RetentionReport{}, err
	}
	after, err := databaseFilesBytes(s.path)
	if err != nil {
		return RetentionReport{}, err
	}
	report.BytesAfter = after
	report.FinishedAt = time.Now().UTC()
	if report.BytesBefore > report.BytesAfter {
		report.BytesReclaimedEstimate = report.BytesBefore - report.BytesAfter
	}
	return report, nil
}

func (s *Store) applyRetention(ctx context.Context, opts RetentionOptions, report *RetentionReport) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)

	for _, stmt := range []string{
		`create temp table if not exists retention_versions(id integer primary key)`,
		`delete from retention_versions`,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	now := time.Now().UTC()
	if opts.MaxAge > 0 {
		report.Cutoff = now.Add(-opts.MaxAge)
		cutoffMillis := millis(report.Cutoff)
		if err := selectRetentionVersions(ctx, tx, cutoffMillis, opts.MinVersionsPerObject, RetentionRuleOptions{}); err != nil {
			return err
		}
	}
	for _, rule := range opts.Rules {
		if rule.MaxAge <= 0 {
			continue
		}
		ruleReport := RetentionRuleReport{
			Name:              rule.Name,
			Cutoff:            now.Add(-rule.MaxAge),
			Profiles:          normalizeRetentionValues(rule.Profiles),
			RetentionPolicies: normalizeRetentionValues(rule.RetentionPolicies),
			Resources:         normalizeRetentionValues(rule.Resources),
		}
		beforeCount := int64(0)
		if err := tx.QueryRowContext(ctx, `select count(*) from retention_versions`).Scan(&beforeCount); err != nil {
			return err
		}
		minVersions := rule.MinVersionsPerObject
		if minVersions <= 0 {
			minVersions = opts.MinVersionsPerObject
		}
		if err := selectRetentionVersions(ctx, tx, millis(ruleReport.Cutoff), minVersions, RetentionRuleOptions{
			Profiles:          ruleReport.Profiles,
			RetentionPolicies: ruleReport.RetentionPolicies,
			Resources:         ruleReport.Resources,
		}); err != nil {
			return err
		}
		afterCount := int64(0)
		if err := tx.QueryRowContext(ctx, `select count(*) from retention_versions`).Scan(&afterCount); err != nil {
			return err
		}
		ruleReport.Versions = afterCount - beforeCount
		report.Rules = append(report.Rules, ruleReport)
	}
	if opts.MaxAge > 0 || hasRetentionRules(opts.Rules) {
		if err := tx.QueryRowContext(ctx, `select count(*) from retention_versions`).Scan(&report.Versions); err != nil {
			return err
		}
		nullObservationClause := ""
		var observationArgs []any
		if opts.MaxAge > 0 {
			nullObservationClause = " or (version_id is null and observed_at < ?)"
			observationArgs = append(observationArgs, millis(now.Add(-opts.MaxAge)))
		}
		var res sql.Result
		if res, err = tx.ExecContext(ctx, `
delete from object_observations
where version_id in (select id from retention_versions)
`+nullObservationClause, observationArgs...); err != nil {
			return err
		}
		report.Observations, _ = res.RowsAffected()
		if res, err = tx.ExecContext(ctx, `delete from object_facts where version_id in (select id from retention_versions)`); err != nil {
			return err
		}
		report.Facts, _ = res.RowsAffected()
		if res, err = tx.ExecContext(ctx, `delete from object_changes where version_id in (select id from retention_versions)`); err != nil {
			return err
		}
		report.Changes, _ = res.RowsAffected()
		if res, err = tx.ExecContext(ctx, `
delete from object_edges
where src_version_id in (select id from retention_versions)
   or dst_version_id in (select id from retention_versions)`); err != nil {
			return err
		}
		report.Edges, _ = res.RowsAffected()
		if _, err := tx.ExecContext(ctx, `update versions set parent_version_id = null where parent_version_id in (select id from retention_versions)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `delete from versions where id in (select id from retention_versions)`); err != nil {
			return err
		}
		if res, err = tx.ExecContext(ctx, `delete from blobs where digest not in (select distinct blob_ref from versions)`); err != nil {
			return err
		}
		report.Blobs, _ = res.RowsAffected()
	}
	if opts.FilterDecisionMaxAge > 0 {
		report.FilterDecisionCutoff = now.Add(-opts.FilterDecisionMaxAge)
		res, err := tx.ExecContext(ctx, `delete from filter_decisions where ts < ?`, millis(report.FilterDecisionCutoff))
		if err != nil {
			return err
		}
		report.FilterDecisions, _ = res.RowsAffected()
		res, err = tx.ExecContext(ctx, `delete from filter_decision_rollups where bucket_start < ?`, millis(report.FilterDecisionCutoff))
		if err != nil {
			return err
		}
		rows, _ := res.RowsAffected()
		report.FilterDecisions += rows
	}
	return tx.Commit()
}

func selectRetentionVersions(ctx context.Context, tx *sql.Tx, cutoffMillis int64, minVersionsPerObject int, rule RetentionRuleOptions) error {
	query := `
insert into retention_versions(id)
select ranked.id
from (
  select
    v.id,
    v.observed_at,
    lower(ar.resource || case when ar.api_group = '' then '' else '.' || ar.api_group end) as resource_name,
    lower(case when ar.api_group = '' then ar.api_version || '/' || ar.resource else ar.api_group || '/' || ar.api_version || '/' || ar.resource end) as gvr,
    lower(coalesce(rpp.profile, 'generic')) as profile,
    lower(coalesce(rpp.retention_class, 'standard')) as retention_class,
    row_number() over (
      partition by v.object_id
      order by v.observed_at desc, v.id desc
    ) as version_rank
  from versions v
  join objects o on o.id = v.object_id
  join object_kinds ok on ok.id = o.kind_id
  join api_resources ar on ar.id = ok.api_resource_id
  left join resource_processing_profiles rpp on rpp.api_resource_id = ar.id
) ranked
where ranked.observed_at < ?
  and not exists (select 1 from latest_index li where li.latest_version_id = ranked.id)
  and not exists (select 1 from objects o where o.latest_version_id = ranked.id)`
	args := []any{cutoffMillis}
	if minVersionsPerObject > 0 {
		query += ` and ranked.version_rank > ?`
		args = append(args, minVersionsPerObject)
	}
	filterSQL, filterArgs := retentionRuleFilterSQL(rule)
	if filterSQL != "" {
		query += " and (" + filterSQL + ")"
		args = append(args, filterArgs...)
	}
	_, err := tx.ExecContext(ctx, query, args...)
	return err
}

func hasRetentionRules(rules []RetentionRuleOptions) bool {
	for _, rule := range rules {
		if rule.MaxAge > 0 {
			return true
		}
	}
	return false
}

func retentionObservationCutoff(now time.Time, opts RetentionOptions) time.Time {
	var out time.Time
	if opts.MaxAge > 0 {
		out = now.Add(-opts.MaxAge)
	}
	for _, rule := range opts.Rules {
		if rule.MaxAge <= 0 {
			continue
		}
		cutoff := now.Add(-rule.MaxAge)
		if out.IsZero() || cutoff.Before(out) {
			out = cutoff
		}
	}
	return out
}

func retentionRuleFilterSQL(rule RetentionRuleOptions) (string, []any) {
	profiles := normalizeRetentionValues(rule.Profiles)
	retentionPolicies := normalizeRetentionValues(rule.RetentionPolicies)
	resources := normalizeRetentionValues(rule.Resources)
	var parts []string
	var args []any
	if len(profiles) > 0 {
		parts = append(parts, "ranked.profile in ("+placeholders(len(profiles))+")")
		args = append(args, stringsToAny(profiles)...)
	}
	if len(retentionPolicies) > 0 {
		parts = append(parts, "ranked.retention_class in ("+placeholders(len(retentionPolicies))+")")
		args = append(args, stringsToAny(retentionPolicies)...)
	}
	if len(resources) > 0 {
		for _, resource := range resources {
			if isSQLiteGlob(resource) {
				parts = append(parts, "(ranked.resource_name glob ? or ranked.gvr glob ?)")
				args = append(args, resource, resource)
				continue
			}
			parts = append(parts, "(ranked.resource_name = ? or ranked.gvr = ?)")
			args = append(args, resource, resource)
		}
	}
	if len(parts) == 0 {
		return "", nil
	}
	return strings.Join(parts, " or "), args
}

func isSQLiteGlob(value string) bool {
	return strings.ContainsAny(value, "*?[") && isValidPathGlob(value)
}

func isValidPathGlob(value string) bool {
	_, err := path.Match(value, "")
	return err == nil
}

func normalizeRetentionValues(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func stringsToAny(values []string) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func (r RetentionRuleOptions) String() string {
	if r.Name != "" {
		return r.Name
	}
	return fmt.Sprintf("profiles=%s retentionPolicies=%s resources=%s", strings.Join(r.Profiles, ","), strings.Join(r.RetentionPolicies, ","), strings.Join(r.Resources, ","))
}
