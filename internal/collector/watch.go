package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"kube-insight/internal/core"
	"kube-insight/internal/ingest"
	"kube-insight/internal/kubeapi"
	"kube-insight/internal/resourceprofile"
	"kube-insight/internal/storage"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
)

type WatchOptions struct {
	Context              string
	ClusterID            string
	Resource             Resource
	Namespace            string
	Store                storage.Store
	Logf                 WatchLogFunc
	MaxEvents            int
	Timeout              time.Duration
	StartResourceVersion string
	MaxRetries           int
}

type WatchSummary struct {
	Context           string         `json:"context"`
	ClusterID         string         `json:"clusterId,omitempty"`
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
	ClusterID         string
	Resources         []Resource
	DiscoverResources bool
	Namespace         string
	Store             storage.Store
	Logf              WatchLogFunc
	MaxEvents         int
	Timeout           time.Duration
	Concurrency       int
	MaxRetries        int
}

type WatchLogFunc func(message string, args ...any)

type WatchResourcesSummary struct {
	Context            string               `json:"context"`
	ClusterID          string               `json:"clusterId,omitempty"`
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

var (
	errWatchClosed        = errors.New("watch channel closed")
	errWatchStoreRequired = errors.New("watch requires a store")
)

func WatchResourcesClientGo(ctx context.Context, opts WatchResourcesOptions) (WatchResourcesSummary, error) {
	if opts.Store == nil {
		return WatchResourcesSummary{}, fmt.Errorf("watch requires a store")
	}
	if opts.Context == "" {
		watchLog(opts.Logf, "resolving current kubeconfig context")
		current, err := CurrentContextClientGo()
		if err != nil {
			return WatchResourcesSummary{}, err
		}
		opts.Context = current
	}
	if opts.ClusterID == "" {
		identity, err := ResolveClusterIdentityClientGo(ctx, opts.Context)
		if err != nil {
			return WatchResourcesSummary{}, err
		}
		opts.ClusterID = identity.ID
		if clusterStore, ok := opts.Store.(storage.ClusterStore); ok {
			if err := clusterStore.UpsertCluster(ctx, storage.ClusterRecord{
				Name:   identity.ID,
				UID:    identity.UID,
				Source: clusterSource(identity),
			}); err != nil {
				return WatchResourcesSummary{}, err
			}
		}
		watchLog(opts.Logf, "resolved cluster", "context", opts.Context, "clusterId", opts.ClusterID, "uid", emptyLabel(identity.UID))
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 4
	}
	if opts.MaxRetries < 0 {
		opts.MaxRetries = -1
	}
	resources := opts.Resources
	if opts.DiscoverResources || len(resources) == 0 || needsResourceDiscovery(resources) {
		watchLog(opts.Logf, "discovering watchable resources", "context", opts.Context)
		discovered, err := DiscoverResourcesClientGo(ctx, opts.Context)
		if err != nil && len(discovered) == 0 {
			return WatchResourcesSummary{}, err
		}
		watchLog(opts.Logf, "discovered watchable resources", "resources", len(discovered), "context", opts.Context)
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
	if apiStore, ok := opts.Store.(storage.APIResourceStore); ok {
		infos := make([]kubeapi.ResourceInfo, 0, len(resources))
		for _, resource := range resources {
			infos = append(infos, resourceInfo(resource))
		}
		if err := apiStore.UpsertAPIResources(ctx, infos, time.Now().UTC()); err != nil {
			return WatchResourcesSummary{}, err
		}
		watchLog(opts.Logf, "registered api resources", "resources", len(infos), "context", opts.Context)
	}
	watchLog(opts.Logf, "starting watch", "context", opts.Context, "namespace", watchNamespaceLabel(opts.Namespace), "resources", len(resources), "concurrency", opts.Concurrency, "maxEvents", opts.MaxEvents, "timeout", watchTimeoutLabel(opts.Timeout))

	summary := WatchResourcesSummary{
		Context:       opts.Context,
		ClusterID:     opts.ClusterID,
		Resources:     len(resources),
		Concurrency:   opts.Concurrency,
		MaxQueueDepth: max(0, len(resources)-opts.Concurrency),
		Workers:       make([]WatchWorkerSummary, len(resources)),
	}
	deadline := time.Time{}
	if opts.Timeout > 0 {
		deadline = time.Now().Add(opts.Timeout)
	}
	runInitialListPhase(ctx, opts, resources, deadline, &summary)
	aggregateWatchResourcesSummary(&summary)
	watchLog(opts.Logf, "initial list phase completed", "context", summary.Context, "completed", summary.Completed, "errors", summary.Errors, "listed", summary.Listed, "stored", summary.Stored)
	if shouldStopBeforeWatch(ctx, deadline, opts, summary) {
		watchLog(opts.Logf, "watch finished", "context", summary.Context, "completed", summary.Completed, "errors", summary.Errors, "listed", summary.Listed, "events", summary.Events, "stored", summary.Stored)
		return summary, nil
	}
	runWatchStreamPhase(ctx, opts, deadline, &summary)
	aggregateWatchResourcesSummary(&summary)
	watchLog(opts.Logf, "watch finished", "context", summary.Context, "completed", summary.Completed, "errors", summary.Errors, "listed", summary.Listed, "events", summary.Events, "stored", summary.Stored)
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

func resourceQueuePriority(resource Resource) string {
	profile := resourceprofile.ForResource(resourceInfo(resource))
	return profile.Priority
}

func WatchResourceClientGo(ctx context.Context, opts WatchOptions) (WatchSummary, error) {
	if opts.MaxRetries < 0 {
		opts.MaxRetries = -1
	}
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
	deadline := time.Time{}
	if opts.Timeout > 0 {
		deadline = time.Now().Add(opts.Timeout)
	}
	resourceVersion := opts.StartResourceVersion
	attempts := 0
	for {
		watchLog(opts.Logf, "list resource", "resource", resourceLabel(prepared.Resource), "namespace", watchNamespaceLabel(opts.Namespace), "resourceVersion", emptyLabel(resourceVersion))
		err := listAndWatchOnce(ctx, prepared.Client, opts, prepared.Info, deadline, resourceVersion, &summary)
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
			resourceVersion = ""
			_ = upsertOffset(ctx, opts.Store, prepared.ClusterID, prepared.Info, opts.Namespace, summary.ResourceVersion, storage.OffsetEventWatch, "retrying", err.Error())
			watchLog(opts.Logf, "resourceVersion expired", "resource", resourceLabel(prepared.Resource), "relist", summary.Relists, "error", err)
		} else if isTransientWatchStreamError(err) {
			summary.Retries++
			resourceVersion = summary.ResourceVersion
			_ = upsertOffset(ctx, opts.Store, prepared.ClusterID, prepared.Info, opts.Namespace, summary.ResourceVersion, storage.OffsetEventWatch, "retrying", err.Error())
			watchLog(opts.Logf, "watch stream reconnect", "resource", resourceLabel(prepared.Resource), "retries", summary.Retries, "resourceVersion", emptyLabel(resourceVersion), "error", err)
		} else {
			summary.WatchErrors++
			summary.Retries++
			resourceVersion = summary.ResourceVersion
			_ = upsertOffset(ctx, opts.Store, prepared.ClusterID, prepared.Info, opts.Namespace, summary.ResourceVersion, storage.OffsetEventWatch, "watch_error", err.Error())
			watchLog(opts.Logf, "retry watch", "resource", resourceLabel(prepared.Resource), "retries", summary.Retries, "resourceVersion", emptyLabel(resourceVersion), "error", err)
		}
		if err := sleepBeforeRetry(ctx, deadline, attempts); err != nil {
			return summary, err
		}
	}
}

func listAndWatchOnce(ctx context.Context, resourceClient dynamic.ResourceInterface, opts WatchOptions, info kubeapi.ResourceInfo, deadline time.Time, resourceVersion string, summary *WatchSummary) error {
	if err := listResourceOnce(ctx, resourceClient, opts, info, resourceVersion, summary); err != nil {
		return err
	}
	return watchResourceStream(ctx, resourceClient, opts, info, deadline, summary)
}

func listResourceOnce(ctx context.Context, resourceClient dynamic.ResourceInterface, opts WatchOptions, info kubeapi.ResourceInfo, resourceVersion string, summary *WatchSummary) error {
	previousRefs, err := latestResourceRefs(ctx, opts.Store, opts.ClusterID, info, opts.Namespace)
	if err != nil {
		return err
	}
	list, err := resourceClient.List(ctx, metav1.ListOptions{ResourceVersion: resourceVersion})
	if err != nil {
		return err
	}
	summary.Listed += len(list.Items)
	summary.ResourceVersion = list.GetResourceVersion()
	watchLog(opts.Logf, "listed resource", "resource", info.Resource, "namespace", watchNamespaceLabel(opts.Namespace), "items", len(list.Items), "resourceVersion", emptyLabel(summary.ResourceVersion))
	if listData, err := json.Marshal(list); err != nil {
		return err
	} else {
		ingested, err := ingestList(ctx, opts.Store, opts.ClusterID, listData)
		if err != nil {
			return err
		}
		summary.Ingest = addIngest(summary.Ingest, ingested)
		summary.Stored += ingested.StoredObservations
	}
	reconciled, err := reconcileDeletedFromList(ctx, opts.Store, opts.ClusterID, info, opts.Namespace, summary.ResourceVersion, previousRefs, list)
	if err != nil {
		return err
	}
	summary.Deleted += reconciled.Deleted
	summary.ReconciledDeleted += reconciled.Deleted
	summary.UnknownVisibility += reconciled.UnknownVisibility
	summary.Stored += reconciled.Ingest.StoredObservations
	summary.Ingest = addIngest(summary.Ingest, reconciled.Ingest)
	if err := upsertOffset(ctx, opts.Store, opts.ClusterID, info, opts.Namespace, summary.ResourceVersion, storage.OffsetEventList, "listed", ""); err != nil {
		return err
	}
	return nil
}

func watchResourceStream(ctx context.Context, resourceClient dynamic.ResourceInterface, opts WatchOptions, info kubeapi.ResourceInfo, deadline time.Time, summary *WatchSummary) error {
	watchLog(opts.Logf, "watching resource", "resource", info.Resource, "namespace", watchNamespaceLabel(opts.Namespace), "resourceVersion", emptyLabel(summary.ResourceVersion))
	watchOptions := metav1.ListOptions{
		ResourceVersion:     summary.ResourceVersion,
		AllowWatchBookmarks: true,
	}
	if timeoutSeconds := watchTimeoutSeconds(deadline, opts.Timeout); timeoutSeconds != nil {
		watchOptions.TimeoutSeconds = timeoutSeconds
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
			if err := handleWatchEvent(ctx, opts.Store, opts.ClusterID, info, opts.Namespace, event, summary, opts.Logf); err != nil {
				return err
			}
		}
	}
}

func handleWatchEvent(ctx context.Context, store storage.Store, clusterID string, info kubeapi.ResourceInfo, namespace string, event watch.Event, summary *WatchSummary, logf WatchLogFunc) error {
	if event.Type == watch.Error {
		err := apierrors.FromObject(event.Object)
		if err == nil {
			err = fmt.Errorf("watch error event")
		}
		if isTransientWatchStreamError(err) {
			watchLog(logf, "watch stream interrupted", "resource", info.Resource, "error", err)
		} else {
			watchLog(logf, "event error", "resource", info.Resource, "error", err)
		}
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
		watchLog(logf, "bookmark", "resource", info.Resource, "resourceVersion", emptyLabel(summary.ResourceVersion), "bookmarks", summary.Bookmarks)
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
	watchLog(logf, "event", "type", event.Type, "resource", info.Resource, "namespace", emptyLabel(obj.GetNamespace()), "name", obj.GetName(), "resourceVersion", emptyLabel(summary.ResourceVersion), "stored", ingested.StoredObservations, "events", summary.Events)
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

func isTransientWatchStreamError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, errWatchClosed) || errors.Is(err, io.EOF) {
		return true
	}
	if apierrors.IsTimeout(err) ||
		apierrors.IsServerTimeout(err) ||
		apierrors.IsTooManyRequests(err) ||
		apierrors.IsServiceUnavailable(err) {
		return true
	}
	text := strings.ToLower(err.Error())
	for _, token := range []string{
		"unable to decode an event from the watch stream",
		"stream error:",
		"internal_error",
		"http2:",
		"received from peer",
		"connection reset by peer",
		"broken pipe",
		"unexpected eof",
		"server closed",
		"use of closed network connection",
		"transport is closing",
		"client connection lost",
	} {
		if strings.Contains(text, token) {
			return true
		}
	}
	return false
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

func needsResourceDiscovery(resources []Resource) bool {
	for _, resource := range resources {
		if resource.Group == "" && resource.Version == "" && resource.Resource == "" {
			return true
		}
		if resource.Kind == "" || len(resource.Verbs) == 0 {
			return true
		}
	}
	return false
}

func enrichWatchResources(base, metadata []Resource) []Resource {
	byKey := map[string]Resource{}
	for _, resource := range metadata {
		for _, key := range resourceLookupKeys(resource) {
			if _, exists := byKey[key]; !exists {
				byKey[key] = resource
			}
		}
	}
	out := make([]Resource, len(base))
	for i, resource := range base {
		for _, key := range resourceLookupKeys(resource) {
			if enriched, ok := byKey[key]; ok {
				out[i] = enriched
				goto next
			}
		}
		out[i] = resource
	next:
	}
	return out
}

func resourceLookupKeys(resource Resource) []string {
	var keys []string
	appendKey := func(key string) {
		if key == "" {
			return
		}
		for _, existing := range keys {
			if existing == key {
				return
			}
		}
		keys = append(keys, key)
	}
	appendKey(resource.Name)
	appendKey(resource.Resource)
	if resource.Group != "" && resource.Resource != "" {
		appendKey(resource.Resource + "." + resource.Group)
	}
	if resource.Group != "" && resource.Name != "" {
		appendKey(resource.Name + "." + resource.Group)
	}
	return keys
}
