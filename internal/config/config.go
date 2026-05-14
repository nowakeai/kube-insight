package config

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Version    string            `yaml:"version" json:"version"`
	Instance   InstanceConfig    `yaml:"instance" json:"instance"`
	Logging    LoggingConfig     `yaml:"logging" json:"logging"`
	Storage    StorageConfig     `yaml:"storage" json:"storage"`
	Collection CollectionConfig  `yaml:"collection" json:"collection"`
	Filters    []FilterConfig    `yaml:"filters" json:"filters"`
	Extractors []ExtractorConfig `yaml:"extractors" json:"extractors"`
	Plugins    PluginConfig      `yaml:"plugins" json:"plugins"`
	Server     ServerConfig      `yaml:"server" json:"server"`
	MCP        MCPConfig         `yaml:"mcp" json:"mcp"`
	Validation ValidationConfig  `yaml:"validation" json:"validation"`
}

type InstanceConfig struct {
	Role string `yaml:"role" json:"role"`
}

type LoggingConfig struct {
	Level  string `yaml:"level" json:"level"`
	Format string `yaml:"format" json:"format"`
}

type StorageConfig struct {
	Driver      string            `yaml:"driver" json:"driver"`
	SQLite      SQLiteConfig      `yaml:"sqlite" json:"sqlite"`
	Postgres    SQLDatabaseConfig `yaml:"postgres" json:"postgres"`
	Cockroach   SQLDatabaseConfig `yaml:"cockroach" json:"cockroach"`
	Maintenance MaintenanceConfig `yaml:"maintenance" json:"maintenance"`
}

type SQLiteConfig struct {
	Path string `yaml:"path" json:"path"`
}

type MaintenanceConfig struct {
	Enabled                     bool  `yaml:"enabled" json:"enabled"`
	IntervalSeconds             int   `yaml:"intervalSeconds" json:"intervalSeconds"`
	MinWalBytes                 int64 `yaml:"minWalBytes" json:"minWalBytes"`
	IncrementalVacuumPages      int   `yaml:"incrementalVacuumPages" json:"incrementalVacuumPages"`
	JournalSizeLimitBytes       int64 `yaml:"journalSizeLimitBytes" json:"journalSizeLimitBytes"`
	RunOnStart                  bool  `yaml:"runOnStart" json:"runOnStart"`
	SkipWhenDatabaseBusy        bool  `yaml:"skipWhenDatabaseBusy" json:"skipWhenDatabaseBusy"`
	LogUnchangedMaintenanceRuns bool  `yaml:"logUnchangedMaintenanceRuns" json:"logUnchangedMaintenanceRuns"`
}

type SQLDatabaseConfig struct {
	DSNEnv string `yaml:"dsnEnv" json:"dsnEnv"`
}

type CollectionConfig struct {
	Enabled     bool           `yaml:"enabled" json:"enabled"`
	Kubeconfig  string         `yaml:"kubeconfig" json:"kubeconfig"`
	UseClientGo bool           `yaml:"useClientGo" json:"useClientGo"`
	Contexts    []string       `yaml:"contexts" json:"contexts"`
	AllContexts bool           `yaml:"allContexts" json:"allContexts"`
	Namespace   string         `yaml:"namespace" json:"namespace"`
	Concurrency int            `yaml:"concurrency" json:"concurrency"`
	Resources   ResourcePolicy `yaml:"resources" json:"resources"`
}

type ResourcePolicy struct {
	All     bool     `yaml:"all" json:"all"`
	Include []string `yaml:"include" json:"include"`
	Exclude []string `yaml:"exclude" json:"exclude"`
}

type FilterConfig struct {
	Name           string   `yaml:"name" json:"name"`
	Enabled        *bool    `yaml:"enabled" json:"enabled,omitempty"`
	Action         string   `yaml:"action" json:"action"`
	Kinds          []string `yaml:"kinds" json:"kinds"`
	Resources      []string `yaml:"resources" json:"resources"`
	RemovePaths    []string `yaml:"removePaths" json:"removePaths"`
	KeepSecretKeys bool     `yaml:"keepSecretKeys" json:"keepSecretKeys"`
	Script         string   `yaml:"script" json:"script,omitempty"`
}

type ExtractorConfig struct {
	Name      string   `yaml:"name" json:"name"`
	Enabled   *bool    `yaml:"enabled" json:"enabled,omitempty"`
	Kinds     []string `yaml:"kinds" json:"kinds"`
	Resources []string `yaml:"resources" json:"resources"`
	Script    string   `yaml:"script" json:"script,omitempty"`
}

type PluginConfig struct {
	Goja GojaConfig `yaml:"goja" json:"goja"`
}

type GojaConfig struct {
	Enabled     bool     `yaml:"enabled" json:"enabled"`
	Directories []string `yaml:"directories" json:"directories"`
}

type ServerConfig struct {
	API  ListenerConfig `yaml:"api" json:"api"`
	Web  ListenerConfig `yaml:"web" json:"web"`
	Chat ChatConfig     `yaml:"chat" json:"chat"`
}

type ListenerConfig struct {
	Enabled bool   `yaml:"enabled" json:"enabled"`
	Listen  string `yaml:"listen" json:"listen"`
}

type ChatConfig struct {
	Enabled         bool   `yaml:"enabled" json:"enabled"`
	OpenAIAPIKeyEnv string `yaml:"openaiApiKeyEnv" json:"openaiApiKeyEnv"`
	Model           string `yaml:"model" json:"model"`
}

type MCPConfig struct {
	Enabled bool   `yaml:"enabled" json:"enabled"`
	Listen  string `yaml:"listen" json:"listen"`
}

type ValidationConfig struct {
	MaxStoredToRawRatio          float64 `yaml:"maxStoredToRawRatio" json:"maxStoredToRawRatio"`
	MaxLatestLookupP95MS         float64 `yaml:"maxLatestLookupP95Ms" json:"maxLatestLookupP95Ms"`
	MaxHistoricalGetP95MS        float64 `yaml:"maxHistoricalGetP95Ms" json:"maxHistoricalGetP95Ms"`
	MaxServiceInvestigationP95MS float64 `yaml:"maxServiceInvestigationP95Ms" json:"maxServiceInvestigationP95Ms"`
	MinServiceVersions           int     `yaml:"minServiceVersions" json:"minServiceVersions"`
	MinServiceDiffs              int     `yaml:"minServiceDiffs" json:"minServiceDiffs"`
}

func LoadFile(path string) (Config, error) {
	cfg, err := ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func ReadFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func LoadEffective(path string, envPrefix string, overrides map[string]string) (Config, error) {
	var cfg Config
	var err error
	if path == "" {
		cfg = Default()
	} else {
		cfg, err = ReadFile(path)
		if err != nil {
			return Config{}, err
		}
	}
	if envPrefix != "" {
		if err := cfg.ApplyEnv(envPrefix, os.LookupEnv); err != nil {
			return Config{}, err
		}
	}
	if len(overrides) > 0 {
		if err := cfg.ApplyOverrides(overrides); err != nil {
			return Config{}, err
		}
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func Default() Config {
	on := true
	return Config{
		Version:  "v1alpha1",
		Instance: InstanceConfig{Role: "all"},
		Logging:  LoggingConfig{Level: "info", Format: "text"},
		Storage: StorageConfig{
			Driver: "sqlite",
			SQLite: SQLiteConfig{Path: "kubeinsight.db"},
			Maintenance: MaintenanceConfig{
				Enabled:                true,
				IntervalSeconds:        300,
				MinWalBytes:            16 * 1024 * 1024,
				IncrementalVacuumPages: 256,
				JournalSizeLimitBytes:  64 * 1024 * 1024,
				RunOnStart:             false,
				SkipWhenDatabaseBusy:   true,
			},
		},
		Collection: CollectionConfig{
			Enabled:     true,
			Concurrency: 4,
			Resources:   ResourcePolicy{All: true},
		},
		Filters: []FilterConfig{
			{Name: "secret_metadata_only", Enabled: &on, Action: "keep_modified", Resources: []string{"secrets"}, RemovePaths: []string{"/data", "/stringData"}, KeepSecretKeys: true},
			{Name: "managed_fields", Enabled: &on, Action: "keep_modified", RemovePaths: []string{"/metadata/managedFields"}},
			{Name: "lease_skip", Enabled: &on, Action: "discard_resource", Resources: []string{"leases.coordination.k8s.io"}},
		},
		Extractors: []ExtractorConfig{
			{Name: "reference", Enabled: &on},
			{Name: "pod", Enabled: &on, Resources: []string{"pods"}},
			{Name: "node", Enabled: &on, Resources: []string{"nodes"}},
			{Name: "event", Enabled: &on, Resources: []string{"events", "events.events.k8s.io"}},
			{Name: "endpointslice", Enabled: &on, Resources: []string{"endpointslices.discovery.k8s.io"}},
		},
		Plugins: PluginConfig{
			Goja: GojaConfig{Directories: []string{"./plugins"}},
		},
		Server: ServerConfig{
			API:  ListenerConfig{Listen: "127.0.0.1:8080"},
			Web:  ListenerConfig{Listen: "127.0.0.1:8081"},
			Chat: ChatConfig{OpenAIAPIKeyEnv: "OPENAI_API_KEY", Model: "gpt-5.2"},
		},
		MCP: MCPConfig{Listen: "127.0.0.1:8090"},
		Validation: ValidationConfig{
			MaxStoredToRawRatio:          20,
			MaxLatestLookupP95MS:         250,
			MaxHistoricalGetP95MS:        250,
			MaxServiceInvestigationP95MS: 5000,
			MinServiceVersions:           3,
			MinServiceDiffs:              1,
		},
	}
}

func (c *Config) ApplyEnv(prefix string, lookup func(string) (string, bool)) error {
	return applyEnvValue(reflect.ValueOf(c).Elem(), []string{prefix}, lookup)
}

func (c *Config) ApplyOverrides(overrides map[string]string) error {
	for path, value := range overrides {
		if err := setByPath(reflect.ValueOf(c).Elem(), strings.Split(path, "."), value); err != nil {
			return err
		}
	}
	return nil
}

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
	if role == "writer" && (c.Server.API.Enabled || c.Server.Web.Enabled || c.Server.Chat.Enabled || c.MCP.Enabled) {
		return errors.New("api/web/chat/mcp listeners must be disabled when instance.role is writer")
	}
	if c.Collection.Concurrency < 0 {
		return errors.New("collection.concurrency must be non-negative")
	}
	if c.Storage.Maintenance.Enabled {
		if c.Storage.Maintenance.IntervalSeconds <= 0 {
			return errors.New("storage.maintenance.intervalSeconds must be positive when maintenance is enabled")
		}
		if c.Storage.Maintenance.MinWalBytes < 0 {
			return errors.New("storage.maintenance.minWalBytes must be non-negative")
		}
		if c.Storage.Maintenance.IncrementalVacuumPages < 0 {
			return errors.New("storage.maintenance.incrementalVacuumPages must be non-negative")
		}
		if c.Storage.Maintenance.JournalSizeLimitBytes < 0 {
			return errors.New("storage.maintenance.journalSizeLimitBytes must be non-negative")
		}
	}
	if err := validateFilters(c.Filters); err != nil {
		return err
	}
	if err := validateExtractors(c.Extractors); err != nil {
		return err
	}
	if c.Server.Chat.Enabled && c.Server.Chat.OpenAIAPIKeyEnv == "" {
		return errors.New("server.chat.openaiApiKeyEnv is required when chat is enabled")
	}
	return nil
}

func applyEnvValue(v reflect.Value, parts []string, lookup func(string) (string, bool)) error {
	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		field := t.Field(i)
		name := yamlName(field)
		if name == "" {
			continue
		}
		fv := v.Field(i)
		next := append(parts, envNamePart(name))
		if fv.Kind() == reflect.Struct {
			if err := applyEnvValue(fv, next, lookup); err != nil {
				return err
			}
			continue
		}
		if !fv.CanSet() {
			continue
		}
		envName := strings.Join(next, "_")
		value, ok := lookup(envName)
		if !ok {
			continue
		}
		if err := setReflectValue(fv, value); err != nil {
			return fmt.Errorf("%s: %w", envName, err)
		}
	}
	return nil
}

func setByPath(v reflect.Value, path []string, value string) error {
	if len(path) == 0 {
		return errors.New("empty override path")
	}
	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		field := t.Field(i)
		if yamlName(field) != path[0] {
			continue
		}
		fv := v.Field(i)
		if len(path) == 1 {
			return setReflectValue(fv, value)
		}
		if fv.Kind() != reflect.Struct {
			return fmt.Errorf("%s is not a nested config object", path[0])
		}
		return setByPath(fv, path[1:], value)
	}
	return fmt.Errorf("unknown config path %q", strings.Join(path, "."))
}

func setReflectValue(v reflect.Value, value string) error {
	switch v.Kind() {
	case reflect.String:
		v.SetString(value)
	case reflect.Bool:
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return err
		}
		v.SetBool(parsed)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		parsed, err := strconv.ParseInt(value, 10, v.Type().Bits())
		if err != nil {
			return err
		}
		v.SetInt(parsed)
	case reflect.Float64:
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return err
		}
		v.SetFloat(parsed)
	case reflect.Slice:
		if v.Type().Elem().Kind() != reflect.String {
			parsed := reflect.New(v.Type())
			if err := yaml.Unmarshal([]byte(value), parsed.Interface()); err != nil {
				return err
			}
			v.Set(parsed.Elem())
			return nil
		}
		if strings.TrimSpace(value) == "" {
			v.Set(reflect.MakeSlice(v.Type(), 0, 0))
			return nil
		}
		parts := strings.Split(value, ",")
		out := reflect.MakeSlice(v.Type(), 0, len(parts))
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			out = reflect.Append(out, reflect.ValueOf(part))
		}
		v.Set(out)
	default:
		return fmt.Errorf("unsupported config type %s", v.Kind())
	}
	return nil
}

func yamlName(field reflect.StructField) string {
	name := strings.Split(field.Tag.Get("yaml"), ",")[0]
	if name == "" || name == "-" {
		return ""
	}
	return name
}

func envNamePart(name string) string {
	var b strings.Builder
	for i, r := range name {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		b.WriteRune(r)
	}
	return strings.ToUpper(b.String())
}

func validateFilters(filters []FilterConfig) error {
	seen := map[string]bool{}
	for _, filter := range filters {
		if filter.Name == "" {
			return errors.New("filters[].name is required")
		}
		if seen[filter.Name] {
			return fmt.Errorf("duplicate filter %q", filter.Name)
		}
		seen[filter.Name] = true
		switch filter.Action {
		case "", "keep", "keep_modified", "discard_change", "discard_resource":
		default:
			return fmt.Errorf("filter %q has unsupported action %q", filter.Name, filter.Action)
		}
	}
	return nil
}

func validateExtractors(extractors []ExtractorConfig) error {
	seen := map[string]bool{}
	for _, extractor := range extractors {
		if extractor.Name == "" {
			return errors.New("extractors[].name is required")
		}
		if seen[extractor.Name] {
			return fmt.Errorf("duplicate extractor %q", extractor.Name)
		}
		seen[extractor.Name] = true
	}
	return nil
}
