package clickhouse

import appconfig "kube-insight/internal/config"

type Options struct {
	InitOnStart      bool
	DSNEnv           string
	Database         string
	Cluster          string
	HotVolume        string
	ColdVolume       string
	ColdAfterSeconds int
	UseJSONType      bool
	AsyncInsert      bool
	BatchSize        int
	FlushIntervalMS  int
}

func OptionsFromConfig(cfg appconfig.ClickHouseConfig) Options {
	return Options{
		InitOnStart:      cfg.InitOnStart,
		DSNEnv:           cfg.DSNEnv,
		Database:         cfg.Database,
		Cluster:          cfg.Cluster,
		HotVolume:        cfg.HotVolume,
		ColdVolume:       cfg.ColdVolume,
		ColdAfterSeconds: cfg.ColdAfterSeconds,
		UseJSONType:      cfg.UseJSONType,
		AsyncInsert:      cfg.AsyncInsert,
		BatchSize:        cfg.BatchSize,
		FlushIntervalMS:  cfg.FlushIntervalMS,
	}
}

func (o Options) SchemaOptions() SchemaOptions {
	return SchemaOptions{
		Database:         o.Database,
		Cluster:          o.Cluster,
		UseJSONType:      o.UseJSONType,
		HotVolume:        o.HotVolume,
		ColdVolume:       o.ColdVolume,
		ColdAfterSeconds: o.ColdAfterSeconds,
	}
}
