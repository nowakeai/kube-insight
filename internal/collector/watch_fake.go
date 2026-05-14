package collector

import (
	"context"
	"fmt"
	"time"

	"kube-insight/internal/core"
	"kube-insight/internal/kubeapi"
	"kube-insight/internal/storage"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type FakeWatchReconciliationOptions struct {
	Context     string
	Store       storage.Store
	Namespace   string
	Concurrency int
}

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
		ClusterID:     opts.Context,
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
			ClusterID:         opts.Context,
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

func fakeQueueWaitMS(index, concurrency int) float64 {
	if concurrency <= 0 || index < concurrency {
		return 0
	}
	return float64(index-concurrency+1) * 10
}
