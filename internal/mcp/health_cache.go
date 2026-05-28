package mcp

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"kube-insight/internal/storage"
)

const (
	defaultHealthCacheTTL             = 15 * time.Second
	defaultHealthCacheRefreshInterval = 10 * time.Second
)

type healthCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]healthCacheEntry
}

type healthCacheEntry struct {
	opts      storage.ResourceHealthOptions
	report    storage.ResourceHealthReport
	expiresAt time.Time
}

func newHealthCache(ttl time.Duration) *healthCache {
	return &healthCache{
		ttl:     ttl,
		entries: map[string]healthCacheEntry{},
	}
}

func (c *healthCache) get(key string, now time.Time) (storage.ResourceHealthReport, bool) {
	if c == nil {
		return storage.ResourceHealthReport{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || now.After(entry.expiresAt) {
		return storage.ResourceHealthReport{}, false
	}
	return entry.report, true
}

func (c *healthCache) set(key string, opts storage.ResourceHealthOptions, report storage.ResourceHealthReport, now time.Time) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = healthCacheEntry{
		opts:      cloneResourceHealthOptions(opts),
		report:    report,
		expiresAt: now.Add(c.ttl),
	}
}

func (c *healthCache) refreshOptions() []storage.ResourceHealthOptions {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]storage.ResourceHealthOptions, 0, len(c.entries))
	for _, entry := range c.entries {
		out = append(out, cloneResourceHealthOptions(entry.opts))
	}
	return out
}

func healthCacheKey(opts storage.ResourceHealthOptions) string {
	opts = cloneResourceHealthOptions(opts)
	sort.Strings(opts.ExcludeResources)
	payload := struct {
		ClusterID        string   `json:"clusterId,omitempty"`
		Status           string   `json:"status,omitempty"`
		ErrorsOnly       bool     `json:"errorsOnly,omitempty"`
		StaleAfter       string   `json:"staleAfter,omitempty"`
		Limit            int      `json:"limit,omitempty"`
		ExcludeResources []string `json:"exclude,omitempty"`
		IncludeExcluded  bool     `json:"includeExcluded,omitempty"`
	}{
		ClusterID:        strings.TrimSpace(opts.ClusterID),
		Status:           strings.TrimSpace(opts.Status),
		ErrorsOnly:       opts.ErrorsOnly,
		Limit:            opts.Limit,
		ExcludeResources: opts.ExcludeResources,
		IncludeExcluded:  opts.IncludeExcluded,
	}
	if opts.StaleAfter > 0 {
		payload.StaleAfter = opts.StaleAfter.String()
	}
	data, _ := json.Marshal(payload)
	return string(data)
}

func cloneResourceHealthOptions(opts storage.ResourceHealthOptions) storage.ResourceHealthOptions {
	opts.ClusterID = strings.TrimSpace(opts.ClusterID)
	opts.Status = strings.TrimSpace(opts.Status)
	if len(opts.ExcludeResources) > 0 {
		exclude := make([]string, 0, len(opts.ExcludeResources))
		for _, value := range opts.ExcludeResources {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			exclude = append(exclude, value)
		}
		opts.ExcludeResources = exclude
	}
	return opts
}

type healthRefreshLoop struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

func (s *Server) ensureHealthRefreshLoop() {
	if s.healthRefresh == nil {
		s.healthRefresh = &healthRefreshLoop{}
	}
	s.healthRefresh.mu.Lock()
	defer s.healthRefresh.mu.Unlock()
	if s.healthRefresh.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.healthRefresh.cancel = cancel
	s.healthRefresh.done = done
	go s.runHealthRefreshLoop(ctx, done)
}

func (s *Server) runHealthRefreshLoop(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(defaultHealthCacheRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.refreshHealthCache(ctx)
		}
	}
}

func (s *Server) refreshHealthCache(ctx context.Context) {
	optsList := s.healthCache.refreshOptions()
	if len(optsList) == 0 {
		return
	}
	store, err := s.openReadStore(ctx)
	if err != nil {
		return
	}
	defer s.closeReadStore(store)
	healthStore, ok := store.(storage.ResourceHealthStore)
	if !ok {
		return
	}
	now := time.Now()
	for _, opts := range optsList {
		if ctx.Err() != nil {
			return
		}
		report, err := healthStore.ResourceHealth(ctx, opts)
		if err != nil {
			continue
		}
		s.healthCache.set(healthCacheKey(opts), opts, report, now)
	}
}

func (l *healthRefreshLoop) stop() {
	l.mu.Lock()
	cancel := l.cancel
	done := l.done
	l.cancel = nil
	l.done = nil
	l.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}
