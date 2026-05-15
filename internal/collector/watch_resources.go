package collector

import (
	"context"
	"errors"
	"sync"
	"time"

	"kube-insight/internal/ingest"
	"kube-insight/internal/kubeapi"
	"kube-insight/internal/storage"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

type preparedWatchResource struct {
	Context   string
	ClusterID string
	Resource  Resource
	Info      kubeapi.ResourceInfo
	Client    dynamic.ResourceInterface
}

func runInitialListPhase(ctx context.Context, opts WatchResourcesOptions, resources []Resource, deadline time.Time, summary *WatchResourcesSummary) {
	sem := make(chan struct{}, opts.Concurrency)
	var wg sync.WaitGroup
	for i, resource := range resources {
		i := i
		resource := resource
		summary.Workers[i].Resource = resource
		wg.Add(1)
		go func() {
			defer wg.Done()
			waitStart := time.Now()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				summary.Workers[i].Error = ctx.Err().Error()
				return
			}
			waitMS := durationMillis(waitStart)
			summary.Workers[i].QueueWaitMS = waitMS
			if i >= opts.Concurrency {
				summary.Workers[i].Queued = true
			}
			watchLog(opts.Logf, "initial list worker start", "resource", resourceLabel(resource), "queueWaitMs", waitMS)
			workerSummary, err := WatchResourceInitialListClientGo(ctx, WatchOptions{
				Context:         opts.Context,
				ClusterID:       opts.ClusterID,
				Resource:        resource,
				Namespace:       opts.Namespace,
				Store:           opts.Store,
				Logf:            opts.Logf,
				Timeout:         remainingTimeout(deadline),
				MaxRetries:      opts.MaxRetries,
				Filters:         opts.Filters,
				FilterChains:    opts.FilterChains,
				Extractors:      opts.Extractors,
				DisableHTTP2:    opts.DisableHTTP2,
				RetryMinBackoff: opts.RetryMinBackoff,
				RetryMaxBackoff: opts.RetryMaxBackoff,
				ProfileRules:    opts.ProfileRules,
			})
			summary.Workers[i].Summary = &workerSummary
			if err != nil {
				summary.Workers[i].Error = err.Error()
				watchLog(opts.Logf, "initial list worker error", "resource", resourceLabel(resource), "error", err)
				return
			}
			watchLog(opts.Logf, "initial list worker done", "resource", resourceLabel(resource), "listed", workerSummary.Listed, "stored", workerSummary.Stored, "deleted", workerSummary.Deleted, "resourceVersion", emptyLabel(workerSummary.ResourceVersion))
		}()
	}
	wg.Wait()
}

func runWatchStreamPhase(ctx context.Context, opts WatchResourcesOptions, deadline time.Time, summary *WatchResourcesSummary) {
	var wg sync.WaitGroup
	streamSem := newWatchStreamSemaphore(opts.MaxConcurrentStreams)
	started := 0
	for i := range summary.Workers {
		i := i
		worker := summary.Workers[i]
		if worker.Error != "" || worker.Summary == nil {
			continue
		}
		startDelay := time.Duration(started) * opts.StreamStartStagger
		started++
		wg.Add(1)
		go func() {
			defer wg.Done()
			if startDelay > 0 {
				timer := time.NewTimer(startDelay)
				select {
				case <-ctx.Done():
					timer.Stop()
					summary.Workers[i].Error = ctx.Err().Error()
					return
				case <-timer.C:
				}
			}
			release, ok := acquireWatchStreamSlot(ctx, streamSem, func() {
				info := resourceInfo(worker.Resource)
				_ = upsertOffset(ctx, opts.Store, opts.ClusterID, info, opts.Namespace, worker.Summary.ResourceVersion, storage.OffsetEventList, "queued", "")
				watchLog(opts.Logf, "watch stream worker queued", "resource", resourceLabel(worker.Resource), "resourceVersion", emptyLabel(worker.Summary.ResourceVersion), "maxConcurrentStreams", opts.MaxConcurrentStreams)
			})
			if !ok {
				summary.Workers[i].Error = ctx.Err().Error()
				return
			}
			defer release()
			resourceVersion := worker.Summary.ResourceVersion
			watchLog(opts.Logf, "watch stream worker start", "resource", resourceLabel(worker.Resource), "resourceVersion", emptyLabel(resourceVersion))
			streamSummary, err := WatchResourceStreamClientGo(ctx, WatchOptions{
				Context:              opts.Context,
				ClusterID:            opts.ClusterID,
				Resource:             worker.Resource,
				Namespace:            opts.Namespace,
				Store:                opts.Store,
				Logf:                 opts.Logf,
				MaxEvents:            opts.MaxEvents,
				Timeout:              remainingTimeout(deadline),
				StartResourceVersion: resourceVersion,
				MaxRetries:           opts.MaxRetries,
				Filters:              opts.Filters,
				FilterChains:         opts.FilterChains,
				Extractors:           opts.Extractors,
				DisableHTTP2:         opts.DisableHTTP2,
				RetryMinBackoff:      opts.RetryMinBackoff,
				RetryMaxBackoff:      opts.RetryMaxBackoff,
				ProfileRules:         opts.ProfileRules,
			})
			mergeWatchSummary(worker.Summary, streamSummary)
			if err != nil {
				summary.Workers[i].Error = err.Error()
				watchLog(opts.Logf, "watch stream worker error", "resource", resourceLabel(worker.Resource), "error", err)
				return
			}
			watchLog(opts.Logf, "watch stream worker done", "resource", resourceLabel(worker.Resource), "events", streamSummary.Events, "bookmarks", streamSummary.Bookmarks, "stored", streamSummary.Stored)
		}()
	}
	wg.Wait()
}

func WatchResourceInitialListClientGo(ctx context.Context, opts WatchOptions) (WatchSummary, error) {
	if opts.MaxRetries < 0 {
		opts.MaxRetries = -1
	}
	opts.RetryMinBackoff, opts.RetryMaxBackoff = normalizedBackoff(opts.RetryMinBackoff, opts.RetryMaxBackoff)
	prepared, err := prepareWatchResourceClientGo(ctx, opts)
	if err != nil {
		return WatchSummary{}, err
	}
	opts.Context = prepared.Context
	opts.ClusterID = prepared.ClusterID
	opts.Resource = prepared.Resource
	summary := WatchSummary{
		Context:   prepared.Context,
		ClusterID: prepared.ClusterID,
		Resource:  prepared.Resource,
		Namespace: opts.Namespace,
	}
	resourceVersion := opts.StartResourceVersion
	attempts := 0
	for {
		err := listResourceOnce(ctx, prepared.Client, opts, prepared.Info, resourceVersion, &summary)
		if err == nil {
			return summary, nil
		}
		summary.WatchErrors++
		_ = upsertOffset(ctx, opts.Store, prepared.ClusterID, prepared.Info, opts.Namespace, summary.ResourceVersion, storage.OffsetEventList, "list_error", err.Error())
		attempts++
		if opts.MaxRetries >= 0 && attempts > opts.MaxRetries {
			return summary, err
		}
		if isResourceVersionExpired(err) {
			summary.Relists++
			resourceVersion = ""
			watchLog(opts.Logf, "resourceVersion expired during list", "resource", resourceLabel(prepared.Resource), "relist", summary.Relists, "error", err)
		} else {
			summary.Retries++
			resourceVersion = summary.ResourceVersion
			watchLog(opts.Logf, "retry list", "resource", resourceLabel(prepared.Resource), "retries", summary.Retries, "resourceVersion", emptyLabel(resourceVersion), "error", err)
		}
		if err := sleepBeforeRetry(ctx, time.Time{}, attempts, opts.RetryMinBackoff, opts.RetryMaxBackoff); err != nil {
			return summary, err
		}
	}
}

func WatchResourceStreamClientGo(ctx context.Context, opts WatchOptions) (WatchSummary, error) {
	if opts.MaxRetries < 0 {
		opts.MaxRetries = -1
	}
	opts.RetryMinBackoff, opts.RetryMaxBackoff = normalizedBackoff(opts.RetryMinBackoff, opts.RetryMaxBackoff)
	prepared, err := prepareWatchResourceClientGo(ctx, opts)
	if err != nil {
		return WatchSummary{}, err
	}
	opts.Context = prepared.Context
	opts.ClusterID = prepared.ClusterID
	opts.Resource = prepared.Resource
	summary := WatchSummary{
		Context:         prepared.Context,
		ClusterID:       prepared.ClusterID,
		Resource:        prepared.Resource,
		Namespace:       opts.Namespace,
		ResourceVersion: opts.StartResourceVersion,
	}
	deadline := time.Time{}
	if opts.Timeout > 0 {
		deadline = time.Now().Add(opts.Timeout)
	}
	attempts := 0
	needsList := summary.ResourceVersion == ""
	for {
		if needsList {
			if err := listResourceOnce(ctx, prepared.Client, opts, prepared.Info, "", &summary); err != nil {
				return summary, err
			}
			needsList = false
		}
		err := watchResourceStream(ctx, prepared.Client, opts, prepared.Info, deadline, &summary)
		if err == nil {
			return summary, nil
		}
		if isGracefulWatchStop(err, deadline) {
			return summary, nil
		}
		attempts++
		if opts.MaxRetries >= 0 && attempts > opts.MaxRetries {
			summary.WatchErrors++
			_ = upsertOffset(ctx, opts.Store, prepared.ClusterID, prepared.Info, opts.Namespace, summary.ResourceVersion, storage.OffsetEventWatch, "watch_error", err.Error())
			return summary, err
		}
		if isResourceVersionExpired(err) {
			summary.Relists++
			needsList = true
			_ = upsertOffset(ctx, opts.Store, prepared.ClusterID, prepared.Info, opts.Namespace, summary.ResourceVersion, storage.OffsetEventWatch, "retrying", err.Error())
			watchLog(opts.Logf, "resourceVersion expired", "resource", resourceLabel(prepared.Resource), "relist", summary.Relists, "error", err)
		} else if isTransientWatchStreamError(err) {
			summary.Retries++
			_ = upsertOffset(ctx, opts.Store, prepared.ClusterID, prepared.Info, opts.Namespace, summary.ResourceVersion, storage.OffsetEventWatch, "retrying", err.Error())
			watchLog(opts.Logf, "watch stream reconnect", "resource", resourceLabel(prepared.Resource), "retries", summary.Retries, "resourceVersion", emptyLabel(summary.ResourceVersion), "error", err)
		} else {
			summary.WatchErrors++
			summary.Retries++
			_ = upsertOffset(ctx, opts.Store, prepared.ClusterID, prepared.Info, opts.Namespace, summary.ResourceVersion, storage.OffsetEventWatch, "watch_error", err.Error())
			watchLog(opts.Logf, "retry watch", "resource", resourceLabel(prepared.Resource), "retries", summary.Retries, "resourceVersion", emptyLabel(summary.ResourceVersion), "error", err)
		}
		if err := sleepBeforeRetry(ctx, deadline, attempts, opts.RetryMinBackoff, opts.RetryMaxBackoff); err != nil {
			return summary, err
		}
	}
}

func prepareWatchResourceClientGo(ctx context.Context, opts WatchOptions) (preparedWatchResource, error) {
	if opts.Store == nil {
		return preparedWatchResource{}, errWatchRequiresStore()
	}
	if opts.Context == "" {
		watchLog(opts.Logf, "resolving current kubeconfig context")
		current, err := CurrentContextClientGo()
		if err != nil {
			return preparedWatchResource{}, err
		}
		opts.Context = current
	}
	if opts.ClusterID == "" {
		identity, err := ResolveClusterIdentityClientGo(ctx, opts.Context)
		if err != nil {
			return preparedWatchResource{}, err
		}
		opts.ClusterID = identity.ID
		if clusterStore, ok := opts.Store.(storage.ClusterStore); ok {
			if err := clusterStore.UpsertCluster(ctx, storage.ClusterRecord{
				Name:   identity.ID,
				UID:    identity.UID,
				Source: clusterSource(identity),
			}); err != nil {
				return preparedWatchResource{}, err
			}
		}
		watchLog(opts.Logf, "resolved cluster", "context", opts.Context, "clusterId", opts.ClusterID, "uid", emptyLabel(identity.UID))
	}
	resolved, err := resolveResourceClientGo(ctx, opts.Context, opts.Resource)
	if err != nil {
		return preparedWatchResource{}, err
	}
	watchLog(opts.Logf, "resolved resource", "resource", resourceLabel(resolved), "apiVersion", resourceAPIVersion(resolved), "namespaced", resolved.Namespaced)
	config, err := watchRestConfig(opts.Context, opts.DisableHTTP2)
	if err != nil {
		return preparedWatchResource{}, err
	}
	client, err := dynamic.NewForConfig(config)
	if err != nil {
		return preparedWatchResource{}, err
	}
	info := resourceInfo(resolved)
	if apiStore, ok := opts.Store.(storage.APIResourceStore); ok {
		if err := apiStore.UpsertAPIResources(ctx, []kubeapi.ResourceInfo{info}, time.Now().UTC()); err != nil {
			return preparedWatchResource{}, err
		}
	}
	gvr := schema.GroupVersionResource{Group: resolved.Group, Version: resolved.Version, Resource: resolved.Resource}
	namespaceableClient := client.Resource(gvr)
	resourceClient := dynamic.ResourceInterface(namespaceableClient)
	if resolved.Namespaced {
		resourceClient = namespaceableClient.Namespace(namespaceForWatch(opts.Namespace))
	}
	return preparedWatchResource{
		Context:   opts.Context,
		ClusterID: opts.ClusterID,
		Resource:  resolved,
		Info:      info,
		Client:    resourceClient,
	}, nil
}

func errWatchRequiresStore() error {
	return errWatchStoreRequired
}

func shouldStopBeforeWatch(ctx context.Context, deadline time.Time, opts WatchResourcesOptions, summary WatchResourcesSummary) bool {
	if ctx.Err() != nil {
		return true
	}
	if !deadline.IsZero() && time.Now().After(deadline) {
		return true
	}
	return summary.Completed == 0 && summary.Errors > 0
}

func aggregateWatchResourcesSummary(summary *WatchResourcesSummary) {
	summary.Completed = 0
	summary.Errors = 0
	summary.Listed = 0
	summary.Events = 0
	summary.Stored = 0
	summary.Bookmarks = 0
	summary.Deleted = 0
	summary.ReconciledDeleted = 0
	summary.UnknownVisibility = 0
	summary.Relists = 0
	summary.Retries = 0
	summary.WatchErrors = 0
	summary.Ingest = ingest.Summary{}
	summary.QueueWaitMS = 0
	summary.BackpressureEvents = 0
	for _, worker := range summary.Workers {
		summary.QueueWaitMS += worker.QueueWaitMS
		if worker.Queued {
			summary.BackpressureEvents++
		}
		if worker.Error != "" {
			summary.Errors++
			continue
		}
		if worker.Summary == nil {
			continue
		}
		summary.Completed++
		addWatchSummaryTotals(summary, *worker.Summary)
	}
	summary.ResourceQueue = resourceQueueMetrics(summary.Workers, summary.Concurrency, summary.ProfileRules)
}

func addWatchSummaryTotals(summary *WatchResourcesSummary, worker WatchSummary) {
	summary.Listed += worker.Listed
	summary.Events += worker.Events
	summary.Stored += worker.Stored
	summary.Bookmarks += worker.Bookmarks
	summary.Deleted += worker.Deleted
	summary.ReconciledDeleted += worker.ReconciledDeleted
	summary.UnknownVisibility += worker.UnknownVisibility
	summary.Relists += worker.Relists
	summary.Retries += worker.Retries
	summary.WatchErrors += worker.WatchErrors
	summary.Ingest = addIngest(summary.Ingest, worker.Ingest)
}

func mergeWatchSummary(base *WatchSummary, extra WatchSummary) {
	base.Listed += extra.Listed
	base.Events += extra.Events
	base.Stored += extra.Stored
	base.Bookmarks += extra.Bookmarks
	base.Deleted += extra.Deleted
	base.ReconciledDeleted += extra.ReconciledDeleted
	base.UnknownVisibility += extra.UnknownVisibility
	base.Relists += extra.Relists
	base.Retries += extra.Retries
	base.WatchErrors += extra.WatchErrors
	if extra.ResourceVersion != "" {
		base.ResourceVersion = extra.ResourceVersion
	}
	base.Ingest = addIngest(base.Ingest, extra.Ingest)
}

func remainingTimeout(deadline time.Time) time.Duration {
	if deadline.IsZero() {
		return 0
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return time.Nanosecond
	}
	return remaining
}

func watchTimeoutSeconds(deadline time.Time, timeout time.Duration) *int64 {
	if timeout <= 0 {
		return nil
	}
	remaining := timeout
	if !deadline.IsZero() {
		remaining = time.Until(deadline)
	}
	seconds := int64(remaining.Seconds())
	if seconds <= 0 {
		seconds = 1
	}
	return &seconds
}

func isGracefulWatchStop(err error, deadline time.Time) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return errors.Is(err, errWatchClosed) && !deadline.IsZero()
}
