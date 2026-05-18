package clickhouse

import (
	"context"
	"fmt"

	"kube-insight/internal/core"
)

func (s *Store) GetFacts(ctx context.Context, objectID string) ([]core.Fact, error) {
	if s == nil || objectID == "" {
		return nil, nil
	}
	if err := s.Flush(ctx); err != nil {
		return nil, err
	}
	query := fmt.Sprintf(`
SELECT ts, object_id, fact_key, fact_value, numeric_value, severity, detail
FROM %s.facts
WHERE object_id = %s
   OR object_id IN (SELECT object_id FROM %s.object_aliases WHERE alias_id = %s)
ORDER BY ts, fact_key`, q(s.database()), quoteString(objectID), q(s.database()), quoteString(objectID))
	result, err := s.client().QueryJSON(ctx, query)
	if err != nil {
		return nil, err
	}
	facts := make([]core.Fact, 0, len(result.Data))
	for _, row := range result.Data {
		facts = append(facts, core.Fact{
			Time:         timeValue(row["ts"]),
			ObjectID:     stringValue(row["object_id"]),
			Key:          stringValue(row["fact_key"]),
			Value:        stringValue(row["fact_value"]),
			NumericValue: numericPointer(row["numeric_value"]),
			Severity:     int(int64Value(row["severity"])),
			Detail:       jsonMap(row["detail"]),
		})
	}
	return facts, nil
}

func (s *Store) GetEdges(ctx context.Context, sourceID string) ([]core.Edge, error) {
	if s == nil || sourceID == "" {
		return nil, nil
	}
	if err := s.Flush(ctx); err != nil {
		return nil, err
	}
	sourceIDs, err := s.objectAndAliasIDs(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf(`
SELECT edge_type, src_id, dst_id, valid_from, if(valid_to_ms >= 9223372036854770000, '', toString(valid_to)) AS valid_to, detail
FROM %s.edges
WHERE src_id IN (%s)
ORDER BY valid_from, edge_type, dst_id`, q(s.database()), sqlStringList(sourceIDs))
	result, err := s.client().QueryJSON(ctx, query)
	if err != nil {
		return nil, err
	}
	edges := make([]core.Edge, 0, len(result.Data))
	for _, row := range result.Data {
		edge := core.Edge{
			Type:      stringValue(row["edge_type"]),
			SourceID:  stringValue(row["src_id"]),
			TargetID:  stringValue(row["dst_id"]),
			ValidFrom: timeValue(row["valid_from"]),
			Detail:    jsonMap(row["detail"]),
		}
		if validTo := timeValue(row["valid_to"]); !validTo.IsZero() {
			edge.ValidTo = &validTo
		}
		edges = append(edges, edge)
	}
	return edges, nil
}
