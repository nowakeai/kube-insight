package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type evidenceArtifactDraft struct {
	Kind         string
	Title        string
	Data         json.RawMessage
	CitationText string
	Target       json.RawMessage
}

func (r einoRunRecorder) appendEvidenceArtifacts(ctx context.Context, toolName string, output json.RawMessage) error {
	for _, draft := range evidenceArtifactsFromToolOutput(toolName, output) {
		artifactID := NewArtifactID()
		artifactData := evidenceArtifactDataWithCitation(draft.Data, draft.CitationText, draft.Target)
		if err := r.append(ctx, EventArtifact, ArtifactEventData{Artifact: Artifact{ID: artifactID, Kind: draft.Kind, Title: draft.Title, Data: artifactData}}); err != nil {
			return err
		}
	}
	return nil
}

func evidenceArtifactsFromToolOutput(toolName string, output json.RawMessage) []evidenceArtifactDraft {
	value, ok := toolEvidenceValue(output)
	if !ok {
		return nil
	}
	switch toolName {
	case "kube_insight_health":
		return healthEvidenceArtifacts(value)
	case "kube_insight_sql":
		return sqlEvidenceArtifacts(value)
	case "kube_insight_search":
		return searchEvidenceArtifacts(value)
	case "kube_insight_topology":
		return topologyEvidenceArtifacts(value)
	case "kube_insight_history":
		return historyEvidenceArtifacts(value)
	case "kube_insight_service_investigation":
		return serviceEvidenceArtifacts(value)
	default:
		return nil
	}
}

func toolEvidenceValue(output json.RawMessage) (any, bool) {
	if len(output) == 0 {
		return nil, false
	}
	var value any
	if err := json.Unmarshal(output, &value); err != nil {
		return nil, false
	}
	if record, ok := value.(map[string]any); ok {
		if inner, ok := mcpTextContentValue(record); ok {
			return inner, true
		}
	}
	return value, true
}

func healthEvidenceArtifacts(value any) []evidenceArtifactDraft {
	markdown := evidenceMarkdown("Health evidence", value)
	if markdown == "" {
		return nil
	}
	return []evidenceArtifactDraft{{
		Kind:         ArtifactKindMarkdown,
		Title:        "Health evidence",
		Data:         jsonRaw(map[string]any{"markdown": markdown}),
		CitationText: "Health evidence",
		Target:       jsonRaw(map[string]any{"type": CitationTargetArtifact, "source": "kube_insight_health"}),
	}}
}

func sqlEvidenceArtifacts(value any) []evidenceArtifactDraft {
	record := valueRecord(value)
	if record == nil {
		markdown := evidenceMarkdown("SQL evidence", value)
		if markdown == "" {
			return nil
		}
		return []evidenceArtifactDraft{{
			Kind:         ArtifactKindMarkdown,
			Title:        "SQL evidence",
			Data:         jsonRaw(map[string]any{"markdown": markdown}),
			CitationText: "SQL evidence",
			Target:       jsonRaw(map[string]any{"type": CitationTargetArtifact, "source": "kube_insight_sql"}),
		}}
	}
	rows, _ := record["rows"].([]any)
	rowCount := len(rows)
	if count, ok := record["rowCount"]; ok {
		rowCount = intNumber(count, rowCount)
	}
	title := sqlEvidenceTitle(record, rowCount)
	markdown := sqlEvidenceMarkdown(title, record)
	if markdown == "" {
		markdown = evidenceMarkdown(title, value)
	}
	return []evidenceArtifactDraft{{
		Kind:         ArtifactKindMarkdown,
		Title:        title,
		Data:         jsonRaw(map[string]any{"markdown": markdown}),
		CitationText: title,
		Target:       jsonRaw(map[string]any{"type": CitationTargetSQLRow, "source": "kube_insight_sql", "rowCount": rowCount}),
	}}
}

func sqlEvidenceTitle(record map[string]any, rowCount int) string {
	rows, _ := record["rows"].([]any)
	if rowCount <= 0 {
		rowCount = len(rows)
	}
	var kind, factValue, factKey string
	columnsText := strings.ToLower(fmt.Sprint(record["columns"]))
	for _, value := range rows {
		row := valueRecord(value)
		if row == nil {
			continue
		}
		if kind == "" {
			kind = textField(row, "kind")
		}
		if factValue == "" {
			factValue = textField(row, "fact_value")
		}
		if factKey == "" {
			factKey = textField(row, "fact_key")
		}
	}
	subject := "SQL evidence"
	switch {
	case strings.EqualFold(factValue, "OOMKilled") || strings.Contains(columnsText, "oom_count") || strings.Contains(columnsText, "oom"):
		subject = "OOMKilled facts"
	case strings.Contains(strings.ToLower(factKey), "restart") || strings.Contains(columnsText, "restart"):
		subject = "Restart facts"
	case factValue != "":
		subject = factValue + " facts"
	case factKey != "":
		subject = factKey + " facts"
	}
	if kind != "" && strings.Contains(strings.ToLower(subject), "facts") {
		subject = subject + " by " + kind
	}
	return fmt.Sprintf("%s (%d rows)", subject, rowCount)
}

func sqlEvidenceMarkdown(title string, record map[string]any) string {
	rows, _ := record["rows"].([]any)
	if len(rows) == 0 {
		return "### " + title + "\n\nNo rows returned."
	}
	columns := stringList(record["columns"])
	if len(columns) == 0 {
		for key := range valueRecord(rows[0]) {
			columns = append(columns, key)
		}
	}
	if len(columns) == 0 {
		return ""
	}
	if len(columns) > 8 {
		columns = columns[:8]
	}
	var b strings.Builder
	b.WriteString("### ")
	b.WriteString(title)
	b.WriteString("\n\n")
	writeMarkdownTableRow(&b, columns)
	separators := make([]string, len(columns))
	for i := range separators {
		separators[i] = "---"
	}
	writeMarkdownTableRow(&b, separators)
	limit := len(rows)
	if limit > 20 {
		limit = 20
	}
	for _, value := range rows[:limit] {
		row := valueRecord(value)
		values := make([]string, 0, len(columns))
		for _, column := range columns {
			values = append(values, markdownCell(textField(row, column)))
		}
		writeMarkdownTableRow(&b, values)
	}
	if len(rows) > limit {
		b.WriteString("\nShowing first ")
		b.WriteString(fmt.Sprint(limit))
		b.WriteString(" rows.")
	}
	return b.String()
}

func writeMarkdownTableRow(b *strings.Builder, values []string) {
	b.WriteString("| ")
	b.WriteString(strings.Join(values, " | "))
	b.WriteString(" |\n")
}

func markdownCell(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.TrimSpace(value)
	if len(value) > 96 {
		value = value[:93] + "..."
	}
	return value
}

func stringList(value any) []string {
	values, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if text := strings.TrimSpace(fmt.Sprint(value)); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func searchEvidenceArtifacts(value any) []evidenceArtifactDraft {
	items := resourceListItems(value)
	if len(items) == 0 {
		return nil
	}
	data := jsonRaw(map[string]any{"title": "Search evidence", "items": items})
	return []evidenceArtifactDraft{{Kind: ArtifactKindK8sResourceList, Title: evidenceTitle("Search evidence", items), Data: data, CitationText: evidenceTitle("Search evidence", items), Target: jsonRaw(map[string]any{"type": CitationTargetArtifact, "source": "kube_insight_search"})}}
}

func serviceEvidenceArtifacts(value any) []evidenceArtifactDraft {
	var out []evidenceArtifactDraft
	items := resourceListItems(value)
	if len(items) > 0 {
		data := jsonRaw(map[string]any{"title": "Service investigation evidence", "items": items})
		out = append(out, evidenceArtifactDraft{Kind: ArtifactKindK8sResourceList, Title: evidenceTitle("Service evidence", items), Data: data, CitationText: evidenceTitle("Service evidence", items), Target: jsonRaw(map[string]any{"type": CitationTargetArtifact, "source": "kube_insight_service_investigation"})})
	}
	out = append(out, topologyEvidenceArtifacts(value)...)
	return out
}

func topologyEvidenceArtifacts(value any) []evidenceArtifactDraft {
	data, count := topologyArtifactData(value)
	if count == 0 {
		return nil
	}
	title := fmt.Sprintf("Topology evidence (%d nodes)", count)
	return []evidenceArtifactDraft{{Kind: ArtifactKindK8sTopology, Title: title, Data: data, CitationText: title, Target: jsonRaw(map[string]any{"type": CitationTargetArtifact, "source": "kube_insight_topology"})}}
}

func historyEvidenceArtifacts(value any) []evidenceArtifactDraft {
	record, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	versions, ok := record["versions"].([]any)
	if !ok || len(versions) == 0 {
		return nil
	}
	object := valueRecord(record["object"])
	data := map[string]any{"versions": versions}
	if changes, ok := record["changes"].([]any); ok && len(changes) > 0 {
		data["changes"] = changes
	}
	if diffs, ok := record["diffs"].([]any); ok && len(diffs) > 0 {
		data["diffs"] = diffs
	}
	if object != nil {
		data["identity"] = object
		data["title"] = objectLabel(object)
	}
	title := fmt.Sprintf("History evidence (%d versions)", len(versions))
	if object != nil {
		title = "History: " + objectLabel(object)
	}
	return []evidenceArtifactDraft{{Kind: ArtifactKindK8sHistory, Title: title, Data: jsonRaw(data), CitationText: title, Target: jsonRaw(map[string]any{"type": CitationTargetArtifact, "source": "kube_insight_history"})}}
}

func resourceListItems(value any) []map[string]any {
	record := valueRecord(value)
	if record == nil {
		return nil
	}
	var items []map[string]any
	if bundles, ok := record["bundles"].([]any); ok {
		items = append(items, bundleItems(bundles)...)
	}
	if service := valueRecord(record["service"]); service != nil {
		items = append(items, bundleItem(service))
	}
	if objects, ok := record["objects"].([]any); ok {
		items = append(items, bundleItems(objects)...)
	}
	if len(items) == 0 {
		if matches, ok := record["matches"].([]any); ok {
			items = append(items, matchItems(matches)...)
		}
	}
	return items
}

func bundleItems(values []any) []map[string]any {
	out := make([]map[string]any, 0, len(values))
	for _, value := range values {
		if item := bundleItem(valueRecord(value)); item != nil {
			out = append(out, item)
		}
	}
	return out
}

func bundleItem(bundle map[string]any) map[string]any {
	if bundle == nil {
		return nil
	}
	object := valueRecord(bundle["object"])
	if object == nil {
		return nil
	}
	item := map[string]any{
		"identity": object,
		"summary":  bundleSummaryLines(bundle),
	}
	if facts := factSummaryValues(bundle["facts"]); len(facts) > 0 {
		item["facts"] = facts
	}
	if changes := changeSummaryValues(bundle["changes"]); len(changes) > 0 {
		item["changes"] = changes
	}
	if reasons := stringValues(bundle["reasons"]); len(reasons) > 0 {
		item["reasons"] = reasons
	}
	if summary := valueRecord(bundle["summary"]); summary != nil {
		if score, ok := summary["evidenceScore"]; ok {
			item["score"] = score
		}
		if rank, ok := summary["rank"]; ok {
			item["reason"] = fmt.Sprintf("rank %v", rank)
		}
	}
	return item
}

func matchItems(values []any) []map[string]any {
	out := make([]map[string]any, 0, len(values))
	for _, value := range values {
		match := valueRecord(value)
		if match == nil {
			continue
		}
		source := match
		if object := valueRecord(match["object"]); object != nil {
			source = object
		}
		identity := map[string]any{}
		for _, key := range []string{"clusterId", "group", "version", "resource", "kind", "namespace", "name", "uid"} {
			if value := textField(source, key); value != "" {
				identity[key] = value
			}
		}
		if len(identity) == 0 {
			continue
		}
		item := map[string]any{"identity": identity}
		if reason, ok := match["reason"]; ok {
			item["reason"] = reason
		}
		if reasons := stringValues(match["reasons"]); len(reasons) > 0 {
			item["reasons"] = reasons
		}
		if facts := factSummaryValues(match["facts"]); len(facts) > 0 {
			item["facts"] = facts
		}
		if score, ok := match["score"]; ok {
			item["score"] = score
		}
		out = append(out, item)
	}
	return out
}

func stringValues(value any) []string {
	values, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		text := strings.TrimSpace(fmt.Sprint(value))
		if text != "" {
			out = append(out, text)
		}
	}
	return out
}

func factSummaryValues(value any) []string {
	values, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		record := valueRecord(value)
		if record == nil {
			continue
		}
		key := firstNonEmptyString(textField(record, "factKey"), textField(record, "key"), textField(record, "name"))
		factValue := firstNonEmptyString(textField(record, "factValue"), textField(record, "value"))
		if key == "" {
			continue
		}
		if factValue != "" {
			out = append(out, key+"="+factValue)
		} else {
			out = append(out, key)
		}
	}
	return out
}

func changeSummaryValues(value any) []string {
	values, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		record := valueRecord(value)
		if record == nil {
			continue
		}
		parts := nonEmptyStrings(textField(record, "changeType"), textField(record, "fieldPath"), textField(record, "lastObservedAt"))
		if len(parts) > 0 {
			out = append(out, strings.Join(parts, " "))
		}
	}
	return out
}

func bundleSummaryLines(bundle map[string]any) []string {
	summary := valueRecord(bundle["summary"])
	if summary == nil {
		return nil
	}
	keys := []string{"facts", "edges", "changes", "versions", "versionDiffs"}
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		if v, ok := summary[key]; ok {
			lines = append(lines, fmt.Sprintf("%s=%v", key, v))
		}
	}
	return lines
}

func topologyArtifactData(value any) (json.RawMessage, int) {
	record := valueRecord(value)
	if record == nil {
		return nil, 0
	}
	nodes := topologyNodes(record)
	edges := topologyEdges(record)
	if len(nodes) == 0 {
		return nil, 0
	}
	return jsonRaw(map[string]any{"title": "Topology evidence", "nodes": nodes, "edges": edges}), len(nodes)
}

func topologyNodes(record map[string]any) []map[string]any {
	seen := map[string]bool{}
	var nodes []map[string]any
	addObject := func(value any) {
		object := valueRecord(value)
		if object == nil {
			return
		}
		id := objectID(object)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		nodes = append(nodes, map[string]any{"id": id, "label": objectLabel(object), "kind": textField(object, "kind"), "namespace": textField(object, "namespace"), "name": textField(object, "name")})
	}
	addObject(record["root"])
	if nodeValues, ok := record["nodes"].([]any); ok {
		for _, node := range nodeValues {
			addObject(node)
		}
	}
	if service := valueRecord(record["service"]); service != nil {
		addObject(service["object"])
	}
	if objects, ok := record["objects"].([]any); ok {
		for _, bundle := range objects {
			if bundleRecord := valueRecord(bundle); bundleRecord != nil {
				addObject(bundleRecord["object"])
			}
		}
	}
	if edgeValues, ok := record["topology"].([]any); ok {
		for _, edge := range edgeValues {
			edgeRecord := valueRecord(edge)
			addObject(firstNonNil(edgeRecord["source"], edgeRecord["src"]))
			addObject(firstNonNil(edgeRecord["target"], edgeRecord["dst"]))
		}
	}
	if edgeValues, ok := record["edges"].([]any); ok {
		for _, edge := range edgeValues {
			edgeRecord := valueRecord(edge)
			addObject(firstNonNil(edgeRecord["source"], edgeRecord["src"]))
			addObject(firstNonNil(edgeRecord["target"], edgeRecord["dst"]))
		}
	}
	return nodes
}

func topologyEdges(record map[string]any) []map[string]any {
	values, _ := record["edges"].([]any)
	if len(values) == 0 {
		values, _ = record["topology"].([]any)
	}
	out := make([]map[string]any, 0, len(values))
	for _, value := range values {
		edge := valueRecord(value)
		if edge == nil {
			continue
		}
		source := objectID(valueRecord(firstNonNil(edge["source"], edge["src"])))
		target := objectID(valueRecord(firstNonNil(edge["target"], edge["dst"])))
		if source == "" || target == "" {
			continue
		}
		label := textField(edge, "type")
		out = append(out, map[string]any{"id": source + "->" + target + ":" + label, "source": source, "target": target, "label": label})
	}
	return out
}

func evidenceTitle(prefix string, items []map[string]any) string {
	if prefix == "Search evidence" {
		if symptom := resourceListSymptom(items); symptom != "" {
			if kind := commonResourceKind(items); kind != "" {
				return fmt.Sprintf("%s %s candidates (%d resources)", symptom, kind, len(items))
			}
			return fmt.Sprintf("%s candidates (%d resources)", symptom, len(items))
		}
	}
	return fmt.Sprintf("%s (%d resources)", prefix, len(items))
}

func resourceListSymptom(items []map[string]any) string {
	text := strings.ToLower(fmt.Sprint(items))
	switch {
	case strings.Contains(text, "oomkilled") || strings.Contains(text, "oom"):
		return "OOMKilled"
	case strings.Contains(text, "crashloopbackoff"):
		return "CrashLoopBackOff"
	case strings.Contains(text, "imagepullbackoff"):
		return "ImagePullBackOff"
	case strings.Contains(text, "failedscheduling"):
		return "FailedScheduling"
	case strings.Contains(text, "evicted"):
		return "Evicted"
	case strings.Contains(text, "restart"):
		return "Restart"
	}
	return ""
}

func commonResourceKind(items []map[string]any) string {
	var kind string
	for _, item := range items {
		identity := valueRecord(item["identity"])
		current := textField(identity, "kind")
		if current == "" {
			continue
		}
		if kind == "" {
			kind = current
			continue
		}
		if kind != current {
			return ""
		}
	}
	return kind
}

func valueRecord(value any) map[string]any {
	if value == nil {
		return nil
	}
	record, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	return record
}

func objectID(object map[string]any) string {
	if object == nil {
		return ""
	}
	parts := []string{textField(object, "clusterId"), textField(object, "kind"), textField(object, "namespace"), textField(object, "name")}
	return strings.Join(nonEmptyStrings(parts...), "/")
}

func objectLabel(object map[string]any) string {
	parts := []string{textField(object, "clusterId"), textField(object, "kind"), textField(object, "namespace"), textField(object, "name")}
	return strings.Join(nonEmptyStrings(parts...), "/")
}

func evidenceMarkdown(title string, value any) string {
	if text, ok := value.(string); ok {
		text = strings.TrimSpace(text)
		if text == "" {
			return ""
		}
		return "### " + title + "\n\n```text\n" + text + "\n```"
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil || len(data) == 0 {
		return ""
	}
	return "### " + title + "\n\n```json\n" + string(data) + "\n```"
}

func intNumber(value any, fallback int) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		if parsed, err := typed.Int64(); err == nil {
			return int(parsed)
		}
	}
	return fallback
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func textField(record map[string]any, key string) string {
	if record == nil {
		return ""
	}
	value, ok := record[key]
	if !ok {
		return ""
	}
	return fmt.Sprint(value)
}

func nonEmptyStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}
