package clickhouse

import (
	"context"
	"fmt"
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
	pendingObservations []core.Observation
	pendingFacts        []core.Fact
	pendingEdges        []core.Edge
	pendingChanges      []core.Change
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
	s.pendingObservations = append(s.pendingObservations, obs)
	s.pendingFacts = append(s.pendingFacts, evidence.Facts...)
	s.pendingEdges = append(s.pendingEdges, evidence.Edges...)
	s.pendingChanges = append(s.pendingChanges, evidence.Changes...)
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

type pendingEvidenceBatch struct {
	Observations []core.Observation
	Facts        []core.Fact
	Edges        []core.Edge
	Changes      []core.Change
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
		Facts:        s.pendingFacts,
		Edges:        s.pendingEdges,
		Changes:      s.pendingChanges,
		Offsets:      offsets,
	}
	s.pendingObservations = nil
	s.pendingFacts = nil
	s.pendingEdges = nil
	s.pendingChanges = nil
	s.pendingOffsets = nil
	return batch
}

func (s *Store) insertPending(ctx context.Context, pending pendingEvidenceBatch) error {
	if len(pending.Observations) > 0 {
		batch, err := BuildEvidenceBatch(s.database(), pending.Observations, pending.Facts, pending.Edges, pending.Changes)
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
