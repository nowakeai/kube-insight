package sqlite

import "context"

type FilterDecisionMetric struct {
	FilterName string
	Outcome    string
	Reason     string
	Count      int64
}

func (s *Store) FilterDecisionMetrics(ctx context.Context) ([]FilterDecisionMetric, error) {
	rows, err := s.db.QueryContext(ctx, `
select filter_name, outcome, reason, sum(count)
from (
  select filter_name, outcome, reason, count(*) as count
  from filter_decisions
  group by filter_name, outcome, reason
  union all
  select filter_name, outcome, reason, sum(count) as count
  from filter_decision_rollups
  group by filter_name, outcome, reason
)
group by filter_name, outcome, reason
order by filter_name, outcome, reason`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []FilterDecisionMetric
	for rows.Next() {
		var row FilterDecisionMetric
		if err := rows.Scan(&row.FilterName, &row.Outcome, &row.Reason, &row.Count); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
