package cli

import (
	appconfig "kube-insight/internal/config"
	"kube-insight/internal/storage/sqlite"
)

func sqliteOptionsFromConfig(cfg appconfig.Config) sqlite.Options {
	return sqlite.Options{ProfileRules: cfg.ProfileRules()}
}
