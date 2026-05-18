package chdb

import (
	"errors"

	appconfig "kube-insight/internal/config"
)

var ErrUnavailable = errors.New(
	"chdb storage adapter is not linked in this build; " +
		"build with -tags chdb and chdb-go/libchdb support, or select storage.driver: sqlite/clickhouse",
)

type Options struct {
	Path     string
	Database string
}

func OptionsFromConfig(cfg appconfig.ChDBConfig) Options {
	return Options{Path: cfg.Path, Database: cfg.Database}
}
