package collector

import (
	"context"
	"sort"
	"sync"
	"time"

	"kube-insight/internal/kubeapi"
	"kube-insight/internal/resourceprofile"
)

func sleepBeforeRetry(ctx context.Context, deadline time.Time, attempt int, minBackoff, maxBackoff time.Duration) error {
	if !deadline.IsZero() && time.Now().After(deadline) {
		return errWatchClosed
	}
	delay := retryDelay(attempt, minBackoff, maxBackoff)
	if !deadline.IsZero() && time.Now().Add(delay).After(deadline) {
		delay = time.Until(deadline)
		if delay <= 0 {
			return errWatchClosed
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func normalizedBackoff(minBackoff, maxBackoff time.Duration) (time.Duration, time.Duration) {
	if minBackoff <= 0 {
		minBackoff = 500 * time.Millisecond
	}
	if maxBackoff <= 0 {
		maxBackoff = 30 * time.Second
	}
	if maxBackoff < minBackoff {
		maxBackoff = minBackoff
	}
	return minBackoff, maxBackoff
}

func newWatchStreamSemaphore(limit int) chan struct{} {
	if limit <= 0 {
		return nil
	}
	return make(chan struct{}, limit)
}

type watchStreamScheduler struct {
	mu       sync.Mutex
	limit    int
	active   int
	nextSeq  int64
	requests map[string]*watchStreamRequest
	score    func(key string, queuedFor time.Duration) int
}

type watchStreamRequest struct {
	key      string
	seq      int64
	queuedAt time.Time
}

func newWatchStreamScheduler(limit int, score func(key string, queuedFor time.Duration) int) *watchStreamScheduler {
	return &watchStreamScheduler{
		limit:    limit,
		requests: map[string]*watchStreamRequest{},
		score:    score,
	}
}

func (s *watchStreamScheduler) acquire(ctx context.Context, key string, queued func(), refreshQueued func(), refreshInterval time.Duration) (func(), bool) {
	if s == nil || s.limit <= 0 {
		return func() {}, true
	}
	req := s.register(key)
	if s.tryAcquire(key) {
		return s.releaseFunc(), true
	}
	if queued != nil {
		queued()
	}
	poll := time.NewTicker(250 * time.Millisecond)
	defer poll.Stop()
	var refresh <-chan time.Time
	var refreshTicker *time.Ticker
	if refreshQueued != nil && refreshInterval > 0 {
		refreshTicker = time.NewTicker(refreshInterval)
		defer refreshTicker.Stop()
		refresh = refreshTicker.C
	}
	for {
		select {
		case <-poll.C:
			if s.tryAcquire(key) {
				return s.releaseFunc(), true
			}
		case <-refresh:
			refreshQueued()
		case <-ctx.Done():
			s.unregister(req)
			return nil, false
		}
	}
}

func (s *watchStreamScheduler) register(key string) *watchStreamRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing := s.requests[key]; existing != nil {
		return existing
	}
	s.nextSeq++
	req := &watchStreamRequest{key: key, seq: s.nextSeq, queuedAt: time.Now()}
	s.requests[key] = req
	return req
}

func (s *watchStreamScheduler) unregister(req *watchStreamRequest) {
	if req == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing := s.requests[req.key]; existing == req {
		delete(s.requests, req.key)
	}
}

func (s *watchStreamScheduler) tryAcquire(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active >= s.limit {
		return false
	}
	best := s.bestLocked(time.Now())
	if best == nil || best.key != key {
		return false
	}
	delete(s.requests, key)
	s.active++
	return true
}

func (s *watchStreamScheduler) bestLocked(now time.Time) *watchStreamRequest {
	var best *watchStreamRequest
	bestScore := 0
	for _, req := range s.requests {
		score := 0
		if s.score != nil {
			score = s.score(req.key, now.Sub(req.queuedAt))
		}
		if best == nil || score > bestScore || (score == bestScore && req.seq < best.seq) {
			best = req
			bestScore = score
		}
	}
	return best
}

func (s *watchStreamScheduler) releaseFunc() func() {
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.active > 0 {
			s.active--
		}
	}
}

func acquireWatchStreamSlot(ctx context.Context, sem chan struct{}, queued func(), refreshQueued func(), refreshInterval time.Duration) (func(), bool) {
	if sem == nil {
		return func() {}, true
	}
	select {
	case sem <- struct{}{}:
		return func() { <-sem }, true
	default:
	}
	if queued != nil {
		queued()
	}
	if refreshQueued == nil || refreshInterval <= 0 {
		select {
		case sem <- struct{}{}:
			return func() { <-sem }, true
		case <-ctx.Done():
			return nil, false
		}
	}
	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()
	for {
		select {
		case sem <- struct{}{}:
			return func() { <-sem }, true
		case <-ticker.C:
			refreshQueued()
		case <-ctx.Done():
			return nil, false
		}
	}
}

func retryDelay(attempt int, minBackoff, maxBackoff time.Duration) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := minBackoff
	for i := 1; i < attempt && delay < maxBackoff; i++ {
		delay *= 2
		if delay > maxBackoff {
			delay = maxBackoff
		}
	}
	delay += retryJitter(delay)
	if delay > maxBackoff {
		delay = maxBackoff
	}
	return delay
}

func retryJitter(delay time.Duration) time.Duration {
	if delay <= 0 {
		return 0
	}
	window := delay / 2
	if window <= 0 {
		return 0
	}
	return time.Duration(time.Now().UnixNano() % int64(window))
}

func sortWatchResources(resources []Resource, rules []resourceprofile.Rule) {
	sort.SliceStable(resources, func(i, j int) bool {
		left := profileForResource(resourceInfo(resources[i]), rules)
		right := profileForResource(resourceInfo(resources[j]), rules)
		if left.Enabled != right.Enabled {
			return left.Enabled
		}
		if left.Priority != right.Priority {
			return priorityRank(left.Priority) < priorityRank(right.Priority)
		}
		return resources[i].Name < resources[j].Name
	})
}

func profileForResource(info kubeapi.ResourceInfo, rules []resourceprofile.Rule) resourceprofile.Profile {
	if len(rules) == 0 {
		return resourceprofile.ForResource(info)
	}
	return resourceprofile.Select(info, rules)
}

func priorityRank(priority string) int {
	switch priority {
	case "high":
		return 0
	case "normal":
		return 1
	case "low":
		return 2
	default:
		return 3
	}
}

func watchScheduleKey(resource Resource) string {
	info := resourceInfo(resource)
	return info.Group + "/" + info.Version + "/" + info.Resource
}

func watchScheduleScore(resource Resource, summary *WatchSummary, rules []resourceprofile.Rule, queuedFor time.Duration) int {
	profile := profileForResource(resourceInfo(resource), rules)
	if !profile.Enabled {
		return -100000
	}
	score := 0
	switch profile.Priority {
	case "high":
		score = 100
	case "normal":
		score = 50
	case "low":
		score = 10
	default:
		score = 0
	}
	if queuedFor > 0 {
		score += min(80, int(queuedFor/time.Minute)*20)
	}
	if summary == nil {
		return score
	}
	score += min(50, summary.Events*5+summary.Bookmarks*2)
	if summary.LatestObjects > 0 {
		score -= min(40, summary.LatestObjects/250)
	}
	score -= min(80, summary.WatchErrors*20+summary.Retries*5)
	return score
}
