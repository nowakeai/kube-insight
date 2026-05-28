package mcp

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"kube-insight/internal/storage"
)

type clusterListStore interface {
	ListClusters(context.Context) ([]storage.ClusterRecord, error)
}

func resolveClusterID(ctx context.Context, store ReadStore, input string) (string, error) {
	value := strings.TrimSpace(input)
	if value == "" {
		return "", nil
	}
	clusterStore, ok := store.(clusterListStore)
	if !ok {
		return value, nil
	}
	clusters, err := clusterStore.ListClusters(ctx)
	if err != nil {
		return "", err
	}
	if len(clusters) == 0 {
		return value, nil
	}
	matches := matchingClusters(clusters, value, clusterAliasMatches)
	if len(matches) == 1 && clusterHasEvidence(matches[0]) {
		return matches[0].Name, nil
	}
	fuzzyMatches := matchingClusters(clusters, value, clusterAliasFuzzyMatches)
	if len(matches) == 1 && !clusterHasEvidence(matches[0]) {
		datafulFuzzy := make([]storage.ClusterRecord, 0, len(fuzzyMatches))
		for _, match := range fuzzyMatches {
			if match.Name != matches[0].Name && clusterHasEvidence(match) {
				datafulFuzzy = append(datafulFuzzy, match)
			}
		}
		if len(datafulFuzzy) == 1 {
			return datafulFuzzy[0].Name, nil
		}
	}
	if len(matches) == 0 {
		matches = fuzzyMatches
	}
	if len(matches) == 1 {
		return matches[0].Name, nil
	}
	if len(matches) > 1 {
		names := make([]string, 0, len(matches))
		for _, match := range matches {
			names = append(names, match.Name)
		}
		return "", fmt.Errorf("cluster %q is ambiguous; matching cluster ids: %s", value, strings.Join(names, ", "))
	}
	available := make([]string, 0, len(clusters))
	for _, cluster := range clusters {
		if strings.TrimSpace(cluster.Name) == "" {
			continue
		}
		display := clusterDisplayAlias(cluster)
		if display != "" && display != cluster.Name {
			available = append(available, fmt.Sprintf("%s (id=%s)", display, cluster.Name))
			continue
		}
		available = append(available, cluster.Name)
	}
	return "", fmt.Errorf("cluster %q was not found; available clusters: %s", value, strings.Join(available, ", "))
}

func clusterHasEvidence(cluster storage.ClusterRecord) bool {
	return cluster.Objects > 0 || cluster.Versions > 0 || cluster.Latest > 0
}

func matchingClusters(clusters []storage.ClusterRecord, input string, match func(storage.ClusterRecord, string) bool) []storage.ClusterRecord {
	matches := make([]storage.ClusterRecord, 0, 1)
	for _, cluster := range clusters {
		if strings.TrimSpace(cluster.Name) == "" {
			continue
		}
		if match(cluster, input) {
			matches = append(matches, cluster)
		}
	}
	return matches
}

func clusterAliasMatches(cluster storage.ClusterRecord, input string) bool {
	want := normalizeClusterAlias(input)
	for _, alias := range clusterAliases(cluster) {
		if normalizeClusterAlias(alias) == want {
			return true
		}
	}
	return false
}

func clusterAliasFuzzyMatches(cluster storage.ClusterRecord, input string) bool {
	wantTokens := clusterAliasTokens(input)
	if len(wantTokens) < 2 {
		return false
	}
	wantCompact := compactClusterAlias(input)
	for _, alias := range clusterAliases(cluster) {
		aliasTokens := clusterAliasTokens(alias)
		if tokenSubsequence(wantTokens, aliasTokens) {
			return true
		}
		if len(wantCompact) >= 4 && strings.Contains(compactClusterAlias(alias), wantCompact) {
			return true
		}
	}
	return false
}

func clusterAliases(cluster storage.ClusterRecord) []string {
	aliases := []string{cluster.Name, cluster.UID, cluster.Source, clusterDisplayAlias(cluster)}
	if cluster.Source != "" {
		aliases = append(aliases, strings.Fields(cluster.Source)...)
	}
	out := make([]string, 0, len(aliases))
	seen := map[string]bool{}
	for _, alias := range aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" || seen[alias] {
			continue
		}
		seen[alias] = true
		out = append(out, alias)
	}
	return out
}

func clusterDisplayAlias(cluster storage.ClusterRecord) string {
	if cluster.Source != "" {
		fields := strings.Fields(cluster.Source)
		if len(fields) > 0 {
			return fields[0]
		}
	}
	return cluster.Name
}

func normalizeClusterAlias(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func compactClusterAlias(value string) string {
	return strings.Join(clusterAliasTokens(value), "")
}

func clusterAliasTokens(value string) []string {
	tokens := make([]string, 0, 4)
	var b strings.Builder
	lastClass := runeClassNone
	flush := func() {
		if b.Len() == 0 {
			return
		}
		tokens = append(tokens, strings.ToLower(b.String()))
		b.Reset()
	}
	for _, r := range value {
		class := runeClass(r)
		if class == runeClassNone {
			flush()
			lastClass = runeClassNone
			continue
		}
		if b.Len() > 0 && lastClass != runeClassNone && class != lastClass {
			flush()
		}
		b.WriteRune(unicode.ToLower(r))
		lastClass = class
	}
	flush()
	return tokens
}

type aliasRuneClass int

const (
	runeClassNone aliasRuneClass = iota
	runeClassLetter
	runeClassDigit
)

func runeClass(r rune) aliasRuneClass {
	switch {
	case unicode.IsLetter(r):
		return runeClassLetter
	case unicode.IsDigit(r):
		return runeClassDigit
	default:
		return runeClassNone
	}
}

func tokenSubsequence(needles, haystack []string) bool {
	if len(needles) == 0 {
		return false
	}
	next := 0
	for _, token := range haystack {
		if token == needles[next] {
			next++
			if next == len(needles) {
				return true
			}
		}
	}
	return false
}
