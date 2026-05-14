package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"kube-insight/internal/core"
	"kube-insight/internal/ingest"
	"kube-insight/internal/kubeapi"
	"kube-insight/internal/resourceprofile"
	"kube-insight/internal/storage"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
)

type WatchOptions struct {
	Context              string
	Resource             Resource
	Namespace            string
	Store                storage.Store
	MaxEvents            int
	Timeout              time.Duration
	StartResourceVersion string
	MaxRetries           int
}

type WatchSummary struct {
	Context           string         `json:"context"`
	Resource          Resource       `json:"resource"`
	Namespace         string         `json:"namespace,omitempty"`
	Listed            int            `json:"listed"`
	Events            int            `json:"events"`
	Stored            int            `json:"stored"`
	Bookmarks         int            `json:"bookmarks"`
	Deleted           int            `json:"deleted"`
	ReconciledDeleted int            `json:"reconciledDeleted"`
	UnknownVisibility int            `json:"unknownVisibility"`
	Relists           int            `json:"relists"`
	Retries           int            `json:"retries"`
	WatchErrors       int            `json:"watchErrors"`
	ResourceVersion   string         `json:"resourceVersion,omitempty"`
	Ingest            ingest.Summary `json:"ingest"`
}

type WatchResourcesOptions struct {
	Context           string
	Resources         []Resource
	DiscoverResources bool
	Namespace         string
	Store             storage.Store
	MaxEvents         int
	Timeout           time.Duration
	Concurrency       int
	MaxRetries        int
}

type WatchResourcesSummary struct {
	Context            string               `json:"context"`
	Resources          int                  `json:"resources"`
	Completed          int                  `json:"completed"`
	Errors             int                  `json:"errors"`
	Listed             int                  `json:"listed"`
	Events             int                  `json:"events"`
	Stored             int                  `json:"stored"`
	Bookmarks          int                  `json:"bookmarks"`
	Deleted            int                  `json:"deleted"`
	ReconciledDeleted  int                  `json:"reconciledDeleted"`
	UnknownVisibility  int                  `json:"unknownVisibility"`
	Relists            int                  `json:"relists"`
	Retries            int                  `json:"retries"`
	WatchErrors        int                  `json:"watchErrors"`
	Ingest             ingest.Summary       `json:"ingest"`
	Workers            []WatchWorkerSummary `json:"workers"`
	ResourceQueue      []WatchResourceQueue `json:"resourceQueue,omitempty"`
	Concurrency        int                  `json:"concurrency"`
	MaxQueueDepth      int                  `json:"maxQueueDepth"`
	BackpressureEvents int                  `json:"backpressureEvents"`
	QueueWaitMS        float64              `json:"queueWaitMs"`
}

type WatchWorkerSummary struct {
	Resource    Resource      `json:"resource"`
	Summary     *WatchSummary `json:"summary,omitempty"`
	Error       string        `json:"error,omitempty"`
	Queued      bool          `json:"queued,omitempty"`
	QueueWaitMS float64       `json:"queueWaitMs,omitempty"`
}

type WatchResourceQueue struct {
	Resource    Resource `json:"resource"`
	Priority    string   `json:"priority"`
	Queued      bool     `json:"queued"`
	QueueDepth  int      `json:"queueDepth"`
	QueueWaitMS float64  `json:"queueWaitMs"`
}

type FakeWatchReconciliationOptions struct {
	Context     string
	Store       storage.Store
	Namespace   string
	Concurrency int
}

var errWatchClosed = errors.New("watch channel closed")

func RunFakeWatchReconciliation(ctx context.Context, opts FakeWatchReconciliationOptions) (WatchResourcesSummary, error) {
	if opts.Store == nil {
		return WatchResourcesSummary{}, fmt.Errorf("fake watch reconciliation requires a store")
	}
	if opts.Context == "" {
		opts.Context = "fake"
	}
	if opts.Namespace == "" {
		opts.Namespace = "default"
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 1
	}
	cases := []struct {
		resource Resource
		previous []core.ResourceRef
		current  []unstructured.Unstructured
	}{
		{
			resource: Resource{Name: "pods", Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true, Verbs: []string{"get", "list", "watch"}},
			previous: []core.ResourceRef{
				{ClusterID: opts.Context, Version: "v1", Resource: "pods", Kind: "Pod", Namespace: opts.Namespace, Name: "keep", UID: "pod-keep"},
				{ClusterID: opts.Context, Version: "v1", Resource: "pods", Kind: "Pod", Namespace: opts.Namespace, Name: "gone", UID: "pod-gone"},
				{ClusterID: opts.Context, Version: "v1", Resource: "pods", Kind: "Pod", Namespace: "other", Name: "hidden", UID: "pod-hidden"},
			},
			current: []unstructured.Unstructured{fakeObject("v1", "Pod", opts.Namespace, "keep", "pod-keep")},
		},
		{
			resource: Resource{Name: "services", Version: "v1", Resource: "services", Kind: "Service", Namespaced: true, Verbs: []string{"get", "list", "watch"}},
			previous: []core.ResourceRef{
				{ClusterID: opts.Context, Version: "v1", Resource: "services", Kind: "Service", Namespace: opts.Namespace, Name: "api", UID: "svc-api"},
				{ClusterID: opts.Context, Version: "v1", Resource: "services", Kind: "Service", Namespace: opts.Namespace, Name: "orphan", UID: "svc-orphan"},
			},
			current: []unstructured.Unstructured{fakeObject("v1", "Service", opts.Namespace, "api", "svc-api")},
		},
	}
	out := WatchResourcesSummary{
		Context:       opts.Context,
		Resources:     len(cases),
		Concurrency:   opts.Concurrency,
		MaxQueueDepth: max(0, len(cases)-opts.Concurrency),
		Workers:       make([]WatchWorkerSummary, len(cases)),
	}
	for i, tc := range cases {
		info := resourceInfo(tc.resource)
		if apiStore, ok := opts.Store.(storage.APIResourceStore); ok {
			if err := apiStore.UpsertAPIResources(ctx, []kubeapi.ResourceInfo{info}, time.Now().UTC()); err != nil {
				return out, err
			}
		}
		list := &unstructured.UnstructuredList{Items: tc.current}
		list.SetResourceVersion(fmt.Sprintf("fake-%d", i+1))
		reconciled, err := reconcileDeletedFromList(ctx, opts.Store, opts.Context, info, opts.Namespace, list.GetResourceVersion(), tc.previous, list)
		summary := WatchSummary{
			Context:           opts.Context,
			Resource:          tc.resource,
			Namespace:         opts.Namespace,
			Listed:            len(tc.current),
			ResourceVersion:   list.GetResourceVersion(),
			Deleted:           reconciled.Deleted,
			ReconciledDeleted: reconciled.Deleted,
			UnknownVisibility: reconciled.UnknownVisibility,
			Stored:            reconciled.Ingest.StoredObservations,
			Ingest:            reconciled.Ingest,
		}
		worker := WatchWorkerSummary{
			Resource:    tc.resource,
			Summary:     &summary,
			Queued:      i >= opts.Concurrency,
			QueueWaitMS: fakeQueueWaitMS(i, opts.Concurrency),
		}
		if err != nil {
			worker.Error = err.Error()
			out.Workers[i] = worker
			out.Errors++
			continue
		}
		out.Workers[i] = worker
		out.Completed++
		out.Listed += summary.Listed
		out.Deleted += summary.Deleted
		out.ReconciledDeleted += summary.ReconciledDeleted
		out.UnknownVisibility += summary.UnknownVisibility
		out.Stored += summary.Stored
		out.Ingest = addIngest(out.Ingest, summary.Ingest)
	}
	out.ResourceQueue = resourceQueueMetrics(out.Workers, out.Concurrency)
	for _, metric := range out.ResourceQueue {
		out.QueueWaitMS += metric.QueueWaitMS
		if metric.Queued {
			out.BackpressureEvents++
		}
	}
	return out, nil
}

func WatchResourcesClientGo(ctx context.Context, opts WatchResourcesOptions) (WatchResourcesSummary, error) {
	if opts.Store == nil {
		return WatchResourcesSummary{}, fmt.Errorf("watch requires a store")
	}
	if opts.Context == "" {
		current, err := CurrentContextClientGo()
		if err != nil {
			return WatchResourcesSummary{}, err
		}
		opts.Context = current
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 4
	}
	if opts.MaxRetries < 0 {
		opts.MaxRetries = 3
	}
	if opts.Timeout <= 0 && opts.MaxEvents <= 0 {
		opts.Timeout = 30 * time.Second
	}

	resources := opts.Resources
	if opts.DiscoverResources || len(resources) == 0 {
		discovered, err := DiscoverResourcesClientGo(ctx, opts.Context)
		if err != nil && len(discovered) == 0 {
			return WatchResourcesSummary{}, err
		}
		if len(resources) == 0 {
			resources = discovered
		} else {
			resources = enrichWatchResources(resources, discovered)
		}
	}
	resources = watchableResources(resources)
	if len(resources) == 0 {
		return WatchResourcesSummary{}, fmt.Errorf("no watchable resources found")
	}

	summary := WatchResourcesSummary{
		Context:       opts.Context,
		Resources:     len(resources),
		Concurrency:   opts.Concurrency,
		MaxQueueDepth: max(0, len(resources)-opts.Concurrency),
		Workers:       make([]WatchWorkerSummary, len(resources)),
	}
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
			workerSummary, err := WatchResourceClientGo(ctx, WatchOptions{
				Context:    opts.Context,
				Resource:   resource,
				Namespace:  opts.Namespace,
				Store:      opts.Store,
				MaxEvents:  opts.MaxEvents,
				Timeout:    opts.Timeout,
				MaxRetries: opts.MaxRetries,
			})
			if err != nil {
				summary.Workers[i].Error = err.Error()
			}
			summary.Workers[i].Summary = &workerSummary
		}()
	}
	wg.Wait()
	for _, worker := range summary.Workers {
		summary.QueueWaitMS += worker.QueueWaitMS
		if worker.Queued {
			summary.BackpressureEvents++
		}
		if worker.Error != "" {
			summary.Errors++
		}
		if worker.Summary == nil {
			continue
		}
		summary.Completed++
		summary.Listed += worker.Summary.Listed
		summary.Events += worker.Summary.Events
		summary.Stored += worker.Summary.Stored
		summary.Bookmarks += worker.Summary.Bookmarks
		summary.Deleted += worker.Summary.Deleted
		summary.ReconciledDeleted += worker.Summary.ReconciledDeleted
		summary.UnknownVisibility += worker.Summary.UnknownVisibility
		summary.Relists += worker.Summary.Relists
		summary.Retries += worker.Summary.Retries
		summary.WatchErrors += worker.Summary.WatchErrors
		summary.Ingest = addIngest(summary.Ingest, worker.Summary.Ingest)
	}
	summary.ResourceQueue = resourceQueueMetrics(summary.Workers, summary.Concurrency)
	return summary, nil
}

func durationMillis(start time.Time) float64 {
	return float64(time.Since(start).Microseconds()) / 1000
}

func resourceQueueMetrics(workers []WatchWorkerSummary, concurrency int) []WatchResourceQueue {
	if len(workers) == 0 {
		return nil
	}
	if concurrency <= 0 {
		concurrency = len(workers)
	}
	out := make([]WatchResourceQueue, 0, len(workers))
	for i, worker := range workers {
		out = append(out, WatchResourceQueue{
			Resource:    worker.Resource,
			Priority:    resourceQueuePriority(worker.Resource),
			Queued:      worker.Queued,
			QueueDepth:  max(0, i-concurrency+1),
			QueueWaitMS: worker.QueueWaitMS,
		})
	}
	return out
}

func fakeQueueWaitMS(index, concurrency int) float64 {
	if concurrency <= 0 || index < concurrency {
		return 0
	}
	return float64(index-concurrency+1) * 10
}

func resourceQueuePriority(resource Resource) string {
	profile := resourceprofile.ForResource(resourceInfo(resource))
	return profile.Priority
}

func WatchResourceClientGo(ctx context.Context, opts WatchOptions) (WatchSummary, error) {
	if opts.Store == nil {
		return WatchSummary{}, fmt.Errorf("watch requires a store")
	}
	if opts.Context == "" {
		current, err := CurrentContextClientGo()
		if err != nil {
			return WatchSummary{}, err
		}
		opts.Context = current
	}
	if opts.MaxEvents <= 0 && opts.Timeout <= 0 {
		opts.MaxEvents = 1
	}
	if opts.MaxRetries < 0 {
		opts.MaxRetries = 3
	}

	resolved, err := resolveResourceClientGo(ctx, opts.Context, opts.Resource)
	if err != nil {
		return WatchSummary{}, err
	}
	config, err := restConfig(opts.Context)
	if err != nil {
		return WatchSummary{}, err
	}
	client, err := dynamic.NewForConfig(config)
	if err != nil {
		return WatchSummary{}, err
	}
	info := resourceInfo(resolved)
	if apiStore, ok := opts.Store.(storage.APIResourceStore); ok {
		if err := apiStore.UpsertAPIResources(ctx, []kubeapi.ResourceInfo{info}, time.Now().UTC()); err != nil {
			return WatchSummary{}, err
		}
	}

	gvr := schema.GroupVersionResource{Group: resolved.Group, Version: resolved.Version, Resource: resolved.Resource}
	namespaceableClient := client.Resource(gvr)
	var resourceClient dynamic.ResourceInterface
	if resolved.Namespaced {
		resourceClient = namespaceableClient.Namespace(namespaceForWatch(opts.Namespace))
	} else {
		resourceClient = namespaceableClient
	}
	summary := WatchSummary{
		Context:   opts.Context,
		Resource:  resolved,
		Namespace: opts.Namespace,
	}
	deadline := time.Time{}
	if opts.Timeout > 0 {
		deadline = time.Now().Add(opts.Timeout)
	}
	resourceVersion := opts.StartResourceVersion
	attempts := 0
	for {
		err := listAndWatchOnce(ctx, resourceClient, opts, info, deadline, resourceVersion, &summary)
		if err == nil {
			return summary, nil
		}
		if errors.Is(err, errWatchClosed) && !deadline.IsZero() && time.Now().After(deadline) {
			return summary, nil
		}
		summary.WatchErrors++
		_ = upsertOffset(ctx, opts.Store, opts.Context, info, opts.Namespace, summary.ResourceVersion, storage.OffsetEventWatch, "watch_error", err.Error())
		attempts++
		if attempts > opts.MaxRetries {
			return summary, err
		}
		if isResourceVersionExpired(err) {
			summary.Relists++
			resourceVersion = ""
		} else {
			summary.Retries++
			resourceVersion = summary.ResourceVersion
		}
		if err := sleepBeforeRetry(ctx, deadline, attempts); err != nil {
			return summary, err
		}
	}
}

func listAndWatchOnce(ctx context.Context, resourceClient dynamic.ResourceInterface, opts WatchOptions, info kubeapi.ResourceInfo, deadline time.Time, resourceVersion string, summary *WatchSummary) error {
	previousRefs, err := latestResourceRefs(ctx, opts.Store, opts.Context, info, opts.Namespace)
	if err != nil {
		return err
	}
	list, err := resourceClient.List(ctx, metav1.ListOptions{ResourceVersion: resourceVersion})
	if err != nil {
		return err
	}
	summary.Listed += len(list.Items)
	summary.ResourceVersion = list.GetResourceVersion()
	if listData, err := json.Marshal(list); err != nil {
		return err
	} else {
		ingested, err := ingestList(ctx, opts.Store, opts.Context, listData)
		if err != nil {
			return err
		}
		summary.Ingest = addIngest(summary.Ingest, ingested)
		summary.Stored += ingested.StoredObservations
	}
	reconciled, err := reconcileDeletedFromList(ctx, opts.Store, opts.Context, info, opts.Namespace, summary.ResourceVersion, previousRefs, list)
	if err != nil {
		return err
	}
	summary.Deleted += reconciled.Deleted
	summary.ReconciledDeleted += reconciled.Deleted
	summary.UnknownVisibility += reconciled.UnknownVisibility
	summary.Stored += reconciled.Ingest.StoredObservations
	summary.Ingest = addIngest(summary.Ingest, reconciled.Ingest)
	if err := upsertOffset(ctx, opts.Store, opts.Context, info, opts.Namespace, summary.ResourceVersion, storage.OffsetEventList, "listed", ""); err != nil {
		return err
	}

	watchOptions := metav1.ListOptions{
		ResourceVersion:     summary.ResourceVersion,
		AllowWatchBookmarks: true,
	}
	if opts.Timeout > 0 {
		seconds := int64(opts.Timeout.Seconds())
		if seconds <= 0 {
			seconds = 1
		}
		watchOptions.TimeoutSeconds = &seconds
	}
	watcher, err := resourceClient.Watch(ctx, watchOptions)
	if err != nil {
		return err
	}
	defer watcher.Stop()

	for {
		if opts.MaxEvents > 0 && summary.Events >= opts.MaxEvents {
			return nil
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-watcher.ResultChan():
			if !ok {
				return errWatchClosed
			}
			if err := handleWatchEvent(ctx, opts.Store, opts.Context, info, opts.Namespace, event, summary); err != nil {
				return err
			}
		}
	}
}

func handleWatchEvent(ctx context.Context, store storage.Store, clusterID string, info kubeapi.ResourceInfo, namespace string, event watch.Event, summary *WatchSummary) error {
	if event.Type == watch.Error {
		err := apierrors.FromObject(event.Object)
		if err == nil {
			err = fmt.Errorf("watch error event")
		}
		_ = upsertOffset(ctx, store, clusterID, info, namespace, summary.ResourceVersion, storage.OffsetEventWatch, "watch_error", err.Error())
		return err
	}
	obj, ok := event.Object.(*unstructured.Unstructured)
	if !ok {
		return nil
	}
	resourceVersion := obj.GetResourceVersion()
	if resourceVersion != "" {
		summary.ResourceVersion = resourceVersion
	}
	if event.Type == watch.Bookmark {
		summary.Bookmarks++
		return upsertOffset(ctx, store, clusterID, info, namespace, summary.ResourceVersion, storage.OffsetEventBookmark, "bookmark", "")
	}
	data, err := watchEventJSON(event.Type, obj)
	if err != nil {
		return err
	}
	ingested, err := ingestList(ctx, store, clusterID, data)
	if err != nil {
		return err
	}
	summary.Events++
	if event.Type == watch.Deleted {
		summary.Deleted++
	}
	summary.Stored += ingested.StoredObservations
	summary.Ingest = addIngest(summary.Ingest, ingested)
	return upsertOffset(ctx, store, clusterID, info, namespace, summary.ResourceVersion, storage.OffsetEventWatch, "watching", "")
}

func watchEventJSON(eventType watch.EventType, obj *unstructured.Unstructured) ([]byte, error) {
	payload := map[string]any{
		"type":   string(eventType),
		"object": obj.Object,
	}
	return json.Marshal(payload)
}

func ingestList(ctx context.Context, store storage.Store, clusterID string, data []byte) (ingest.Summary, error) {
	pipeline := ingest.DefaultPipeline(store)
	pipeline.ClusterID = clusterID
	return pipeline.IngestJSON(ctx, data)
}

type reconciliationSummary struct {
	Deleted           int
	UnknownVisibility int
	Ingest            ingest.Summary
}

func latestResourceRefs(ctx context.Context, store storage.Store, clusterID string, info kubeapi.ResourceInfo, namespace string) ([]core.ResourceRef, error) {
	refStore, ok := store.(storage.LatestResourceRefStore)
	if !ok {
		return nil, nil
	}
	return refStore.LatestResourceRefs(ctx, clusterID, info, namespace)
}

func reconcileDeletedFromList(ctx context.Context, store storage.Store, clusterID string, info kubeapi.ResourceInfo, namespace, resourceVersion string, previous []core.ResourceRef, list *unstructured.UnstructuredList) (reconciliationSummary, error) {
	if len(previous) == 0 {
		return reconciliationSummary{}, nil
	}
	current := map[string]bool{}
	for i := range list.Items {
		ref := refFromUnstructured(clusterID, info, &list.Items[i])
		current[resourceRefKey(ref)] = true
	}

	var out reconciliationSummary
	for _, ref := range previous {
		if namespace != "" && ref.Namespace != namespace {
			out.UnknownVisibility++
			continue
		}
		if current[resourceRefKey(ref)] {
			continue
		}
		data, err := deletedObservationJSON(ref, resourceVersion)
		if err != nil {
			return out, err
		}
		summary, err := ingestList(ctx, store, clusterID, data)
		if err != nil {
			return out, err
		}
		out.Deleted++
		out.Ingest = addIngest(out.Ingest, summary)
	}
	return out, nil
}

func refFromUnstructured(clusterID string, info kubeapi.ResourceInfo, obj *unstructured.Unstructured) core.ResourceRef {
	return core.ResourceRef{
		ClusterID: clusterID,
		Group:     info.Group,
		Version:   info.Version,
		Resource:  info.Resource,
		Kind:      info.Kind,
		Namespace: obj.GetNamespace(),
		Name:      obj.GetName(),
		UID:       string(obj.GetUID()),
	}
}

func fakeObject(apiVersion, kind, namespace, name, uid string) unstructured.Unstructured {
	metadata := map[string]any{"name": name}
	if namespace != "" {
		metadata["namespace"] = namespace
	}
	if uid != "" {
		metadata["uid"] = uid
	}
	return unstructured.Unstructured{Object: map[string]any{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata":   metadata,
	}}
}

func deletedObservationJSON(ref core.ResourceRef, resourceVersion string) ([]byte, error) {
	metadata := map[string]any{
		"name": ref.Name,
	}
	if ref.Namespace != "" {
		metadata["namespace"] = ref.Namespace
	}
	if ref.UID != "" {
		metadata["uid"] = ref.UID
	}
	if resourceVersion != "" {
		metadata["resourceVersion"] = resourceVersion
	}
	payload := map[string]any{
		"type": string(watch.Deleted),
		"object": map[string]any{
			"apiVersion": apiVersion(ref.Group, ref.Version),
			"kind":       ref.Kind,
			"metadata":   metadata,
		},
	}
	return json.Marshal(payload)
}

func apiVersion(group, version string) string {
	if group == "" {
		return version
	}
	return group + "/" + version
}

func resourceRefKey(ref core.ResourceRef) string {
	if ref.UID != "" {
		return "uid:" + ref.UID
	}
	return "name:" + ref.Namespace + "/" + ref.Name
}

func upsertOffset(ctx context.Context, store storage.Store, clusterID string, info kubeapi.ResourceInfo, namespace, resourceVersion string, event storage.OffsetEvent, status, errorText string) error {
	offsetStore, ok := store.(storage.IngestionOffsetStore)
	if !ok {
		return nil
	}
	return offsetStore.UpsertIngestionOffset(ctx, storage.IngestionOffset{
		ClusterID:       clusterID,
		Resource:        info,
		Namespace:       namespace,
		ResourceVersion: resourceVersion,
		Event:           event,
		Status:          status,
		Error:           errorText,
		At:              time.Now().UTC(),
	})
}

func resourceInfo(resource Resource) kubeapi.ResourceInfo {
	return kubeapi.ResourceInfo{
		Group:      resource.Group,
		Version:    resource.Version,
		Resource:   resource.Resource,
		Kind:       resource.Kind,
		Namespaced: resource.Namespaced,
		Verbs:      resource.Verbs,
	}
}

func namespaceForWatch(namespace string) string {
	if namespace == "" {
		return metav1.NamespaceAll
	}
	return namespace
}

func addIngest(total, next ingest.Summary) ingest.Summary {
	total.Observations += next.Observations
	total.StoredObservations += next.StoredObservations
	total.ModifiedObservations += next.ModifiedObservations
	total.DiscardedChanges += next.DiscardedChanges
	total.DiscardedResources += next.DiscardedResources
	total.Facts += next.Facts
	total.Edges += next.Edges
	total.Changes += next.Changes
	return total
}

func isResourceVersionExpired(err error) bool {
	if err == nil {
		return false
	}
	if apierrors.IsGone(err) {
		return true
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "too old") || strings.Contains(text, "expired") || strings.Contains(text, "410")
}

func sleepBeforeRetry(ctx context.Context, deadline time.Time, attempt int) error {
	if !deadline.IsZero() && time.Now().After(deadline) {
		return errWatchClosed
	}
	delay := time.Duration(attempt) * 100 * time.Millisecond
	if delay > time.Second {
		delay = time.Second
	}
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

func watchableResources(resources []Resource) []Resource {
	out := make([]Resource, 0, len(resources))
	for _, resource := range resources {
		if len(resource.Verbs) > 0 && !hasVerb(resource.Verbs, "watch") {
			continue
		}
		out = append(out, resource)
	}
	return out
}

func enrichWatchResources(base, metadata []Resource) []Resource {
	byKey := map[string]Resource{}
	for _, resource := range metadata {
		byKey[resource.Name+"\x00"+fmt.Sprint(resource.Namespaced)] = resource
	}
	out := make([]Resource, len(base))
	for i, resource := range base {
		if enriched, ok := byKey[resource.Name+"\x00"+fmt.Sprint(resource.Namespaced)]; ok {
			out[i] = enriched
			continue
		}
		out[i] = resource
	}
	return out
}
