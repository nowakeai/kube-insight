package resourcematch

import (
	"path"
	"strings"

	"kube-insight/internal/core"
	"kube-insight/internal/kubeapi"
)

type Resource struct {
	Group    string
	Version  string
	Resource string
	Kind     string
	Name     string
}

func FromCoreRef(ref core.ResourceRef) Resource {
	return Resource{
		Group:    ref.Group,
		Version:  ref.Version,
		Resource: ref.Resource,
		Kind:     ref.Kind,
	}
}

func FromKubeAPI(info kubeapi.ResourceInfo) Resource {
	return Resource{
		Group:    info.Group,
		Version:  info.Version,
		Resource: info.Resource,
		Kind:     info.Kind,
	}
}

func MatchAnyResource(patterns []string, resource Resource) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, pattern := range patterns {
		if MatchResource(pattern, resource) {
			return true
		}
	}
	return false
}

func MatchResource(pattern string, resource Resource) bool {
	pattern = normalize(pattern)
	if pattern == "" {
		return false
	}
	for _, candidate := range ResourceCandidates(resource) {
		if match(pattern, candidate) {
			return true
		}
	}
	return false
}

func MatchAnyString(patterns []string, value string) bool {
	if len(patterns) == 0 {
		return true
	}
	value = normalize(value)
	for _, pattern := range patterns {
		pattern = normalize(pattern)
		if pattern == "" && value == "" {
			return true
		}
		if match(pattern, value) {
			return true
		}
	}
	return false
}

func ResourceCandidates(resource Resource) []string {
	group := normalize(resource.Group)
	version := normalize(resource.Version)
	name := normalize(resource.Name)
	res := normalize(resource.Resource)
	out := uniqueNonEmpty(name, res)
	if group != "" && res != "" {
		out = append(out, res+"."+group)
	}
	if group == "" && version != "" && res != "" {
		out = append(out, version+"/"+res)
	}
	if group != "" && version != "" && res != "" {
		out = append(out, group+"/"+version+"/"+res)
	}
	return uniqueNonEmpty(out...)
}

func IsPattern(value string) bool {
	value = strings.TrimSpace(value)
	return strings.ContainsAny(value, "*?[") || strings.Contains(value, "/")
}

func NormalizeValues(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = normalize(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func match(pattern, value string) bool {
	if pattern == "" {
		return false
	}
	if pattern == "*" || pattern == value {
		return true
	}
	ok, err := path.Match(pattern, value)
	return err == nil && ok
}

func normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func uniqueNonEmpty(values ...string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = normalize(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
