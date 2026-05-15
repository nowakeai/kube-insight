package processing

import (
	"fmt"
	"sort"

	"kube-insight/internal/extractor"
	"kube-insight/internal/filter"
)

type FilterOptions struct {
	Name           string
	RemovePaths    []string
	KeepSecretKeys bool
	Script         string
}

type builtInFilterDefinition struct {
	Action string
	Build  func(FilterOptions) filter.Filter
}

type BuiltInFilter struct {
	Name   string `json:"name" yaml:"name"`
	Action string `json:"action" yaml:"action"`
}

type BuiltInExtractor struct {
	Name string `json:"name" yaml:"name"`
}

var builtInFilters = map[string]builtInFilterDefinition{
	"secret_metadata_only": {
		Action: string(filter.KeepModified),
		Build: func(FilterOptions) filter.Filter {
			return filter.SecretRedactionFilter{}
		},
	},
	"secret_redaction_filter": {
		Action: string(filter.KeepModified),
		Build: func(FilterOptions) filter.Filter {
			return filter.SecretRedactionFilter{}
		},
	},
	"managed_fields": {
		Action: string(filter.KeepModified),
		Build: func(opts FilterOptions) filter.Filter {
			if len(opts.RemovePaths) > 0 {
				return pathRemoveFilter(opts, "managed_fields_removed")
			}
			return filter.ManagedFieldsFilter{}
		},
	},
	"metadata_normalization_filter": {
		Action: string(filter.KeepModified),
		Build: func(opts FilterOptions) filter.Filter {
			if len(opts.RemovePaths) > 0 {
				return pathRemoveFilter(opts, "managed_fields_removed")
			}
			return filter.ManagedFieldsFilter{}
		},
	},
	"resource_version": {
		Action: string(filter.KeepModified),
		Build: func(opts FilterOptions) filter.Filter {
			if len(opts.RemovePaths) > 0 {
				return pathRemoveFilter(opts, "resource_version_removed")
			}
			return filter.ResourceVersionFilter{}
		},
	},
	"resource_version_normalization_filter": {
		Action: string(filter.KeepModified),
		Build: func(opts FilterOptions) filter.Filter {
			if len(opts.RemovePaths) > 0 {
				return pathRemoveFilter(opts, "resource_version_removed")
			}
			return filter.ResourceVersionFilter{}
		},
	},
	"status_condition_timestamps": {
		Action: string(filter.KeepModified),
		Build: func(opts FilterOptions) filter.Filter {
			if len(opts.RemovePaths) > 0 {
				return pathRemoveFilter(opts, "condition_timestamps_removed")
			}
			return filter.StatusConditionTimestampFilter{}
		},
	},
	"status_condition_timestamp_normalization_filter": {
		Action: string(filter.KeepModified),
		Build: func(opts FilterOptions) filter.Filter {
			if len(opts.RemovePaths) > 0 {
				return pathRemoveFilter(opts, "condition_timestamps_removed")
			}
			return filter.StatusConditionTimestampFilter{}
		},
	},
	"status_condition_set": {
		Action: string(filter.KeepModified),
		Build: func(FilterOptions) filter.Filter {
			return filter.StatusConditionSetFilter{}
		},
	},
	"status_condition_set_normalization_filter": {
		Action: string(filter.KeepModified),
		Build: func(FilterOptions) filter.Filter {
			return filter.StatusConditionSetFilter{}
		},
	},
	"metadata_generation": {
		Action: string(filter.KeepModified),
		Build: func(opts FilterOptions) filter.Filter {
			return pathRemoveFilter(opts, "metadata_generation_removed")
		},
	},
	"metadata_generation_normalization_filter": {
		Action: string(filter.KeepModified),
		Build: func(opts FilterOptions) filter.Filter {
			return pathRemoveFilter(opts, "metadata_generation_removed")
		},
	},
	"leader_election_configmap": {
		Action: string(filter.KeepModified),
		Build: func(opts FilterOptions) filter.Filter {
			if len(opts.RemovePaths) > 0 {
				return pathRemoveFilter(opts, "leader_annotation_removed")
			}
			return filter.LeaderElectionConfigMapFilter{}
		},
	},
	"leader_election_configmap_normalization_filter": {
		Action: string(filter.KeepModified),
		Build: func(opts FilterOptions) filter.Filter {
			if len(opts.RemovePaths) > 0 {
				return pathRemoveFilter(opts, "leader_annotation_removed")
			}
			return filter.LeaderElectionConfigMapFilter{}
		},
	},
	"event_series": {
		Action: string(filter.KeepModified),
		Build: func(opts FilterOptions) filter.Filter {
			return pathRemoveFilter(opts, "event_series_removed")
		},
	},
	"event_series_normalization_filter": {
		Action: string(filter.KeepModified),
		Build: func(opts FilterOptions) filter.Filter {
			return pathRemoveFilter(opts, "event_series_removed")
		},
	},
	"cluster_autoscaler_status": {
		Action: string(filter.KeepModified),
		Build: func(FilterOptions) filter.Filter {
			return filter.ClusterAutoscalerStatusFilter{}
		},
	},
	"cluster_autoscaler_status_normalization_filter": {
		Action: string(filter.KeepModified),
		Build: func(FilterOptions) filter.Filter {
			return filter.ClusterAutoscalerStatusFilter{}
		},
	},
	"gke_webhook_heartbeat": {
		Action: string(filter.KeepModified),
		Build: func(FilterOptions) filter.Filter {
			return filter.GKEWebhookHeartbeatFilter{}
		},
	},
	"gke_webhook_heartbeat_normalization_filter": {
		Action: string(filter.KeepModified),
		Build: func(FilterOptions) filter.Filter {
			return filter.GKEWebhookHeartbeatFilter{}
		},
	},
	"lease_skip": {
		Action: string(filter.DiscardResource),
		Build: func(FilterOptions) filter.Filter {
			return filter.LeaseSkipFilter{}
		},
	},
	"lease_skip_filter": {
		Action: string(filter.DiscardResource),
		Build: func(FilterOptions) filter.Filter {
			return filter.LeaseSkipFilter{}
		},
	},
	"report_skip": {
		Action: string(filter.DiscardResource),
		Build: func(FilterOptions) filter.Filter {
			return filter.StaticDecisionFilter{FilterName: "report_skip_filter", Outcome: filter.DiscardResource, Reason: "report_skipped"}
		},
	},
	"report_skip_filter": {
		Action: string(filter.DiscardResource),
		Build: func(FilterOptions) filter.Filter {
			return filter.StaticDecisionFilter{FilterName: "report_skip_filter", Outcome: filter.DiscardResource, Reason: "report_skipped"}
		},
	},
}

var builtInExtractors = map[string]func() extractor.Extractor{
	"reference": func() extractor.Extractor { return extractor.ReferenceExtractor{} },
	"pod":       func() extractor.Extractor { return extractor.PodExtractor{} },
	"node":      func() extractor.Extractor { return extractor.NodeExtractor{} },
	"event":     func() extractor.Extractor { return extractor.EventExtractor{} },
	"endpointslice": func() extractor.Extractor {
		return extractor.EndpointSliceExtractor{}
	},
}

func NewBuiltInFilter(name string, opts FilterOptions) (filter.Filter, string, error) {
	def, ok := builtInFilters[name]
	if !ok {
		return nil, "", fmt.Errorf("unsupported built-in filter %q", name)
	}
	if opts.Name == "" {
		opts.Name = name
	}
	return def.Build(opts), def.Action, nil
}

func BuiltInFilterAction(name string) (string, bool) {
	def, ok := builtInFilters[name]
	return def.Action, ok
}

func BuiltInFilterNames() []string {
	return sortedKeys(builtInFilters)
}

func BuiltInFilterCatalog() []BuiltInFilter {
	names := BuiltInFilterNames()
	out := make([]BuiltInFilter, 0, len(names))
	for _, name := range names {
		out = append(out, BuiltInFilter{Name: name, Action: builtInFilters[name].Action})
	}
	return out
}

func NewBuiltInExtractor(name string) (extractor.Extractor, error) {
	build, ok := builtInExtractors[name]
	if !ok {
		return nil, fmt.Errorf("unsupported built-in extractor %q", name)
	}
	return build(), nil
}

func BuiltInExtractorNames() []string {
	return sortedKeys(builtInExtractors)
}

func BuiltInExtractorCatalog() []BuiltInExtractor {
	names := BuiltInExtractorNames()
	out := make([]BuiltInExtractor, 0, len(names))
	for _, name := range names {
		out = append(out, BuiltInExtractor{Name: name})
	}
	return out
}

func IsBuiltInExtractor(name string) bool {
	_, ok := builtInExtractors[name]
	return ok
}

func pathRemoveFilter(opts FilterOptions, reason string) filter.PathRemoveFilter {
	return filter.PathRemoveFilter{
		FilterName: opts.Name,
		Reason:     reason,
		Paths:      opts.RemovePaths,
	}
}

func sortedKeys[T any](values map[string]T) []string {
	out := make([]string, 0, len(values))
	for key := range values {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
