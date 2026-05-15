package config

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadExampleConfig(t *testing.T) {
	cfg, err := LoadFile(filepath.Join("..", "..", "config", "kube-insight.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Storage.Driver != "sqlite" || cfg.Storage.SQLite.Path == "" {
		t.Fatalf("storage = %#v", cfg.Storage)
	}
	if !cfg.Storage.Maintenance.Enabled || cfg.Storage.Maintenance.IntervalSeconds <= 0 {
		t.Fatalf("maintenance = %#v", cfg.Storage.Maintenance)
	}
	if cfg.Storage.Retention.Enabled || cfg.Storage.Retention.MinVersionsPerObject <= 0 {
		t.Fatalf("retention = %#v", cfg.Storage.Retention)
	}
	if cfg.Instance.Role != "all" || !cfg.Collection.Enabled {
		t.Fatalf("instance/collection = %#v %#v", cfg.Instance, cfg.Collection)
	}
	if !cfg.Collection.UseClientGo {
		t.Fatalf("useClientGo = false")
	}
	if !cfg.Collection.Resources.All {
		t.Fatalf("resources = %#v", cfg.Collection.Resources)
	}
	if cfg.Collection.Watch.DisableHTTP2 || cfg.Collection.Watch.MaxConcurrentStreams <= 0 || cfg.Collection.Watch.MinBackoffMillis <= 0 || cfg.Collection.Watch.StreamStartStaggerMS <= 0 {
		t.Fatalf("watch tuning = %#v", cfg.Collection.Watch)
	}
	for _, want := range []string{"events", "leases.coordination.k8s.io", "*policyreports.wgpolicyk8s.io", "*ephemeralreports.reports.kyverno.io"} {
		if !hasString(cfg.Collection.Resources.Exclude, want) {
			t.Fatalf("resource exclude %q missing: %#v", want, cfg.Collection.Resources.Exclude)
		}
	}
	if len(cfg.Processing.Filters) == 0 || len(cfg.Processing.Extractors) == 0 {
		t.Fatalf("filters/extractors missing: %#v %#v", cfg.Processing.Filters, cfg.Processing.Extractors)
	}
	if len(cfg.ProfileRules()) == 0 {
		t.Fatalf("profile rules missing")
	}
	for _, want := range []string{"resource_version", "status_condition_timestamps", "leader_election_configmap", "report_skip"} {
		if !hasProcessingFilter(cfg.Processing.Filters, want) {
			t.Fatalf("filter %q missing: %#v", want, cfg.Processing.Filters)
		}
	}
}

func TestDefaultMatchesExampleConfig(t *testing.T) {
	example, err := LoadFile(filepath.Join("..", "..", "config", "kube-insight.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	defaultCfg := Default()
	normalizeEmptySlices(reflect.ValueOf(&example).Elem())
	normalizeEmptySlices(reflect.ValueOf(&defaultCfg).Elem())
	if !reflect.DeepEqual(defaultCfg, example) {
		t.Fatalf("Default() drifted from config/kube-insight.example.yaml\nDefault: %#v\nExample: %#v", defaultCfg, example)
	}
}

func TestExampleConfigMatchesEmbeddedDefaultYAML(t *testing.T) {
	example, err := os.ReadFile(filepath.Join("..", "..", "config", "kube-insight.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.TrimSpace(DefaultYAML()), bytes.TrimSpace(example)) {
		t.Fatal("config/kube-insight.example.yaml drifted from internal/config/default.yaml")
	}
}

func hasString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func normalizeEmptySlices(v reflect.Value) {
	if !v.IsValid() {
		return
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			normalizeEmptySlices(v.Elem())
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			normalizeEmptySlices(v.Field(i))
		}
	case reflect.Map:
		for _, key := range v.MapKeys() {
			value := v.MapIndex(key)
			if !value.CanSet() {
				copyValue := reflect.New(value.Type()).Elem()
				copyValue.Set(value)
				normalizeEmptySlices(copyValue)
				v.SetMapIndex(key, copyValue)
				continue
			}
			normalizeEmptySlices(value)
		}
	case reflect.Slice:
		if v.Len() == 0 && !v.IsNil() && v.CanSet() {
			v.Set(reflect.Zero(v.Type()))
			return
		}
		for i := 0; i < v.Len(); i++ {
			normalizeEmptySlices(v.Index(i))
		}
	}
}

func hasProcessingFilter(filters map[string]ProcessingFilterConfig, name string) bool {
	_, ok := filters[name]
	return ok
}

func TestValidateRejectsAPIInstanceWithCollectionEnabled(t *testing.T) {
	cfg := Config{
		Version:  "v1alpha1",
		Instance: InstanceConfig{Role: "api"},
		Storage: StorageConfig{
			Driver: "sqlite",
			SQLite: SQLiteConfig{Path: "kube-insight.db"},
		},
		Collection: CollectionConfig{Enabled: true},
	}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "collection.enabled") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadEffectiveAppliesEnvAndOverrides(t *testing.T) {
	t.Setenv("KUBE_INSIGHT_COLLECTION_CONTEXTS", "staging,prod")
	t.Setenv("KUBE_INSIGHT_COLLECTION_CONCURRENCY", "9")
	t.Setenv("KUBE_INSIGHT_COLLECTION_WATCH_DISABLE_HTTP2", "false")
	t.Setenv("KUBE_INSIGHT_COLLECTION_WATCH_MAX_BACKOFF_MILLIS", "12000")
	t.Setenv("KUBE_INSIGHT_LOGGING_LEVEL", "debug")
	t.Setenv("KUBE_INSIGHT_LOGGING_FORMAT", "json")
	t.Setenv("KUBE_INSIGHT_STORAGE_MAINTENANCE_MIN_WAL_BYTES", "1048576")
	t.Setenv("KUBE_INSIGHT_PROCESSING_FILTERS", `{managed_fields: {type: builtin, action: keep_modified, removePaths: [/metadata/managedFields]}}`)
	t.Setenv("KUBE_INSIGHT_PROCESSING_FILTER_CHAINS", `{default: [managed_fields]}`)
	cfg, err := LoadEffective(filepath.Join("..", "..", "config", "kube-insight.example.yaml"), "KUBE_INSIGHT", map[string]string{
		"collection.namespace":                "payments",
		"storage.sqlite.path":                 "override.db",
		"storage.maintenance.intervalSeconds": "60",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cfg.Collection.Contexts, ","); got != "staging,prod" {
		t.Fatalf("contexts = %q", got)
	}
	if cfg.Collection.Concurrency != 9 || cfg.Collection.Namespace != "payments" {
		t.Fatalf("collection = %#v", cfg.Collection)
	}
	if cfg.Collection.Watch.DisableHTTP2 || cfg.Collection.Watch.MaxBackoffMillis != 12000 {
		t.Fatalf("watch tuning = %#v", cfg.Collection.Watch)
	}
	if cfg.Storage.SQLite.Path != "override.db" {
		t.Fatalf("sqlite path = %q", cfg.Storage.SQLite.Path)
	}
	if cfg.Logging.Level != "debug" || cfg.Logging.Format != "json" {
		t.Fatalf("logging = %#v", cfg.Logging)
	}
	if cfg.Storage.Maintenance.IntervalSeconds != 60 || cfg.Storage.Maintenance.MinWalBytes != 1048576 {
		t.Fatalf("maintenance = %#v", cfg.Storage.Maintenance)
	}
	if len(cfg.Processing.Filters) != 1 {
		t.Fatalf("filters = %#v", cfg.Processing.Filters)
	}
	if _, ok := cfg.Processing.Filters["managed_fields"]; !ok {
		t.Fatalf("filters = %#v", cfg.Processing.Filters)
	}
}

func TestReadOverlaysDefaultConfig(t *testing.T) {
	cfg, err := Read([]byte(`
logging:
  level: debug
collection:
  resources:
    exclude: [pods]
processing:
  filters:
    managed_fields:
      enabled: false
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Logging.Level != "debug" {
		t.Fatalf("logging = %#v", cfg.Logging)
	}
	if cfg.Storage.SQLite.Path != "kubeinsight.db" || !cfg.Collection.UseClientGo {
		t.Fatalf("defaults not retained: storage=%#v collection=%#v", cfg.Storage, cfg.Collection)
	}
	if got := strings.Join(cfg.Collection.Resources.Exclude, ","); got != "pods" {
		t.Fatalf("exclude list = %q, want replacement", got)
	}
	managedFields := cfg.Processing.Filters["managed_fields"]
	if managedFields.Enabled == nil || *managedFields.Enabled {
		t.Fatalf("managed_fields enabled = %#v", managedFields.Enabled)
	}
	if managedFields.Action != "keep_modified" || len(managedFields.RemovePaths) == 0 {
		t.Fatalf("managed_fields default fields not retained: %#v", managedFields)
	}
	if _, ok := cfg.Processing.Filters["resource_version"]; !ok {
		t.Fatalf("processing filters were not map-merged: %#v", cfg.Processing.Filters)
	}
}

func TestReadEmptyConfigUsesDefaults(t *testing.T) {
	cfg, err := Read(nil)
	if err != nil {
		t.Fatal(err)
	}
	defaultCfg := Default()
	normalizeEmptySlices(reflect.ValueOf(&cfg).Elem())
	normalizeEmptySlices(reflect.ValueOf(&defaultCfg).Elem())
	if !reflect.DeepEqual(defaultCfg, cfg) {
		t.Fatalf("empty config should load defaults\nDefault: %#v\nRead: %#v", defaultCfg, cfg)
	}
}

func TestReadRejectsUnknownTopLevelFields(t *testing.T) {
	_, err := Read([]byte(`
loging:
  level: debug
`))
	if err == nil || !strings.Contains(err.Error(), "field loging not found") {
		t.Fatalf("err = %v", err)
	}
}

func TestReadRejectsUnknownNestedFields(t *testing.T) {
	_, err := Read([]byte(`
logging:
  levle: debug
`))
	if err == nil || !strings.Contains(err.Error(), "field levle not found") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateAcceptsPostgresSharedDSN(t *testing.T) {
	cfg := Config{
		Version:  "v1alpha1",
		Instance: InstanceConfig{Role: "api"},
		Storage: StorageConfig{
			Driver:   "postgres",
			Postgres: SQLDatabaseConfig{DSNEnv: "KUBE_INSIGHT_POSTGRES_DSN"},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsWriterInstanceWithListeners(t *testing.T) {
	cfg := Config{
		Version:  "v1alpha1",
		Instance: InstanceConfig{Role: "writer"},
		Storage: StorageConfig{
			Driver: "sqlite",
			SQLite: SQLiteConfig{Path: "kube-insight.db"},
		},
		Server: ServerConfig{
			Chat: ChatConfig{Enabled: true, OpenAIAPIKeyEnv: "OPENAI_API_KEY"},
		},
	}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "api/metrics/web/chat/mcp") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateRejectsBadFilterAction(t *testing.T) {
	cfg := Config{
		Version: "v1alpha1",
		Storage: StorageConfig{
			Driver: "sqlite",
			SQLite: SQLiteConfig{Path: "kube-insight.db"},
		},
		Processing: ProcessingConfig{
			Filters: map[string]ProcessingFilterConfig{
				"managed_fields": {Type: "builtin", Action: "drop_everything"},
			},
		},
	}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "unsupported action") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateRejectsUnknownBuiltInFilter(t *testing.T) {
	cfg := Default()
	cfg.Processing.Filters["custom"] = ProcessingFilterConfig{Type: "builtin", Action: "keep"}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "unsupported built-in filter") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateRejectsBuiltInFilterActionMismatch(t *testing.T) {
	cfg := Default()
	cfg.Processing.Filters["lease_skip"] = ProcessingFilterConfig{Type: "builtin", Action: "keep"}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "does not match built-in action") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateRejectsUnknownBuiltInExtractor(t *testing.T) {
	cfg := Default()
	cfg.Processing.Extractors["custom"] = ProcessingExtractorConfig{Type: "builtin"}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "unsupported built-in extractor") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateRejectsNonSnakeConfigNames(t *testing.T) {
	cfg := Default()
	cfg.ResourceProfiles.Rules = []ResourceProfileRuleConfig{
		{Name: "camelCase", Resources: []string{"pods"}},
	}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "lower_snake_case") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateAcceptsLogfmtLogging(t *testing.T) {
	cfg := Default()
	cfg.Logging.Format = "logfmt"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestProfileRulesPreferConfigRules(t *testing.T) {
	cfg := Default()
	cfg.ResourceProfiles.Rules = []ResourceProfileRuleConfig{
		{
			Name:            "custom_events",
			Resources:       []string{"events.events.k8s.io"},
			RetentionPolicy: "short",
			ExtractorSet:    "event",
			Priority:        "low",
		},
	}
	rules := cfg.ProfileRules()
	if len(rules) == 0 || rules[0].Name != "custom_events" {
		t.Fatalf("rules = %#v", rules)
	}
}

func TestWithEffectiveProfileRulesMaterializesBuiltins(t *testing.T) {
	cfg := Default().WithEffectiveProfileRules()
	if !cfg.ResourceProfiles.ReplaceDefaults {
		t.Fatalf("replaceDefaults = false")
	}
	if len(cfg.ResourceProfiles.Rules) == 0 {
		t.Fatal("rules empty")
	}
	var foundPod bool
	var foundLease bool
	for _, rule := range cfg.ResourceProfiles.Rules {
		switch rule.Name {
		case "pod_fast_path":
			foundPod = rule.Enabled != nil && *rule.Enabled &&
				rule.FilterChain == "default" &&
				rule.ExtractorSet == "pod" &&
				rule.RetentionPolicy == "hot"
		case "lease_skip_or_downsample":
			foundLease = rule.Enabled != nil && !*rule.Enabled &&
				rule.FilterChain == "lease_skip" &&
				rule.ExtractorSet == "none"
		}
	}
	if !foundPod || !foundLease {
		t.Fatalf("materialized rules missing pod=%v lease=%v: %#v", foundPod, foundLease, cfg.ResourceProfiles.Rules)
	}
}

func TestValidateRejectsEmptyProfileReplacement(t *testing.T) {
	cfg := Default()
	cfg.ResourceProfiles.ReplaceDefaults = true
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "resourceProfiles.rules") {
		t.Fatalf("err = %v", err)
	}
}
