package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"kube-insight/internal/core"
	"kube-insight/internal/extractor"
)

func TestStoreRecordsDeleteObservationTime(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "kube-insight.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ref := core.ResourceRef{ClusterID: "c1", Version: "v1", Resource: "pods", Kind: "Pod", Namespace: "default", Name: "app", UID: "pod-uid"}
	obj := map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata": map[string]any{
			"name":              "app",
			"namespace":         "default",
			"uid":               "pod-uid",
			"deletionTimestamp": "1970-01-01T00:00:09Z",
		},
	}
	if err := store.PutObservation(context.Background(), core.Observation{Type: core.ObservationModified, ObservedAt: time.Unix(10, 0), Ref: ref, Object: obj}, extractor.Evidence{}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutObservation(context.Background(), core.Observation{Type: core.ObservationDeleted, ObservedAt: time.Unix(11, 0), Ref: ref, Object: obj}, extractor.Evidence{}); err != nil {
		t.Fatal(err)
	}

	var deletedAt int64
	if err := store.db.QueryRow(`
select o.deleted_at
from objects o
join clusters c on c.id = o.cluster_id
where c.name = 'c1' and o.uid = 'pod-uid'`).Scan(&deletedAt); err != nil {
		t.Fatal(err)
	}
	if deletedAt != time.Unix(11, 0).UnixMilli() {
		t.Fatalf("deleted_at = %d, want delete observation time", deletedAt)
	}

	var observedAt int64
	if err := store.db.QueryRow(`
select max(oo.observed_at)
from object_observations oo
join objects o on o.id = oo.object_id
where o.uid = 'pod-uid' and oo.observation_type = 'DELETED'`).Scan(&observedAt); err != nil {
		t.Fatal(err)
	}
	if observedAt != time.Unix(11, 0).UnixMilli() {
		t.Fatalf("delete observation observed_at = %d, want delete observation time", observedAt)
	}
}
