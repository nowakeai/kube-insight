package config

import (
	"bytes"
	_ "embed"
	"fmt"

	"gopkg.in/yaml.v3"
)

//go:embed default.yaml
var defaultConfigYAML []byte

func Default() Config {
	cfg, err := parseDefault(defaultConfigYAML)
	if err != nil {
		panic(err)
	}
	return cfg
}

func DefaultYAML() []byte {
	out := make([]byte, len(defaultConfigYAML))
	copy(out, defaultConfigYAML)
	return out
}

func parseDefault(data []byte) (Config, error) {
	cfg, err := decodeConfigStrict(data)
	if err != nil {
		return Config{}, fmt.Errorf("parse embedded default config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("embedded default config is invalid: %w", err)
	}
	return cfg, nil
}

func decodeConfigStrict(data []byte) (Config, error) {
	var cfg Config
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
