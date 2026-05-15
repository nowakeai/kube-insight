package resourceprofile

import (
	_ "embed"
	"fmt"

	"gopkg.in/yaml.v3"
)

//go:embed default_rules.yaml
var defaultRulesYAML []byte

var defaultRules = mustParseDefaultRules(defaultRulesYAML)

type defaultRulesConfig struct {
	Rules []defaultRuleConfig `yaml:"rules"`
}

type defaultRuleConfig struct {
	Name               string   `yaml:"name"`
	Enabled            *bool    `yaml:"enabled"`
	Groups             []string `yaml:"groups"`
	Resources          []string `yaml:"resources"`
	Kinds              []string `yaml:"kinds"`
	RetentionPolicy    string   `yaml:"retentionPolicy"`
	FilterChain        string   `yaml:"filterChain"`
	ExtractorSet       string   `yaml:"extractorSet"`
	CompactionStrategy string   `yaml:"compactionStrategy"`
	Priority           string   `yaml:"priority"`
	MaxEventBuffer     int      `yaml:"maxEventBuffer"`
}

func mustParseDefaultRules(data []byte) []Rule {
	rules, err := parseDefaultRules(data)
	if err != nil {
		panic(err)
	}
	return rules
}

func parseDefaultRules(data []byte) ([]Rule, error) {
	var cfg defaultRulesConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse embedded default profile rules: %w", err)
	}
	if len(cfg.Rules) == 0 {
		return nil, fmt.Errorf("embedded default profile rules are empty")
	}
	rules := make([]Rule, 0, len(cfg.Rules))
	for i, rule := range cfg.Rules {
		if rule.Name == "" {
			return nil, fmt.Errorf("embedded default profile rules[%d].name is required", i)
		}
		enabled := true
		if rule.Enabled != nil {
			enabled = *rule.Enabled
		}
		rules = append(rules, Rule{
			Name:      rule.Name,
			Groups:    rule.Groups,
			Resources: rule.Resources,
			Kinds:     rule.Kinds,
			Disabled:  !enabled,
			Profile: Profile{
				RetentionPolicy:    rule.RetentionPolicy,
				FilterChain:        rule.FilterChain,
				ExtractorSet:       rule.ExtractorSet,
				CompactionStrategy: rule.CompactionStrategy,
				Priority:           rule.Priority,
				MaxEventBuffer:     rule.MaxEventBuffer,
			},
		})
	}
	return rules, nil
}
