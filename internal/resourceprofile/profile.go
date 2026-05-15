package resourceprofile

import (
	"strings"

	"kube-insight/internal/kubeapi"
	"kube-insight/internal/resourcematch"
)

type Profile struct {
	Name               string
	RetentionPolicy    string
	FilterChain        string
	ExtractorSet       string
	CompactionStrategy string
	Priority           string
	MaxEventBuffer     int
	Enabled            bool
}

type Resource struct {
	Group    string
	Version  string
	Resource string
	Kind     string
}

type Rule struct {
	Name      string
	Groups    []string
	Resources []string
	Kinds     []string
	Disabled  bool
	Base      Profile
	Profile   Profile
}

func ForResource(info kubeapi.ResourceInfo) Profile {
	return Select(info, DefaultRules())
}

func Select(info kubeapi.ResourceInfo, rules []Rule) Profile {
	resource := normalize(info)
	base := DefaultProfile()
	for _, rule := range rules {
		if rule.matches(resource) {
			return rule.apply(base)
		}
	}
	return base
}

func DefaultRules() []Rule {
	return CloneRules(defaultRules)
}

func DefaultRulesWithBase(base Profile) []Rule {
	rules := CloneRules(defaultRules)
	for i := range rules {
		rules[i].Base = base
	}
	return rules
}

func CloneRules(rules []Rule) []Rule {
	out := make([]Rule, len(rules))
	for i, rule := range rules {
		out[i] = rule
		out[i].Groups = cloneStrings(rule.Groups)
		out[i].Resources = cloneStrings(rule.Resources)
		out[i].Kinds = cloneStrings(rule.Kinds)
	}
	return out
}

func DefaultProfile() Profile {
	return Profile{
		Name:               "generic",
		RetentionPolicy:    "standard",
		FilterChain:        "default",
		ExtractorSet:       "generic",
		CompactionStrategy: "full_json",
		Priority:           "normal",
		MaxEventBuffer:     256,
		Enabled:            true,
	}
}

func normalize(info kubeapi.ResourceInfo) Resource {
	return Resource{
		Group:    strings.ToLower(strings.TrimSpace(info.Group)),
		Version:  strings.ToLower(strings.TrimSpace(info.Version)),
		Resource: strings.ToLower(strings.TrimSpace(info.Resource)),
		Kind:     strings.ToLower(strings.TrimSpace(info.Kind)),
	}
}

func (rule Rule) matches(resource Resource) bool {
	matchResource := resource.matchResource()
	if len(rule.Groups) > 0 && !resourcematch.MatchAnyString(rule.Groups, resource.Group) {
		return false
	}
	if len(rule.Resources) > 0 && !resourcematch.MatchAnyResource(rule.Resources, matchResource) {
		return false
	}
	if len(rule.Kinds) > 0 && !resourcematch.MatchAnyString(rule.Kinds, resource.Kind) {
		return false
	}
	return true
}

func (resource Resource) matchResource() resourcematch.Resource {
	return resourcematch.Resource{
		Group:    resource.Group,
		Version:  resource.Version,
		Resource: resource.Resource,
		Kind:     resource.Kind,
	}
}

func (rule Rule) apply(base Profile) Profile {
	if rule.Base.Name != "" {
		base = rule.Base
	}
	out := base
	if rule.Name != "" {
		out.Name = rule.Name
	}
	out = mergeProfile(out, rule.Profile)
	if rule.Disabled {
		out.Enabled = false
	}
	return out
}

// EffectiveProfile returns the profile selected by this rule after applying
// defaults, rule overrides, and disabled state.
func (rule Rule) EffectiveProfile(base Profile) Profile {
	return rule.apply(base)
}

func mergeProfile(base, override Profile) Profile {
	out := base
	if override.Name != "" {
		out.Name = override.Name
	}
	if override.RetentionPolicy != "" {
		out.RetentionPolicy = override.RetentionPolicy
	}
	if override.FilterChain != "" {
		out.FilterChain = override.FilterChain
	}
	if override.ExtractorSet != "" {
		out.ExtractorSet = override.ExtractorSet
	}
	if override.CompactionStrategy != "" {
		out.CompactionStrategy = override.CompactionStrategy
	}
	if override.Priority != "" {
		out.Priority = override.Priority
	}
	if override.MaxEventBuffer != 0 {
		out.MaxEventBuffer = override.MaxEventBuffer
	}
	if override.Enabled {
		out.Enabled = true
	}
	return out
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}
