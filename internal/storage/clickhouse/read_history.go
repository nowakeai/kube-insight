package clickhouse

import (
	"context"
	"fmt"

	"kube-insight/internal/storage"
)

func (s *Store) ObjectHistory(ctx context.Context, target storage.ObjectTarget, opts storage.ObjectHistoryOptions) (storage.ObjectHistory, error) {
	object, err := s.findObject(ctx, target)
	if err != nil {
		return storage.ObjectHistory{}, err
	}
	opts.MaxVersions = boundedLimit(opts.MaxVersions, 50, 1000)
	opts.MaxObservations = boundedLimit(opts.MaxObservations, 100, 5000)
	versions, err := s.historyVersions(ctx, object.LogicalID, opts)
	if err != nil {
		return storage.ObjectHistory{}, err
	}
	observations, err := s.historyObservations(ctx, object, opts)
	if err != nil {
		return storage.ObjectHistory{}, err
	}
	out := storage.ObjectHistory{
		Object:       object,
		Versions:     versions,
		Observations: observations,
		Summary: storage.ObjectHistorySummary{
			Versions:                   len(versions),
			Observations:               int64(len(observations)),
			ReturnedObservations:       len(observations),
			ContentChangedObservations: int64(len(observations)),
		},
	}
	if opts.IncludeDiffs {
		out.VersionDiffs = clickHouseVersionDiffs(versions)
		out.Summary.VersionDiffs = len(out.VersionDiffs)
	}
	return out, nil
}

func (s *Store) historyVersions(ctx context.Context, objectID string, opts storage.ObjectHistoryOptions) ([]storage.HistoryVersion, error) {
	query := fmt.Sprintf(`
SELECT seq, observed_at, resource_version, doc_hash, materialization, raw_size, stored_size, doc
FROM %s.versions
WHERE object_id = %s%s
ORDER BY observed_at DESC, seq DESC
LIMIT %d`, q(s.database()), quoteString(objectID), timeFilter("observed_at", opts.From, opts.To), opts.MaxVersions)
	result, err := s.client().QueryJSON(ctx, query)
	if err != nil {
		return nil, err
	}
	versions := make([]storage.HistoryVersion, 0, len(result.Data))
	for _, row := range result.Data {
		seq := int64Value(row["seq"])
		version := storage.HistoryVersion{
			ID:               seq,
			Sequence:         seq,
			ObservedAt:       timeValue(row["observed_at"]),
			ResourceVersion:  stringValue(row["resource_version"]),
			DocumentHash:     stringValue(row["doc_hash"]),
			Materialization:  stringValue(row["materialization"]),
			Strategy:         "full",
			RawSize:          int64Value(row["raw_size"]),
			StoredSize:       int64Value(row["stored_size"]),
			ObservationCount: 1,
		}
		version.FirstObservedAt = version.ObservedAt
		version.LastObservedAt = version.ObservedAt
		version.LatestResourceVersion = version.ResourceVersion
		if opts.IncludeDocs {
			version.Document = jsonMap(row["doc"])
		}
		versions = append(versions, version)
	}
	return versions, nil
}

func (s *Store) historyObservations(ctx context.Context, object storage.ObjectRecord, opts storage.ObjectHistoryOptions) ([]storage.HistoryObservation, error) {
	query := fmt.Sprintf(`
SELECT observed_at, observation_type, resource_version, doc_hash
FROM %s.observations
WHERE cluster_id = %s
  AND ((%s != '' AND uid = %s) OR (%s = '' AND kind = %s AND namespace = %s AND name = %s))%s
ORDER BY observed_at DESC
LIMIT %d`, q(s.database()), quoteString(object.ClusterID), quoteString(object.UID), quoteString(object.UID), quoteString(object.UID), quoteString(object.Kind), quoteString(object.Namespace), quoteString(object.Name), timeFilter("observed_at", opts.From, opts.To), opts.MaxObservations)
	result, err := s.client().QueryJSON(ctx, query)
	if err != nil {
		return nil, err
	}
	observations := make([]storage.HistoryObservation, 0, len(result.Data))
	for i, row := range result.Data {
		observations = append(observations, storage.HistoryObservation{
			ID:              int64(i + 1),
			ObservedAt:      timeValue(row["observed_at"]),
			ObservationType: stringValue(row["observation_type"]),
			ResourceVersion: stringValue(row["resource_version"]),
			DocumentHash:    stringValue(row["doc_hash"]),
			ContentChanged:  true,
		})
	}
	return observations, nil
}
