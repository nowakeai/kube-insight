package mcp

import (
	"strconv"
	"strings"
	"time"

	"kube-insight/internal/core"
	"kube-insight/internal/storage"
)

func formatSQLSchemaDSL(schema storage.SQLSchema) string {
	var b strings.Builder
	b.WriteString("kube_insight_sql_schema v1\n")
	if len(schema.Notes) > 0 {
		b.WriteString("notes:\n")
		for _, note := range schema.Notes {
			if strings.TrimSpace(note) == "" {
				continue
			}
			b.WriteString("- ")
			b.WriteString(strings.TrimSpace(note))
			b.WriteByte('\n')
		}
	}
	tables := schemaTablesForLLM(schema)
	if len(tables) > 0 {
		b.WriteString("tables:\n")
		for _, table := range tables {
			b.WriteString("table ")
			b.WriteString(table.Name)
			if table.Type != "" {
				b.WriteString(" type=")
				b.WriteString(table.Type)
			}
			if table.Description != "" {
				b.WriteString(" // ")
				b.WriteString(table.Description)
			}
			b.WriteByte('\n')
			if len(table.Columns) > 0 {
				columns := make([]string, 0, len(table.Columns))
				for _, column := range table.Columns {
					columns = append(columns, compactSchemaColumn(column))
				}
				b.WriteString("  columns: ")
				b.WriteString(strings.Join(columns, ", "))
				b.WriteByte('\n')
			}
			if len(table.Indexes) > 0 {
				indexes := make([]string, 0, len(table.Indexes))
				for _, index := range table.Indexes {
					indexes = append(indexes, compactSchemaIndex(index))
				}
				b.WriteString("  indexes: ")
				b.WriteString(strings.Join(indexes, "; "))
				b.WriteByte('\n')
			}
		}
	}
	if len(schema.Relationships) > 0 {
		b.WriteString("relationships:\n")
		for _, relationship := range schema.Relationships {
			b.WriteString("- ")
			b.WriteString(relationship.Name)
			b.WriteString(": ")
			b.WriteString(relationship.From)
			b.WriteString(" -> ")
			b.WriteString(relationship.To)
			if relationship.SQL != "" {
				b.WriteString("; join ")
				b.WriteString(oneLine(relationship.SQL))
			}
			if relationship.Description != "" {
				b.WriteString("; ")
				b.WriteString(relationship.Description)
			}
			b.WriteByte('\n')
		}
	}
	if len(schema.Recipes) > 0 {
		b.WriteString("recipes:\n")
		for _, recipe := range schema.Recipes {
			b.WriteString("recipe ")
			b.WriteString(recipe.Name)
			if recipe.Description != "" {
				b.WriteString(": ")
				b.WriteString(recipe.Description)
			}
			b.WriteByte('\n')
			if recipe.SQL != "" {
				b.WriteString(indentBlock(recipe.SQL, "  "))
				b.WriteByte('\n')
			}
		}
	}
	return strings.TrimSpace(b.String())
}

func schemaTablesForLLM(schema storage.SQLSchema) []storage.SQLSchemaTable {
	hasDescriptions := false
	for _, table := range schema.Tables {
		if table.Description != "" {
			hasDescriptions = true
			break
		}
	}
	if !hasDescriptions {
		return schema.Tables
	}
	tables := make([]storage.SQLSchemaTable, 0, len(schema.Tables))
	for _, table := range schema.Tables {
		if table.Description == "" {
			continue
		}
		tables = append(tables, table)
	}
	return tables
}

func compactSchemaColumn(column storage.SQLSchemaColumn) string {
	value := column.Name
	if column.Type != "" {
		value += ":" + column.Type
	}
	if column.Primary {
		value += " pk"
	}
	if column.NotNull {
		value += "!"
	}
	return value
}

func compactSchemaIndex(index storage.SQLSchemaIndex) string {
	prefix := index.Name
	if index.Unique {
		prefix += " unique"
	}
	return prefix + "(" + strings.Join(index.Columns, ",") + ")"
}

func oneLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func oneLineLimit(value string, maxRunes int) string {
	line := oneLine(value)
	if maxRunes <= 0 {
		return line
	}
	runes := []rune(line)
	if len(runes) <= maxRunes {
		return line
	}
	if maxRunes <= 1 {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-1]) + "..."
}

func indentBlock(value, prefix string) string {
	lines := strings.Split(strings.TrimSpace(value), "\n")
	for i, line := range lines {
		lines[i] = prefix + strings.TrimRight(line, " \t")
	}
	return strings.Join(lines, "\n")
}

func formatResourceHealthDSL(report storage.ResourceHealthReport, problemLimit int) string {
	if problemLimit <= 0 {
		problemLimit = 10
	}
	var b strings.Builder
	b.WriteString("kube_insight_health v1\n")
	if !report.CheckedAt.IsZero() {
		b.WriteString("checked_at: ")
		b.WriteString(report.CheckedAt.UTC().Format(time.RFC3339Nano))
		b.WriteByte('\n')
	}
	if clusters := compactHealthClusters(report.Resources); len(clusters) > 0 {
		b.WriteString("clusters:\n")
		for _, cluster := range clusters {
			b.WriteString("- ")
			b.WriteString(cluster)
			b.WriteByte('\n')
		}
	}
	b.WriteString("summary: ")
	b.WriteString(compactHealthSummary(report.Summary))
	b.WriteByte('\n')
	if len(report.ByStatus) > 0 {
		b.WriteString("by_status:")
		for status, count := range report.ByStatus {
			b.WriteByte(' ')
			b.WriteString(status)
			b.WriteByte('=')
			b.WriteString(strconv.Itoa(count))
		}
		b.WriteByte('\n')
	}
	for _, warning := range report.Summary.Warnings {
		if strings.TrimSpace(warning) == "" {
			continue
		}
		b.WriteString("warning: ")
		b.WriteString(strings.TrimSpace(warning))
		b.WriteByte('\n')
	}
	problems, hidden := healthProblemRecords(report.Resources, problemLimit)
	if len(problems) == 0 {
		b.WriteString("problem_resources: none\n")
	} else {
		b.WriteString("problem_resources:\n")
		for _, record := range problems {
			b.WriteString("- ")
			b.WriteString(compactHealthRecord(record))
			b.WriteByte('\n')
		}
		if hidden > 0 {
			b.WriteString("problem_resources_omitted: ")
			b.WriteString(strconv.Itoa(hidden))
			b.WriteByte('\n')
		}
	}
	return strings.TrimSpace(b.String())
}

func compactHealthClusters(records []storage.ResourceHealthRecord) []string {
	byID := map[string]storage.ResourceHealthRecord{}
	order := []string{}
	for _, record := range records {
		id := strings.TrimSpace(record.ClusterID)
		if id == "" {
			continue
		}
		existing, ok := byID[id]
		if !ok {
			byID[id] = record
			order = append(order, id)
			continue
		}
		if existing.ClusterSource == "" && record.ClusterSource != "" {
			existing.ClusterSource = record.ClusterSource
		}
		if existing.ClusterUID == "" && record.ClusterUID != "" {
			existing.ClusterUID = record.ClusterUID
		}
		byID[id] = existing
	}
	out := make([]string, 0, len(order))
	for _, id := range order {
		record := byID[id]
		parts := []string{"display=" + clusterDisplayName(record), "id=" + id}
		if record.ClusterUID != "" {
			parts = append(parts, "uid="+record.ClusterUID)
		}
		if record.ClusterSource != "" {
			parts = append(parts, "source="+oneLineLimit(record.ClusterSource, 160))
		}
		out = append(out, strings.Join(parts, " "))
	}
	return out
}

func clusterDisplayName(record storage.ResourceHealthRecord) string {
	if record.ClusterSource != "" {
		fields := strings.Fields(record.ClusterSource)
		if len(fields) > 0 {
			return fields[0]
		}
	}
	return record.ClusterID
}

func compactHealthSummary(summary storage.ResourceHealthSummary) string {
	parts := []string{
		"resources=" + strconv.Itoa(summary.Resources),
		"healthy=" + strconv.Itoa(summary.Healthy),
		"unstable=" + strconv.Itoa(summary.Unstable),
		"errors=" + strconv.Itoa(summary.Errors),
		"stale=" + strconv.Itoa(summary.Stale),
		"not_started=" + strconv.Itoa(summary.NotStarted),
		"queued=" + strconv.Itoa(summary.Queued),
		"skipped=" + strconv.Itoa(summary.Skipped),
		"complete=" + boolString(summary.Complete),
	}
	return strings.Join(parts, " ")
}

func healthProblemRecords(records []storage.ResourceHealthRecord, limit int) ([]storage.ResourceHealthRecord, int) {
	out := make([]storage.ResourceHealthRecord, 0, limit)
	hidden := 0
	for _, record := range records {
		if !isProblemHealthRecord(record) {
			continue
		}
		if len(out) < limit {
			out = append(out, record)
		} else {
			hidden++
		}
	}
	return out, hidden
}

func isProblemHealthRecord(record storage.ResourceHealthRecord) bool {
	if record.Error != "" || record.Stale || record.Skipped {
		return true
	}
	return !isHealthyHealthStatus(record.Status)
}

func isHealthyHealthStatus(status string) bool {
	switch status {
	case "watching", "bookmark", "queued":
		return true
	default:
		return false
	}
}

func compactHealthRecord(record storage.ResourceHealthRecord) string {
	parts := []string{
		"cluster=" + record.ClusterID,
		"resource=" + compactResourceName(record),
		"kind=" + record.Kind,
		"status=" + record.Status,
		"latest_objects=" + strconv.Itoa(record.LatestObjects),
	}
	if record.Namespace != "" {
		parts = append(parts, "namespace="+record.Namespace)
	}
	if record.ResourceVersion != "" {
		parts = append(parts, "rv="+record.ResourceVersion)
	}
	if record.AgeSeconds > 0 {
		parts = append(parts, "age_seconds="+strconv.FormatInt(record.AgeSeconds, 10))
	}
	if record.Stale {
		parts = append(parts, "stale=true")
	}
	if record.Skipped {
		parts = append(parts, "skipped=true")
	}
	if record.UpdatedAt != nil {
		parts = append(parts, "updated_at="+record.UpdatedAt.UTC().Format(time.RFC3339Nano))
	}
	if record.Error != "" {
		parts = append(parts, "error="+oneLineLimit(record.Error, 180))
	}
	return strings.Join(parts, " ")
}

func compactResourceName(record storage.ResourceHealthRecord) string {
	if record.Group == "" {
		return record.Resource + "/" + record.Version
	}
	return record.Resource + "." + record.Group + "/" + record.Version
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

type compactObjectRecord struct {
	LogicalID string `json:"logicalId,omitempty"`
	ClusterID string `json:"clusterId,omitempty"`
	Group     string `json:"group,omitempty"`
	Version   string `json:"version,omitempty"`
	Resource  string `json:"resource,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	UID       string `json:"uid,omitempty"`
}

type compactBundle struct {
	Object    compactObjectRecord      `json:"object"`
	Summary   storage.BundleSummary    `json:"summary"`
	Versions  []compactVersionEvidence `json:"versions,omitempty"`
	Facts     []compactFact            `json:"facts,omitempty"`
	Edges     []compactCoreEdge        `json:"edges,omitempty"`
	Changes   []compactChange          `json:"changes,omitempty"`
	Truncated bool                     `json:"truncated,omitempty"`
}

type compactVersionEvidence struct {
	ID              int64     `json:"id"`
	Sequence        int64     `json:"sequence"`
	ObservedAt      time.Time `json:"observedAt"`
	ResourceVersion string    `json:"resourceVersion,omitempty"`
	DocumentHash    string    `json:"documentHash,omitempty"`
	Materialization string    `json:"materialization,omitempty"`
	Strategy        string    `json:"strategy,omitempty"`
}

type compactFact struct {
	Time     time.Time `json:"time"`
	Key      string    `json:"key"`
	Value    string    `json:"value"`
	Severity int       `json:"severity"`
}

type compactCoreEdge struct {
	Type      string     `json:"type"`
	SourceID  string     `json:"sourceId"`
	TargetID  string     `json:"targetId"`
	ValidFrom time.Time  `json:"validFrom"`
	ValidTo   *time.Time `json:"validTo,omitempty"`
}

type compactChange struct {
	Time     time.Time `json:"time"`
	Family   string    `json:"family"`
	Path     string    `json:"path"`
	Op       string    `json:"op"`
	Old      string    `json:"old,omitempty"`
	New      string    `json:"new,omitempty"`
	Severity int       `json:"severity"`
}

type compactTopologyEdge struct {
	Type      string              `json:"type"`
	Source    compactObjectRecord `json:"source"`
	Target    compactObjectRecord `json:"target"`
	Direction string              `json:"direction,omitempty"`
	ValidFrom time.Time           `json:"validFrom"`
	ValidTo   *time.Time          `json:"validTo,omitempty"`
}

func compactEvidenceSearchResult(result storage.EvidenceSearchResult) any {
	out := struct {
		Input    storage.EvidenceSearchOptions   `json:"input"`
		Summary  storage.EvidenceSearchSummary   `json:"summary"`
		Coverage *storage.EvidenceSearchCoverage `json:"coverage,omitempty"`
		Matches  []storage.EvidenceSearchMatch   `json:"matches"`
		Bundles  []compactBundle                 `json:"bundles,omitempty"`
	}{Input: result.Input, Summary: result.Summary, Coverage: result.Coverage, Matches: result.Matches}
	if len(result.Bundles) > 0 {
		out.Bundles = make([]compactBundle, 0, len(result.Bundles))
		for _, bundle := range result.Bundles {
			out.Bundles = append(out.Bundles, compactEvidenceBundle(bundle))
		}
	}
	return out
}

func compactTopologyGraph(graph storage.TopologyGraph) any {
	out := struct {
		Root    compactObjectRecord     `json:"root"`
		Summary storage.TopologySummary `json:"summary"`
		Nodes   []compactObjectRecord   `json:"nodes"`
		Edges   []compactTopologyEdge   `json:"edges"`
	}{Root: compactObject(graph.Root), Summary: graph.Summary}
	out.Nodes = make([]compactObjectRecord, 0, len(graph.Nodes))
	for _, node := range graph.Nodes {
		out.Nodes = append(out.Nodes, compactObject(node))
	}
	out.Edges = make([]compactTopologyEdge, 0, len(graph.Edges))
	for _, edge := range graph.Edges {
		out.Edges = append(out.Edges, compactTopologyEdge{Type: edge.Type, Source: compactObject(edge.Source), Target: compactObject(edge.Target), Direction: edge.Direction, ValidFrom: edge.ValidFrom, ValidTo: edge.ValidTo})
	}
	return out
}

func compactServiceInvestigation(result storage.ServiceInvestigation) any {
	out := struct {
		Summary  storage.ServiceInvestigationSummary `json:"summary"`
		Service  compactBundle                       `json:"service"`
		Objects  []compactBundle                     `json:"objects"`
		Topology []compactTopologyEdge               `json:"topology"`
	}{Summary: result.Summary, Service: compactEvidenceBundle(result.Service)}
	out.Objects = make([]compactBundle, 0, len(result.Objects))
	for _, bundle := range result.Objects {
		out.Objects = append(out.Objects, compactEvidenceBundle(bundle))
	}
	out.Topology = make([]compactTopologyEdge, 0, len(result.Topology))
	for _, edge := range result.Topology {
		out.Topology = append(out.Topology, compactTopologyEdge{Type: edge.Type, Source: compactObject(edge.Source), Target: compactObject(edge.Target), Direction: edge.Direction, ValidFrom: edge.ValidFrom, ValidTo: edge.ValidTo})
	}
	return out
}

func compactEvidenceBundle(bundle storage.EvidenceBundle) compactBundle {
	out := compactBundle{Object: compactObject(bundle.Object), Summary: bundle.Summary}
	out.Versions = compactVersions(bundle.Versions, 3)
	out.Facts = compactFacts(bundle.Facts, 8)
	out.Edges = compactCoreEdges(bundle.Edges, 8)
	out.Changes = compactChanges(bundle.Changes, 8)
	out.Truncated = len(bundle.Versions) > len(out.Versions) || len(bundle.Facts) > len(out.Facts) || len(bundle.Edges) > len(out.Edges) || len(bundle.Changes) > len(out.Changes) || len(bundle.Latest) > 0 || len(bundle.VersionDiffs) > 0
	return out
}

func compactObject(object storage.ObjectRecord) compactObjectRecord {
	return compactObjectRecord{LogicalID: object.LogicalID, ClusterID: object.ClusterID, Group: object.Group, Version: object.Version, Resource: object.Resource, Kind: object.Kind, Namespace: object.Namespace, Name: object.Name, UID: object.UID}
}

func compactVersions(in []storage.VersionEvidence, limit int) []compactVersionEvidence {
	if len(in) == 0 {
		return nil
	}
	if len(in) < limit {
		limit = len(in)
	}
	out := make([]compactVersionEvidence, 0, limit)
	for _, version := range in[:limit] {
		out = append(out, compactVersionEvidence{ID: version.ID, Sequence: version.Sequence, ObservedAt: version.ObservedAt, ResourceVersion: version.ResourceVersion, DocumentHash: version.DocumentHash, Materialization: version.Materialization, Strategy: version.Strategy})
	}
	return out
}

func compactFacts(in []core.Fact, limit int) []compactFact {
	if len(in) == 0 {
		return nil
	}
	if len(in) < limit {
		limit = len(in)
	}
	out := make([]compactFact, 0, limit)
	for _, fact := range in[:limit] {
		out = append(out, compactFact{Time: fact.Time, Key: fact.Key, Value: fact.Value, Severity: fact.Severity})
	}
	return out
}

func compactCoreEdges(in []core.Edge, limit int) []compactCoreEdge {
	if len(in) == 0 {
		return nil
	}
	if len(in) < limit {
		limit = len(in)
	}
	out := make([]compactCoreEdge, 0, limit)
	for _, edge := range in[:limit] {
		out = append(out, compactCoreEdge{Type: edge.Type, SourceID: edge.SourceID, TargetID: edge.TargetID, ValidFrom: edge.ValidFrom, ValidTo: edge.ValidTo})
	}
	return out
}

func compactChanges(in []core.Change, limit int) []compactChange {
	if len(in) == 0 {
		return nil
	}
	if len(in) < limit {
		limit = len(in)
	}
	out := make([]compactChange, 0, limit)
	for _, change := range in[:limit] {
		out = append(out, compactChange{Time: change.Time, Family: change.Family, Path: change.Path, Op: change.Op, Old: change.Old, New: change.New, Severity: change.Severity})
	}
	return out
}
