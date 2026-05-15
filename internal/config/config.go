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
	Version          string                 `yaml:"version" json:"version"`
	Instance         InstanceConfig         `yaml:"instance" json:"instance"`
	Logging          LoggingConfig          `yaml:"logging" json:"logging"`
	Storage          StorageConfig          `yaml:"storage" json:"storage"`
	Collection       CollectionConfig       `yaml:"collection" json:"collection"`
	ResourceProfiles ResourceProfilesConfig `yaml:"resourceProfiles" json:"resourceProfiles"`
	Processing       ProcessingConfig       `yaml:"processing" json:"processing"`
	Plugins          PluginConfig           `yaml:"plugins" json:"plugins"`
	Server           ServerConfig           `yaml:"server" json:"server"`
	MCP              MCPConfig              `yaml:"mcp" json:"mcp"`
	Validation       ValidationConfig       `yaml:"validation" json:"validation"`
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
	Retention   RetentionConfig   `yaml:"retention" json:"retention"`
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

type RetentionConfig struct {
	Enabled                     bool                             `yaml:"enabled" json:"enabled"`
	MaxAgeSeconds               int                              `yaml:"maxAgeSeconds" json:"maxAgeSeconds"`
	MinVersionsPerObject        int                              `yaml:"minVersionsPerObject" json:"minVersionsPerObject"`
	FilterDecisionMaxAgeSeconds int                              `yaml:"filterDecisionMaxAgeSeconds" json:"filterDecisionMaxAgeSeconds"`
	Policies                    map[string]RetentionPolicyConfig `yaml:"policies" json:"policies"`
}

type RetentionPolicyConfig struct {
	MaxAgeSeconds        int `yaml:"maxAgeSeconds" json:"maxAgeSeconds"`
	MinVersionsPerObject int `yaml:"minVersionsPerObject" json:"minVersionsPerObject"`
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
	Watch       WatchConfig    `yaml:"watch" json:"watch"`
	Resources   ResourcePolicy `yaml:"resources" json:"resources"`
}

type WatchConfig struct {
	DisableHTTP2         bool `yaml:"disableHttp2" json:"disableHttp2"`
	MaxConcurrentStreams int  `yaml:"maxConcurrentStreams" json:"maxConcurrentStreams"`
	MinBackoffMillis     int  `yaml:"minBackoffMillis" json:"minBackoffMillis"`
	MaxBackoffMillis     int  `yaml:"maxBackoffMillis" json:"maxBackoffMillis"`
	StreamStartStaggerMS int  `yaml:"streamStartStaggerMillis" json:"streamStartStaggerMillis"`
}

type ResourcePolicy struct {
	All     bool     `yaml:"all" json:"all"`
	Include []string `yaml:"include" json:"include"`
	Exclude []string `yaml:"exclude" json:"exclude"`
}

type ResourceProfilesConfig struct {
	Defaults        ResourceProfileDefaultsConfig `yaml:"defaults" json:"defaults"`
	ReplaceDefaults bool                          `yaml:"replaceDefaults" json:"replaceDefaults"`
	Rules           []ResourceProfileRuleConfig   `yaml:"rules" json:"rules"`
}

type ResourceProfileDefaultsConfig struct {
	Enabled            *bool  `yaml:"enabled" json:"enabled,omitempty"`
	RetentionPolicy    string `yaml:"retentionPolicy" json:"retentionPolicy"`
	FilterChain        string `yaml:"filterChain" json:"filterChain"`
	ExtractorSet       string `yaml:"extractorSet" json:"extractorSet"`
	CompactionStrategy string `yaml:"compactionStrategy" json:"compactionStrategy"`
	Priority           string `yaml:"priority" json:"priority"`
	MaxEventBuffer     int    `yaml:"maxEventBuffer" json:"maxEventBuffer"`
}

type ResourceProfileRuleConfig struct {
	Name               string   `yaml:"name" json:"name"`
	Enabled            *bool    `yaml:"enabled" json:"enabled,omitempty"`
	Groups             []string `yaml:"groups" json:"groups"`
	Resources          []string `yaml:"resources" json:"resources"`
	Kinds              []string `yaml:"kinds" json:"kinds"`
	RetentionPolicy    string   `yaml:"retentionPolicy" json:"retentionPolicy"`
	FilterChain        string   `yaml:"filterChain" json:"filterChain"`
	ExtractorSet       string   `yaml:"extractorSet" json:"extractorSet"`
	CompactionStrategy string   `yaml:"compactionStrategy" json:"compactionStrategy"`
	Priority           string   `yaml:"priority" json:"priority"`
	MaxEventBuffer     int      `yaml:"maxEventBuffer" json:"maxEventBuffer"`
}

type ProcessingConfig struct {
	FilterChains  map[string][]string                  `yaml:"filterChains" json:"filterChains"`
	Filters       map[string]ProcessingFilterConfig    `yaml:"filters" json:"filters"`
	ExtractorSets map[string][]string                  `yaml:"extractorSets" json:"extractorSets"`
	Extractors    map[string]ProcessingExtractorConfig `yaml:"extractors" json:"extractors"`
}

type ProcessingFilterConfig struct {
	Type           string              `yaml:"type" json:"type"`
	Enabled        *bool               `yaml:"enabled" json:"enabled,omitempty"`
	Action         string              `yaml:"action" json:"action"`
	RemovePaths    []string            `yaml:"removePaths" json:"removePaths"`
	KeepSecretKeys bool                `yaml:"keepSecretKeys" json:"keepSecretKeys"`
	Guard          ResourceGuardConfig `yaml:"guard" json:"guard"`
	Script         string              `yaml:"script" json:"script,omitempty"`
}

type ProcessingExtractorConfig struct {
	Type    string              `yaml:"type" json:"type"`
	Enabled *bool               `yaml:"enabled" json:"enabled,omitempty"`
	Guard   ResourceGuardConfig `yaml:"guard" json:"guard"`
	Script  string              `yaml:"script" json:"script,omitempty"`
}

type ResourceGuardConfig struct {
	Resources  []string `yaml:"resources" json:"resources"`
	Kinds      []string `yaml:"kinds" json:"kinds"`
	Namespaces []string `yaml:"namespaces" json:"namespaces"`
	Names      []string `yaml:"names" json:"names"`
}

type PluginConfig struct {
	Goja GojaConfig `yaml:"goja" json:"goja"`
}

type GojaConfig struct {
	Enabled     bool     `yaml:"enabled" json:"enabled"`
	Directories []string `yaml:"directories" json:"directories"`
}

type ServerConfig struct {
	API     ListenerConfig `yaml:"api" json:"api"`
	Metrics ListenerConfig `yaml:"metrics" json:"metrics"`
	Web     ListenerConfig `yaml:"web" json:"web"`
	Chat    ChatConfig     `yaml:"chat" json:"chat"`
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
	return Read(data)
}

func Read(data []byte) (Config, error) {
	merged, err := overlayDefaultYAML(data)
	if err != nil {
		return Config{}, err
	}
	cfg, err := decodeConfigStrict(merged)
	if err != nil {
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
	case reflect.Map:
		parsed := reflect.New(v.Type())
		if err := yaml.Unmarshal([]byte(value), parsed.Interface()); err != nil {
			return err
		}
		v.Set(parsed.Elem())
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
