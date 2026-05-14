package logging

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	charmlog "charm.land/log/v2"
)

type Config struct {
	Level  string `yaml:"level" json:"level"`
	Format string `yaml:"format" json:"format"`
}

func Default() Config {
	return Config{
		Level:  "info",
		Format: "text",
	}
}

func New(w io.Writer, cfg Config) (*slog.Logger, error) {
	level, err := parseLevel(cfg.Level)
	if err != nil {
		return nil, err
	}
	formatter, err := parseFormatter(cfg.Format)
	if err != nil {
		return nil, err
	}
	handler := charmlog.NewWithOptions(w, charmlog.Options{
		Level:           level,
		Formatter:       formatter,
		ReportTimestamp: true,
		TimeFormat:      time.RFC3339,
		TimeFunction:    charmlog.NowUTC,
	})
	return slog.New(handler), nil
}

func parseFormatter(value string) (charmlog.Formatter, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "text":
		return charmlog.TextFormatter, nil
	case "json":
		return charmlog.JSONFormatter, nil
	case "logfmt":
		return charmlog.LogfmtFormatter, nil
	default:
		return charmlog.TextFormatter, fmt.Errorf("unsupported log format %q", value)
	}
}

func parseLevel(value string) (charmlog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "info":
		return charmlog.InfoLevel, nil
	case "debug":
		return charmlog.DebugLevel, nil
	case "warn", "warning":
		return charmlog.WarnLevel, nil
	case "error":
		return charmlog.ErrorLevel, nil
	default:
		return charmlog.InfoLevel, fmt.Errorf("unsupported log level %q", value)
	}
}
