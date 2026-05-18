package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	appconfig "kube-insight/internal/config"
	"kube-insight/internal/storage"
	"kube-insight/internal/storage/chdb"
	"kube-insight/internal/storage/clickhouse"
)

func storageDriver(cfg appconfig.Config) string {
	driver := strings.ToLower(strings.TrimSpace(cfg.Storage.Driver))
	if driver == "" {
		return "sqlite"
	}
	return driver
}

func newClickHouseStore(ctx context.Context, cfg appconfig.Config) (storage.Store, error) {
	store, err := newClickHouseStoreFromConfig(cfg)
	if err != nil {
		return nil, err
	}
	ch := cfg.Storage.ClickHouse
	if ch.InitOnStart {
		options := clickhouse.OptionsFromConfig(ch)
		statements, err := clickhouse.CreateTableStatements(options.SchemaOptions())
		if err != nil {
			return nil, err
		}
		if _, err := store.ApplySchema(ctx, statements); err != nil {
			return nil, err
		}
	}
	return store, nil
}

func newClickHouseStoreFromConfig(cfg appconfig.Config) (*clickhouse.Store, error) {
	ch := cfg.Storage.ClickHouse
	endpoint := os.Getenv(ch.DSNEnv)
	if endpoint == "" {
		return nil, fmt.Errorf("clickhouse storage requires env %s", ch.DSNEnv)
	}
	return clickhouse.NewHTTPStore(endpoint, clickhouse.OptionsFromConfig(ch))
}

func newChDBStore(ctx context.Context, cfg appconfig.Config) (storage.Store, error) {
	store, err := newChDBStoreFromConfig(cfg)
	if err != nil {
		return nil, err
	}
	options := clickhouse.SchemaOptions{Database: cfg.Storage.ChDB.Database}
	statements, err := clickhouse.CreateTableStatements(options)
	if err != nil {
		return nil, err
	}
	applier, ok := store.(interface {
		ApplySchema(context.Context, []string) (clickhouse.ApplyResult, error)
	})
	if !ok {
		return nil, fmt.Errorf("chdb store does not support schema initialization")
	}
	if _, err := applier.ApplySchema(ctx, statements); err != nil {
		return nil, err
	}
	return store, nil
}

func newChDBStoreFromConfig(cfg appconfig.Config) (storage.Store, error) {
	return chdb.NewStore(chdb.OptionsFromConfig(cfg.Storage.ChDB))
}
