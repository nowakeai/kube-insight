package mcp

import (
	"context"
	"fmt"
	"strings"

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
	matches := make([]storage.ClusterRecord, 0, 1)
	for _, cluster := range clusters {
		if clusterAliasMatches(cluster, value) {
			matches = append(matches, cluster)
		}
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
		display := clusterDisplayAlias(cluster)
		if display != "" && display != cluster.Name {
			available = append(available, fmt.Sprintf("%s (id=%s)", display, cluster.Name))
			continue
		}
		available = append(available, cluster.Name)
	}
	return "", fmt.Errorf("cluster %q was not found; available clusters: %s", value, strings.Join(available, ", "))
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
