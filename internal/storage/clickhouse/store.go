package clickhouse

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"kube-insight/internal/core"
	"kube-insight/internal/extractor"
)

// Store writes retained observations and derived evidence into ClickHouse.
// Read-side methods are intentionally minimal until ClickHouse query coverage
// reaches parity with the SQLite store.
type Store struct {
	Client        Client
	Database      string
	BatchSize     int
	FlushInterval time.Duration

	mu                  sync.Mutex
	insertMu            sync.Mutex
	pendingObservations []pendingObservation
	pendingOffsets      map[string]map[string]any
	flushTimer          *time.Timer
}

func NewStore(client Client, opts Options) (*Store, error) {
	if client == nil {
		return nil, fmt.Errorf("clickhouse client is required")
	}
	if opts.Database == "" {
		opts.Database = defaultDatabase
	}
	if err := opts.SchemaOptions().Validate(); err != nil {
		return nil, err
	}
	return &Store{
		Client:        client,
		Database:      opts.Database,
		BatchSize:     opts.BatchSize,
		FlushInterval: time.Duration(opts.FlushIntervalMS) * time.Millisecond,
	}, nil
}

func NewHTTPStore(endpoint string, opts Options) (*Store, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("clickhouse endpoint is required")
	}
	return NewStore(HTTPClient{Endpoint: endpoint, AsyncInsert: opts.AsyncInsert}, opts)
}

func (s *Store) PutObservation(ctx context.Context, obs core.Observation, evidence extractor.Evidence) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	s.pendingObservations = append(s.pendingObservations, pendingObservation{Observation: obs, Evidence: evidence})
	if s.pendingRowsLocked() >= s.batchSizeLocked() {
		batch := s.drainLocked()
		s.mu.Unlock()
		return s.insertPending(ctx, batch)
	}
	s.armFlushTimerLocked()
	s.mu.Unlock()
	return nil
}

func (s *Store) Flush(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	batch := s.drainLocked()
	s.mu.Unlock()
	return s.insertPending(ctx, batch)
}

func (s *Store) Close() error {
	return s.Flush(context.Background())
}

func (s *Store) ApplySchema(ctx context.Context, statements []string) (ApplyResult, error) {
	return s.client().ApplySchema(ctx, statements)
}

type pendingObservation struct {
	Observation core.Observation
	Evidence    extractor.Evidence
}

type objectVersionState struct {
	Seq     uint64
	DocHash string
	Exists  bool
}

type pendingEvidenceBatch struct {
	Observations []pendingObservation
	Offsets      []map[string]any
}

func (s *Store) client() Client {
	if s != nil && s.Client != nil {
		return s.Client
	}
	return HTTPClient{}
}

func (s *Store) batchSizeLocked() int {
	if s.BatchSize <= 0 {
		return 1
	}
	return s.BatchSize
}

func (s *Store) pendingRowsLocked() int {
	return len(s.pendingObservations) + len(s.pendingOffsets)
}

func (s *Store) armFlushTimerLocked() {
	if s.FlushInterval <= 0 || s.flushTimer != nil || s.pendingRowsLocked() == 0 {
		return
	}
	s.flushTimer = time.AfterFunc(s.FlushInterval, func() {
		_ = s.Flush(context.Background())
	})
}

func (s *Store) drainLocked() pendingEvidenceBatch {
	if s.flushTimer != nil {
		s.flushTimer.Stop()
		s.flushTimer = nil
	}
	offsets := make([]map[string]any, 0, len(s.pendingOffsets))
	for _, row := range s.pendingOffsets {
		offsets = append(offsets, row)
	}
	batch := pendingEvidenceBatch{
		Observations: s.pendingObservations,
		Offsets:      offsets,
	}
	s.pendingObservations = nil
	s.pendingOffsets = nil
	return batch
}

func (s *Store) insertPending(ctx context.Context, pending pendingEvidenceBatch) error {
	if len(pending.Observations) == 0 && len(pending.Offsets) == 0 {
		return nil
	}
	s.insertMu.Lock()
	defer s.insertMu.Unlock()
	if len(pending.Observations) > 0 {
		states, err := s.latestVersionStates(ctx, pending.Observations)
		if err != nil {
			return err
		}
		batch, err := buildEvidenceBatchForPending(s.database(), pending.Observations, states)
		if err != nil {
			return err
		}
		if _, err := s.client().InsertEvidenceBatch(ctx, batch); err != nil {
			return err
		}
	}
	if len(pending.Offsets) > 0 {
		return s.client().InsertRows(ctx, s.database(), "ingestion_offsets", pending.Offsets)
	}
	return nil
}

func (s *Store) latestVersionStates(ctx context.Context, observations []pendingObservation) (map[string]objectVersionState, error) {
	objectIDs := uniqueObservationObjectIDs(observations)
	if len(objectIDs) == 0 {
		return map[string]objectVersionState{}, nil
	}
	query := fmt.Sprintf(`SELECT object_id, max(seq) AS seq, argMax(doc_hash, observed_at) AS doc_hash
FROM %s.versions
WHERE object_id IN (%s)
GROUP BY object_id`, q(s.database()), sqlStringList(objectIDs))
	result, err := s.client().QueryTSV(ctx, query)
	if err != nil {
		return nil, err
	}
	states := make(map[string]objectVersionState, len(result.Rows))
	for _, row := range result.Rows {
		if len(row) != 3 {
			return nil, fmt.Errorf("clickhouse latest version state row has %d fields, expected 3", len(row))
		}
		seq, err := strconv.ParseUint(row[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse latest seq for %s: %w", row[0], err)
		}
		states[row[0]] = objectVersionState{Seq: seq, DocHash: row[2], Exists: true}
	}
	return states, nil
}

func uniqueObservationObjectIDs(observations []pendingObservation) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(observations))
	for _, pending := range observations {
		objectID := logicalID(pending.Observation.Ref)
		if objectID == "" || seen[objectID] {
			continue
		}
		seen[objectID] = true
		out = append(out, objectID)
	}
	return out
}
