package ingest

import (
	appconfig "kube-insight/internal/config"
	"kube-insight/internal/extractor"
	"kube-insight/internal/processing"
)

func DefaultExtractorRegistry() *extractor.Registry {
	registry, err := ExtractorRegistryFromProcessing(appconfig.Default().Processing)
	if err != nil {
		panic(err)
	}
	return registry
}

func ExtractorRegistryFromProcessing(cfg appconfig.ProcessingConfig) (*extractor.Registry, error) {
	if len(cfg.Extractors) == 0 {
		return extractor.NewScopedRegistryWithSets(cfg.ExtractorSets), nil
	}
	entries := make([]extractor.Entry, 0, len(cfg.Extractors))
	for name, cfg := range cfg.Extractors {
		if cfg.Enabled != nil && !*cfg.Enabled {
			continue
		}
		builtIn, err := processing.NewBuiltInExtractor(processingComponentType(name, cfg.Type))
		if err != nil {
			return nil, err
		}
		entries = append(entries, extractor.Entry{
			Name:      name,
			Extractor: builtIn,
			Scope: extractor.Scope{
				Resources: cfg.Guard.Resources,
				Kinds:     cfg.Guard.Kinds,
			},
		})
	}
	return extractor.NewScopedRegistryWithSets(cfg.ExtractorSets, entries...), nil
}
