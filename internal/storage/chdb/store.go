package chdb

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

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

var databaseIdentifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func OptionsFromConfig(cfg appconfig.ChDBConfig) Options {
	return Options{Path: cfg.Path, Database: cfg.Database}
}

func validateOptions(opts Options) (Options, error) {
	opts.Path = strings.TrimSpace(opts.Path)
	opts.Database = strings.TrimSpace(opts.Database)
	if opts.Path == "" {
		return opts, fmt.Errorf("chdb path is required")
	}
	if opts.Database == "" {
		return opts, fmt.Errorf("chdb database is required")
	}
	if !databaseIdentifierPattern.MatchString(opts.Database) {
		return opts, fmt.Errorf("invalid chdb database %q", opts.Database)
	}
	return opts, nil
}
