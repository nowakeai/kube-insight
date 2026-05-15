package extractor

import (
	"context"
	"hash/fnv"
	"strconv"

	"kube-insight/internal/core"
)

type EventExtractor struct{}

func (EventExtractor) Kind() string { return "Event" }

func (EventExtractor) Extract(ctx context.Context, obs core.Observation) (Evidence, error) {
	var out Evidence
	if reason, ok := stringAt(obs.Object, "reason"); ok {
		out.Facts = append(out.Facts, fact(obs, "k8s_event.reason", reason, 40))
	}
	if typ, ok := stringAt(obs.Object, "type"); ok {
		out.Facts = append(out.Facts, fact(obs, "k8s_event.type", typ, 20))
	}
	if message, ok := stringAt(obs.Object, "message"); ok {
		out.Facts = append(out.Facts, fact(obs, "k8s_event.message_fingerprint", fingerprint(message), 20))
	} else if note, ok := stringAt(obs.Object, "note"); ok {
		out.Facts = append(out.Facts, fact(obs, "k8s_event.message_fingerprint", fingerprint(note), 20))
	}
	if count, ok := numericAt(obs.Object, "count"); ok {
		value := strconv.FormatFloat(count, 'f', -1, 64)
		out.Facts = append(out.Facts, core.Fact{
			Time:         obs.ObservedAt,
			ObjectID:     objectID(obs),
			Key:          "k8s_event.count",
			Value:        value,
			NumericValue: &count,
			Severity:     20,
		})
		out.Changes = append(out.Changes, change(obs, "event_rollup", "count", "replace", "", value, 20))
	}
	if ref, ok := objectRefAt(ctx, obs.Object, "involvedObject", obs.Ref.Namespace); ok {
		if edge, ok := edgeToRef(ctx, obs, "event_involves_object", ref); ok {
			out.Edges = append(out.Edges, edge)
		}
	}
	if ref, ok := objectRefAt(ctx, obs.Object, "regarding", obs.Ref.Namespace); ok {
		if edge, ok := edgeToRef(ctx, obs, "event_regarding_object", ref); ok {
			out.Edges = append(out.Edges, edge)
		}
	}
	if ref, ok := objectRefAt(ctx, obs.Object, "related", obs.Ref.Namespace); ok {
		if edge, ok := edgeToRef(ctx, obs, "event_related_object", ref); ok {
			out.Edges = append(out.Edges, edge)
		}
	}
	return out, nil
}

func objectRefAt(ctx context.Context, obj map[string]any, key, defaultNamespace string) (targetObjectRef, bool) {
	ref, ok := obj[key].(map[string]any)
	if !ok {
		return targetObjectRef{}, false
	}
	return objectRefFromMap(ctx, ref, defaultNamespace)
}

func fingerprint(value string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(value))
	return strconv.FormatUint(h.Sum64(), 16)
}
