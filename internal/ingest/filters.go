package ingest

import (
	"fmt"

	appconfig "kube-insight/internal/config"
	"kube-insight/internal/filter"
	"kube-insight/internal/processing"
	"kube-insight/internal/storage"
)

func DefaultFilterChain() filter.Chain {
	chains, err := FilterChainsFromProcessing(appconfig.Default().Processing)
	if err != nil {
		panic(err)
	}
	return chains.Default
}

func PipelineForAppConfig(store storage.Store, cfg appconfig.Config) (Pipeline, error) {
	pipeline := DefaultPipeline(store)
	chains, err := FilterChainsFromProcessing(cfg.Processing)
	if err != nil {
		return Pipeline{}, err
	}
	extractors, err := ExtractorRegistryFromProcessing(cfg.Processing)
	if err != nil {
		return Pipeline{}, err
	}
	pipeline.FilterChains = chains
	pipeline.Filters = chains.Default
	pipeline.Extractors = extractors
	pipeline.ProfileRules = cfg.ProfileRules()
	return pipeline, nil
}

type FilterChains struct {
	Default filter.Chain
	ByName  map[string]filter.Chain
}

func (chains FilterChains) Select(name string) filter.Chain {
	switch name {
	case "", "default":
		return chains.Default
	case "none":
		return nil
	default:
		if chain, ok := chains.ByName[name]; ok {
			return chain
		}
		return chains.Default
	}
}

func FilterChainsFromProcessing(cfg appconfig.ProcessingConfig) (FilterChains, error) {
	chains := FilterChains{ByName: map[string]filter.Chain{}}
	for chainName, filterNames := range cfg.FilterChains {
		chain, err := filterChainFromProcessingNames(filterNames, cfg.Filters)
		if err != nil {
			return FilterChains{}, fmt.Errorf("processing.filterChains.%s: %w", chainName, err)
		}
		if chainName == "default" {
			chains.Default = chain
			continue
		}
		chains.ByName[chainName] = chain
	}
	if _, ok := cfg.FilterChains["none"]; !ok {
		chains.ByName["none"] = nil
	}
	return chains, nil
}

func filterChainFromProcessingNames(names []string, configs map[string]appconfig.ProcessingFilterConfig) (filter.Chain, error) {
	chain := make(filter.Chain, 0, len(names))
	for _, name := range names {
		cfg, ok := configs[name]
		if !ok {
			return nil, fmt.Errorf("unknown filter %q", name)
		}
		if cfg.Enabled != nil && !*cfg.Enabled {
			continue
		}
		builtIn, expectedAction, err := processing.NewBuiltInFilter(processingComponentType(name, cfg.Type), processing.FilterOptions{
			Name:           processingComponentType(name, cfg.Type),
			RemovePaths:    cfg.RemovePaths,
			KeepSecretKeys: cfg.KeepSecretKeys,
			Script:         cfg.Script,
		})
		if err != nil {
			return nil, err
		}
		if cfg.Action != "" && cfg.Action != expectedAction {
			return nil, fmt.Errorf("filter %q action %q does not match built-in action %q", name, cfg.Action, expectedAction)
		}
		if len(cfg.Guard.Resources) > 0 || len(cfg.Guard.Kinds) > 0 || len(cfg.Guard.Namespaces) > 0 || len(cfg.Guard.Names) > 0 {
			builtIn = filter.ScopedFilter{
				Inner: builtIn,
				Scope: filter.Scope{
					Resources:  cfg.Guard.Resources,
					Kinds:      cfg.Guard.Kinds,
					Namespaces: cfg.Guard.Namespaces,
					Names:      cfg.Guard.Names,
				},
			}
		}
		chain = append(chain, builtIn)
	}
	return chain, nil
}

func processingComponentType(name, typ string) string {
	if typ == "" || typ == "builtin" {
		return name
	}
	return typ
}
