package config

import (
	"errors"
	"fmt"

	"kube-insight/internal/resourceprofile"
)

func (c Config) ProfileRules() []resourceprofile.Rule {
	base := profileDefaultsFromConfig(c.ResourceProfiles.Defaults)
	custom := profileRulesFromConfig(c.ResourceProfiles.Rules)
	if c.ResourceProfiles.ReplaceDefaults {
		return withBaseProfile(custom, base)
	}
	return append(withBaseProfile(custom, base), resourceprofile.DefaultRulesWithBase(base)...)
}

func (c Config) WithEffectiveProfileRules() Config {
	out := c
	base := profileDefaultsFromConfig(c.ResourceProfiles.Defaults)
	rules := c.ProfileRules()
	out.ResourceProfiles.ReplaceDefaults = true
	out.ResourceProfiles.Rules = make([]ResourceProfileRuleConfig, 0, len(rules))
	for _, rule := range rules {
		profile := rule.EffectiveProfile(base)
		enabled := profile.Enabled
		out.ResourceProfiles.Rules = append(out.ResourceProfiles.Rules, ResourceProfileRuleConfig{
			Name:               profile.Name,
			Enabled:            &enabled,
			Groups:             cloneStringSlice(rule.Groups),
			Resources:          cloneStringSlice(rule.Resources),
			Kinds:              cloneStringSlice(rule.Kinds),
			RetentionPolicy:    profile.RetentionPolicy,
			FilterChain:        profile.FilterChain,
			ExtractorSet:       profile.ExtractorSet,
			CompactionStrategy: profile.CompactionStrategy,
			Priority:           profile.Priority,
			MaxEventBuffer:     profile.MaxEventBuffer,
		})
	}
	return out
}

func profileRulesFromConfig(configs []ResourceProfileRuleConfig) []resourceprofile.Rule {
	rules := make([]resourceprofile.Rule, 0, len(configs))
	for _, cfg := range configs {
		enabled := true
		if cfg.Enabled != nil {
			enabled = *cfg.Enabled
		}
		rules = append(rules, resourceprofile.Rule{
			Name:      cfg.Name,
			Groups:    cfg.Groups,
			Resources: cfg.Resources,
			Kinds:     cfg.Kinds,
			Disabled:  !enabled,
			Profile: resourceprofile.Profile{
				RetentionPolicy:    cfg.RetentionPolicy,
				FilterChain:        cfg.FilterChain,
				ExtractorSet:       cfg.ExtractorSet,
				CompactionStrategy: cfg.CompactionStrategy,
				Priority:           cfg.Priority,
				MaxEventBuffer:     cfg.MaxEventBuffer,
			},
		})
	}
	return resourceprofile.CloneRules(rules)
}

func profileDefaultsFromConfig(cfg ResourceProfileDefaultsConfig) resourceprofile.Profile {
	profile := resourceprofile.DefaultProfile()
	if cfg.Enabled != nil {
		profile.Enabled = *cfg.Enabled
	}
	if cfg.RetentionPolicy != "" {
		profile.RetentionPolicy = cfg.RetentionPolicy
	}
	if cfg.FilterChain != "" {
		profile.FilterChain = cfg.FilterChain
	}
	if cfg.ExtractorSet != "" {
		profile.ExtractorSet = cfg.ExtractorSet
	}
	if cfg.CompactionStrategy != "" {
		profile.CompactionStrategy = cfg.CompactionStrategy
	}
	if cfg.Priority != "" {
		profile.Priority = cfg.Priority
	}
	if cfg.MaxEventBuffer != 0 {
		profile.MaxEventBuffer = cfg.MaxEventBuffer
	}
	return profile
}

func withBaseProfile(rules []resourceprofile.Rule, base resourceprofile.Profile) []resourceprofile.Rule {
	out := resourceprofile.CloneRules(rules)
	for i := range out {
		out[i].Base = base
	}
	return out
}

func validateResourceProfiles(profiles ResourceProfilesConfig) error {
	if err := validateProfileDefaults(profiles.Defaults); err != nil {
		return err
	}
	for i, rule := range profiles.Rules {
		if err := validateConfigName(fmt.Sprintf("resourceProfiles.rules[%d].name", i), rule.Name); err != nil {
			return err
		}
		if len(rule.Groups) == 0 && len(rule.Resources) == 0 && len(rule.Kinds) == 0 {
			return fmt.Errorf("resourceProfiles.rules[%d] must set at least one of groups, resources, or kinds", i)
		}
		switch rule.Priority {
		case "", "high", "normal", "low":
		default:
			return fmt.Errorf("resourceProfiles.rules[%d].priority %q is unsupported", i, rule.Priority)
		}
		if rule.MaxEventBuffer < 0 {
			return fmt.Errorf("resourceProfiles.rules[%d].maxEventBuffer must be non-negative", i)
		}
	}
	if profiles.ReplaceDefaults && len(profiles.Rules) == 0 {
		return errors.New("resourceProfiles.rules must not be empty when resourceProfiles.replaceDefaults is true")
	}
	return nil
}

func cloneStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}

func validateProfileDefaults(defaults ResourceProfileDefaultsConfig) error {
	switch defaults.Priority {
	case "", "high", "normal", "low":
	default:
		return fmt.Errorf("resourceProfiles.defaults.priority %q is unsupported", defaults.Priority)
	}
	if defaults.MaxEventBuffer < 0 {
		return errors.New("resourceProfiles.defaults.maxEventBuffer must be non-negative")
	}
	return nil
}
