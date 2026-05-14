package config

import (
	"path/filepath"
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
	if cfg.Instance.Role != "all" || !cfg.Collection.Enabled {
		t.Fatalf("instance/collection = %#v %#v", cfg.Instance, cfg.Collection)
	}
	if !cfg.Collection.UseClientGo {
		t.Fatalf("useClientGo = false")
	}
	if !cfg.Collection.Resources.All {
		t.Fatalf("resources = %#v", cfg.Collection.Resources)
	}
	if len(cfg.Filters) == 0 || len(cfg.Extractors) == 0 {
		t.Fatalf("filters/extractors missing: %#v %#v", cfg.Filters, cfg.Extractors)
	}
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
	t.Setenv("KUBE_INSIGHT_LOGGING_LEVEL", "debug")
	t.Setenv("KUBE_INSIGHT_LOGGING_FORMAT", "json")
	t.Setenv("KUBE_INSIGHT_STORAGE_MAINTENANCE_MIN_WAL_BYTES", "1048576")
	t.Setenv("KUBE_INSIGHT_FILTERS", `[{name: managed_fields, action: keep_modified, removePaths: [/metadata/managedFields]}]`)
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
	if cfg.Storage.SQLite.Path != "override.db" {
		t.Fatalf("sqlite path = %q", cfg.Storage.SQLite.Path)
	}
	if cfg.Logging.Level != "debug" || cfg.Logging.Format != "json" {
		t.Fatalf("logging = %#v", cfg.Logging)
	}
	if cfg.Storage.Maintenance.IntervalSeconds != 60 || cfg.Storage.Maintenance.MinWalBytes != 1048576 {
		t.Fatalf("maintenance = %#v", cfg.Storage.Maintenance)
	}
	if len(cfg.Filters) != 1 || cfg.Filters[0].Name != "managed_fields" {
		t.Fatalf("filters = %#v", cfg.Filters)
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
	if err == nil || !strings.Contains(err.Error(), "api/web/chat/mcp") {
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
		Filters: []FilterConfig{{Name: "bad", Action: "drop_everything"}},
	}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "unsupported action") {
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
