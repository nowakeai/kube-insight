package collector

import (
	"context"
	"sort"
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

func acquireWatchStreamSlot(ctx context.Context, sem chan struct{}, queued func()) (func(), bool) {
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
	select {
	case sem <- struct{}{}:
		return func() { <-sem }, true
	case <-ctx.Done():
		return nil, false
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
