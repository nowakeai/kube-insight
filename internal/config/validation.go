package config

import (
	"errors"
	"fmt"
	"strings"

	"kube-insight/internal/processing"
)

func (c Config) Validate() error {
	if c.Version == "" {
		return errors.New("version is required")
	}
	role := c.Instance.Role
	if role == "" {
		role = "all"
	}
	switch role {
	case "all", "writer", "api":
	default:
		return fmt.Errorf("unsupported instance.role %q", c.Instance.Role)
	}
	switch strings.ToLower(c.Logging.Level) {
	case "", "debug", "info", "warn", "warning", "error":
	default:
		return fmt.Errorf("unsupported logging.level %q", c.Logging.Level)
	}
	switch strings.ToLower(c.Logging.Format) {
	case "", "text", "json", "logfmt":
	default:
		return fmt.Errorf("unsupported logging.format %q", c.Logging.Format)
	}
	switch c.Storage.Driver {
	case "sqlite":
		if c.Storage.SQLite.Path == "" {
			return errors.New("storage.sqlite.path is required when driver is sqlite")
		}
	case "postgres":
		if c.Storage.Postgres.DSNEnv == "" {
			return errors.New("postgres dsnEnv is required")
		}
	case "cockroach":
		if c.Storage.Cockroach.DSNEnv == "" {
			return errors.New("cockroach dsnEnv is required")
		}
	default:
		return fmt.Errorf("unsupported storage.driver %q", c.Storage.Driver)
	}
	if role == "api" && c.Collection.Enabled {
		return errors.New("collection.enabled must be false when instance.role is api")
	}
	if role == "writer" && (c.Server.API.Enabled || c.Server.Metrics.Enabled || c.Server.Web.Enabled || c.Server.Chat.Enabled || c.MCP.Enabled) {
		return errors.New("api/metrics/web/chat/mcp listeners must be disabled when instance.role is writer")
	}
	if err := validateCollection(c.Collection); err != nil {
		return err
	}
	if err := validateStorage(c.Storage); err != nil {
		return err
	}
	if err := validateResourceProfiles(c.ResourceProfiles); err != nil {
		return err
	}
	if err := validateProcessing(c.Processing); err != nil {
		return err
	}
	if c.Server.Chat.Enabled && c.Server.Chat.OpenAIAPIKeyEnv == "" {
		return errors.New("server.chat.openaiApiKeyEnv is required when chat is enabled")
	}
	return nil
}

func validateCollection(collection CollectionConfig) error {
	if collection.Concurrency < 0 {
		return errors.New("collection.concurrency must be non-negative")
	}
	if collection.Watch.MinBackoffMillis < 0 {
		return errors.New("collection.watch.minBackoffMillis must be non-negative")
	}
	if collection.Watch.MaxConcurrentStreams < 0 {
		return errors.New("collection.watch.maxConcurrentStreams must be non-negative")
	}
	if collection.Watch.MaxBackoffMillis < 0 {
		return errors.New("collection.watch.maxBackoffMillis must be non-negative")
	}
	if collection.Watch.MinBackoffMillis > 0 && collection.Watch.MaxBackoffMillis > 0 && collection.Watch.MaxBackoffMillis < collection.Watch.MinBackoffMillis {
		return errors.New("collection.watch.maxBackoffMillis must be greater than or equal to minBackoffMillis")
	}
	if collection.Watch.StreamStartStaggerMS < 0 {
		return errors.New("collection.watch.streamStartStaggerMillis must be non-negative")
	}
	return nil
}

func validateStorage(storage StorageConfig) error {
	if storage.Maintenance.Enabled {
		if storage.Maintenance.IntervalSeconds <= 0 {
			return errors.New("storage.maintenance.intervalSeconds must be positive when maintenance is enabled")
		}
		if storage.Maintenance.MinWalBytes < 0 {
			return errors.New("storage.maintenance.minWalBytes must be non-negative")
		}
		if storage.Maintenance.IncrementalVacuumPages < 0 {
			return errors.New("storage.maintenance.incrementalVacuumPages must be non-negative")
		}
		if storage.Maintenance.JournalSizeLimitBytes < 0 {
			return errors.New("storage.maintenance.journalSizeLimitBytes must be non-negative")
		}
	}
	if storage.Retention.MaxAgeSeconds < 0 {
		return errors.New("storage.retention.maxAgeSeconds must be non-negative")
	}
	if storage.Retention.MinVersionsPerObject < 0 {
		return errors.New("storage.retention.minVersionsPerObject must be non-negative")
	}
	if storage.Retention.FilterDecisionMaxAgeSeconds < 0 {
		return errors.New("storage.retention.filterDecisionMaxAgeSeconds must be non-negative")
	}
	retentionPolicyEnabled := false
	for name, policy := range storage.Retention.Policies {
		if err := validateConfigName("storage.retention.policies", name); err != nil {
			return err
		}
		if policy.MaxAgeSeconds < 0 {
			return fmt.Errorf("storage.retention.policies.%s.maxAgeSeconds must be non-negative", name)
		}
		if policy.MinVersionsPerObject < 0 {
			return fmt.Errorf("storage.retention.policies.%s.minVersionsPerObject must be non-negative", name)
		}
		if policy.MaxAgeSeconds > 0 {
			retentionPolicyEnabled = true
		}
	}
	if storage.Retention.Enabled && storage.Retention.MaxAgeSeconds <= 0 && storage.Retention.FilterDecisionMaxAgeSeconds <= 0 && !retentionPolicyEnabled {
		return errors.New("storage.retention.maxAgeSeconds or filterDecisionMaxAgeSeconds must be positive when retention is enabled")
	}
	return nil
}

func validateProcessing(cfg ProcessingConfig) error {
	for name, filter := range cfg.Filters {
		if err := validateConfigName("processing.filters", name); err != nil {
			return err
		}
		filterType := processingComponentType(name, filter.Type)
		expectedAction, ok := processing.BuiltInFilterAction(filterType)
		if !ok {
			return fmt.Errorf("processing.filters.%s has unsupported built-in filter %q; supported: %s", name, filterType, strings.Join(processing.BuiltInFilterNames(), ", "))
		}
		switch filter.Action {
		case "", "keep", "keep_modified", "discard_change", "discard_resource":
		default:
			return fmt.Errorf("processing.filters.%s has unsupported action %q", name, filter.Action)
		}
		if filter.Action != "" && filter.Action != expectedAction {
			return fmt.Errorf("processing.filters.%s action %q does not match built-in action %q", name, filter.Action, expectedAction)
		}
	}
	for chainName, filterNames := range cfg.FilterChains {
		if err := validateConfigName("processing.filterChains", chainName); err != nil {
			return err
		}
		for _, filterName := range filterNames {
			if _, ok := cfg.Filters[filterName]; !ok {
				return fmt.Errorf("processing.filterChains.%s references unknown filter %q", chainName, filterName)
			}
		}
	}
	for name, extractor := range cfg.Extractors {
		if err := validateConfigName("processing.extractors", name); err != nil {
			return err
		}
		extractorType := processingComponentType(name, extractor.Type)
		if !processing.IsBuiltInExtractor(extractorType) {
			return fmt.Errorf("processing.extractors.%s has unsupported built-in extractor %q; supported: %s", name, extractorType, strings.Join(processing.BuiltInExtractorNames(), ", "))
		}
	}
	for setName, extractorNames := range cfg.ExtractorSets {
		if err := validateConfigName("processing.extractorSets", setName); err != nil {
			return err
		}
		for _, extractorName := range extractorNames {
			if _, ok := cfg.Extractors[extractorName]; !ok {
				return fmt.Errorf("processing.extractorSets.%s references unknown extractor %q", setName, extractorName)
			}
		}
	}
	return nil
}

func processingComponentType(name, typ string) string {
	if typ == "" || typ == "builtin" {
		return name
	}
	return typ
}

func validateConfigName(path, name string) error {
	if name == "" {
		return fmt.Errorf("%s must not contain an empty name", path)
	}
	if !isLowerSnakeName(name) {
		return fmt.Errorf("%s.%s must use lower_snake_case", path, name)
	}
	return nil
}

func isLowerSnakeName(name string) bool {
	if name == "" || name[0] < 'a' || name[0] > 'z' {
		return false
	}
	prevUnderscore := false
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			prevUnderscore = false
		case r >= '0' && r <= '9':
			prevUnderscore = false
		case r == '_':
			if prevUnderscore {
				return false
			}
			prevUnderscore = true
		default:
			return false
		}
	}
	return !prevUnderscore
}
