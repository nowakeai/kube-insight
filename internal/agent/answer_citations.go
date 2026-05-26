package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

const maxVerifiedAnswerCitations = 8

var (
	evidenceTokenPattern = regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9._:/-]{2,}`)
	evidenceLabelPattern = regexp.MustCompile(`\{\{evidence:\s*([^{}\n]{1,64})\s*\}\}`)
)

type verifiedAnswerCitation struct {
	Citation Citation
	Tokens   []string
}

type answerCitationCandidate struct {
	Artifact Artifact
	Meta     evidenceCitationMetadata
	Tokens   []string
	Source   string
	Text     string
}

func (r einoRunRecorder) verifiedAnswerCitations(ctx context.Context, finalAnswer string) ([]verifiedAnswerCitation, error) {
	finalAnswer = strings.TrimSpace(finalAnswer)
	if !r.enabled() || finalAnswer == "" {
		return nil, nil
	}
	events, err := r.store.ListRunEvents(ctx, r.runID)
	if err != nil {
		return nil, err
	}
	answer := strings.ToLower(finalAnswer)
	candidates := answerCitationCandidates(events)
	seenArtifacts := map[string]bool{}
	citations := make([]verifiedAnswerCitation, 0, maxVerifiedAnswerCitations)
	appendCitation := func(candidate answerCitationCandidate, label string) {
		seenArtifacts[candidate.Artifact.ID] = true
		text := firstNonEmptyString(label, candidate.Meta.Text, candidate.Artifact.Title, candidate.Artifact.Kind)
		citation := Citation{
			ID:         NewCitationID(),
			ArtifactID: candidate.Artifact.ID,
			Text:       text,
			Target:     candidate.Meta.Target,
		}
		if len(citation.Target) == 0 {
			citation.Target = jsonRaw(map[string]any{"type": CitationTargetArtifact, "artifactId": candidate.Artifact.ID})
		}
		citations = append(citations, verifiedAnswerCitation{Citation: citation, Tokens: candidate.Tokens})
	}
	labels := uniqueEvidenceLabels(finalAnswer)
	for _, label := range labels {
		if len(citations) >= maxVerifiedAnswerCitations {
			break
		}
		if candidate, ok := bestCandidateForEvidenceLabel(label, candidates, seenArtifacts, answer); ok {
			appendCitation(candidate, label)
		}
	}
	if len(labels) > 0 && len(citations) > 0 {
		return citations, nil
	}
	for _, candidate := range candidates {
		if len(citations) >= maxVerifiedAnswerCitations {
			break
		}
		if seenArtifacts[candidate.Artifact.ID] || !answerMentionsEvidence(answer, candidate.Tokens) {
			continue
		}
		appendCitation(candidate, "")
	}
	return citations, nil
}

func answerCitationCandidates(events []RunEvent) []answerCitationCandidate {
	candidates := make([]answerCitationCandidate, 0, len(events))
	seenArtifacts := map[string]bool{}
	for _, event := range events {
		if event.Type != EventArtifact {
			continue
		}
		var data ArtifactEventData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			continue
		}
		artifact := data.Artifact
		if artifact.ID == "" || seenArtifacts[artifact.ID] {
			continue
		}
		if artifact.Kind == ArtifactKindToolCall && !toolCallArtifactCanSupportCitation(artifact) {
			continue
		}
		seenArtifacts[artifact.ID] = true
		meta := citationMetadataFromArtifact(artifact)
		source := citationTargetSource(meta.Target)
		if source == "" && artifact.Kind == ArtifactKindToolCall {
			source = toolCallArtifactName(artifact)
		}
		candidates = append(candidates, answerCitationCandidate{
			Artifact: artifact,
			Meta:     meta,
			Tokens:   evidenceCitationTokens(artifact),
			Source:   source,
			Text:     strings.ToLower(artifact.Title + " " + string(artifact.Data)),
		})
	}
	return candidates
}

func uniqueEvidenceLabels(answer string) []string {
	seen := map[string]bool{}
	var out []string
	for _, label := range evidenceLabelsInBlock(answer) {
		key := strings.ToLower(label)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, label)
	}
	return out
}

func bestCandidateForEvidenceLabel(label string, candidates []answerCitationCandidate, seen map[string]bool, answer string) (answerCitationCandidate, bool) {
	bestIndex := -1
	bestScore := 0
	for index, candidate := range candidates {
		if seen[candidate.Artifact.ID] {
			continue
		}
		score := evidenceLabelCandidateScore(label, candidate, answer)
		if score > bestScore {
			bestScore = score
			bestIndex = index
		}
	}
	if bestIndex < 0 || bestScore < 25 {
		return answerCitationCandidate{}, false
	}
	return candidates[bestIndex], true
}

func evidenceLabelCandidateScore(label string, candidate answerCitationCandidate, answer string) int {
	label = strings.ToLower(label)
	text := candidate.Text
	score := 0
	if answerMentionsEvidence(answer, candidate.Tokens) {
		score += 5
	}
	if strings.Contains(label, "health") || strings.Contains(label, "collector") || strings.Contains(label, "收集") {
		if candidate.Source == "kube_insight_health" {
			score += 90
		}
		if strings.Contains(text, "health") || strings.Contains(text, "collector") {
			score += 20
		}
	}
	if strings.Contains(label, "oom") || strings.Contains(label, "restart") || strings.Contains(label, "facts") || strings.Contains(label, "事实") {
		if candidate.Source == "kube_insight_sql" || candidate.Source == scriptedQueryToolName {
			score += 55
		}
		if strings.Contains(text, "oomkilled") || strings.Contains(text, "oom") {
			score += 35
		}
		if strings.Contains(text, "fact_key") || strings.Contains(text, "factkey") || strings.Contains(text, "facts") {
			score += 20
		}
		if candidate.Source == "kube_insight_search" {
			score += 10
		}
	}
	if strings.Contains(label, "history") || strings.Contains(label, "历史") {
		if candidate.Source == "kube_insight_history" {
			score += 70
		}
	}
	if strings.Contains(label, "change") || strings.Contains(label, "changes") || strings.Contains(label, "recent") || strings.Contains(label, "变更") {
		if candidate.Source == "kube_insight_sql" || candidate.Source == scriptedQueryToolName {
			score += 45
		}
		if strings.Contains(text, "change") || strings.Contains(text, "changes") || strings.Contains(text, "change_family") {
			score += 35
		}
	}
	if strings.Contains(label, "topology") || strings.Contains(label, "edge") || strings.Contains(label, "impact") || strings.Contains(label, "拓扑") {
		if candidate.Source == "kube_insight_topology" || candidate.Source == "kube_insight_service_investigation" {
			score += 60
		}
		if strings.Contains(text, "topology") || strings.Contains(text, "edge") || strings.Contains(text, "endpointslice") {
			score += 35
		}
	}
	if strings.Contains(label, "search") || strings.Contains(label, "candidate") || strings.Contains(label, "resource") || strings.Contains(label, "候选") || strings.Contains(label, "资源") {
		if candidate.Source == "kube_insight_search" {
			score += 60
		}
	}
	if strings.Contains(label, "scripted") || strings.Contains(label, "sql") || strings.Contains(label, "rollup") || strings.Contains(label, "capacity") || strings.Contains(label, "容量") {
		if candidate.Source == scriptedQueryToolName {
			score += 65
		}
		if candidate.Source == "kube_insight_sql" {
			score += 45
		}
		if strings.Contains(text, "node_capacity") || strings.Contains(text, "numeric_value") || strings.Contains(text, "rowcount") {
			score += 25
		}
	}
	for _, word := range strings.Fields(label) {
		word = strings.Trim(word, "`*_[](){}<>#.,;:")
		if len(word) >= 3 && strings.Contains(text, word) {
			score += 8
		}
	}
	return score
}

func toolCallArtifactCanSupportCitation(artifact Artifact) bool {
	var record map[string]any
	if len(artifact.Data) == 0 || json.Unmarshal(artifact.Data, &record) != nil {
		return false
	}
	name := textField(record, "name")
	if name == parallelInvestigationToolName || name == evidenceCondenserToolName || name == scriptedQueryToolName {
		return true
	}
	output := valueRecord(record["output"])
	return output != nil && textField(output, "tool") == parallelInvestigationToolName
}

func toolCallArtifactName(artifact Artifact) string {
	var record map[string]any
	if len(artifact.Data) == 0 || json.Unmarshal(artifact.Data, &record) != nil {
		return ""
	}
	return textField(record, "name")
}

func citationTargetSource(target json.RawMessage) string {
	var record map[string]any
	if len(target) == 0 || json.Unmarshal(target, &record) != nil {
		return ""
	}
	return textField(record, "source")
}

func annotateAnswerWithEvidenceReferences(answer string, citations []verifiedAnswerCitation) string {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return answer
	}
	if len(citations) == 0 {
		return strings.TrimSpace(stripEvidenceLabels(answer))
	}
	blocks := markdownBlocks(answer)
	if len(blocks) == 0 {
		return strings.TrimSpace(stripEvidenceLabels(answer))
	}
	markersByBlock := map[int][]string{}
	labelsByBlock := map[int][]string{}
	labelCursorByBlock := map[int]int{}
	lastTextBlock := 0
	for index, block := range blocks {
		labelsByBlock[index] = evidenceLabelsInBlock(block)
		if strings.TrimSpace(stripEvidenceLabels(block)) != "" {
			lastTextBlock = index
		}
	}
	for index, citation := range citations {
		if citation.Citation.ID == "" {
			continue
		}
		if strings.Contains(answer, "#citation:"+citation.Citation.ID) {
			continue
		}
		blockIndex := citationLabelBlockIndex(citation.Citation.Text, labelsByBlock)
		if blockIndex < 0 {
			for candidateIndex, block := range blocks {
				trimmed := strings.TrimSpace(stripEvidenceLabels(block))
				if trimmed == "" || strings.HasPrefix(trimmed, "```") {
					continue
				}
				if answerMentionsEvidence(strings.ToLower(trimmed), citation.Tokens) {
					blockIndex = candidateIndex
					break
				}
			}
		}
		if blockIndex < 0 {
			blockIndex = lastTextBlock
		}
		label := evidenceLabelForBlock(labelsByBlock[blockIndex], labelCursorByBlock[blockIndex])
		if label != "" {
			labelCursorByBlock[blockIndex]++
			citations[index].Citation.Text = label
		}
		markerText := firstNonEmptyString(label, citation.Citation.Text, fmt.Sprintf("E%d", index+1))
		marker := fmt.Sprintf("[%s](#citation:%s)", markdownLinkText(markerText), citation.Citation.ID)
		markersByBlock[blockIndex] = append(markersByBlock[blockIndex], marker)
	}
	for index := range blocks {
		blocks[index] = stripEvidenceLabels(blocks[index])
		if markers := markersByBlock[index]; len(markers) > 0 {
			blocks[index] = appendMarkdownMarkers(blocks[index], markers)
		}
	}
	return strings.TrimSpace(strings.Join(blocks, ""))
}

func citationLabelBlockIndex(text string, labelsByBlock map[int][]string) int {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return -1
	}
	for index, labels := range labelsByBlock {
		for _, label := range labels {
			if strings.ToLower(label) == text {
				return index
			}
		}
	}
	return -1
}

func markdownBlocks(answer string) []string {
	matches := regexp.MustCompile(`(?s)(.*?(?:\n\s*\n|$))`).FindAllString(answer, -1)
	blocks := make([]string, 0, len(matches))
	for _, match := range matches {
		if match != "" {
			blocks = append(blocks, match)
		}
	}
	return blocks
}

func appendMarkdownMarkers(block string, markers []string) string {
	trimmed := strings.TrimRight(block, " \t\r\n")
	if trimmed == "" || len(markers) == 0 {
		return block
	}
	suffix := block[len(trimmed):]
	return trimmed + " " + strings.Join(markers, " ") + suffix
}

func evidenceLabelsInBlock(block string) []string {
	matches := evidenceLabelPattern.FindAllStringSubmatch(block, -1)
	labels := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		if label := sanitizeEvidenceLabel(match[1]); label != "" {
			labels = append(labels, label)
		}
	}
	return labels
}

func stripEvidenceLabels(block string) string {
	return evidenceLabelPattern.ReplaceAllString(block, "")
}

func evidenceLabelForBlock(labels []string, index int) string {
	if len(labels) == 0 {
		return ""
	}
	if index >= 0 && index < len(labels) {
		return labels[index]
	}
	return labels[len(labels)-1]
}

func sanitizeEvidenceLabel(label string) string {
	label = strings.Join(strings.Fields(strings.TrimSpace(label)), " ")
	label = strings.Trim(label, "`*_[](){}<>#")
	if label == "" {
		return ""
	}
	if len([]rune(label)) > 36 {
		runes := []rune(label)
		label = strings.TrimSpace(string(runes[:36]))
	}
	return label
}

func markdownLinkText(text string) string {
	text = strings.ReplaceAll(text, "[", "(")
	text = strings.ReplaceAll(text, "]", ")")
	text = strings.ReplaceAll(text, "\n", " ")
	return strings.TrimSpace(text)
}

type evidenceCitationMetadata struct {
	Text   string
	Target json.RawMessage
}

func citationMetadataFromArtifact(artifact Artifact) evidenceCitationMetadata {
	var record map[string]any
	if len(artifact.Data) == 0 || json.Unmarshal(artifact.Data, &record) != nil {
		return evidenceCitationMetadata{}
	}
	meta := valueRecord(record["citation"])
	if meta == nil {
		return evidenceCitationMetadata{}
	}
	out := evidenceCitationMetadata{Text: textField(meta, "text")}
	if target, ok := meta["target"]; ok {
		out.Target = jsonRaw(target)
	}
	return out
}

func evidenceArtifactDataWithCitation(data json.RawMessage, text string, target json.RawMessage) json.RawMessage {
	var record map[string]any
	if len(data) == 0 || json.Unmarshal(data, &record) != nil || record == nil {
		record = map[string]any{"value": rawMessageValue(data)}
	}
	citation := map[string]any{"text": text}
	if len(target) > 0 {
		citation["target"] = rawMessageValue(target)
	}
	record["citation"] = citation
	return jsonRaw(record)
}

func evidenceCitationTokens(artifact Artifact) []string {
	seen := map[string]bool{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if !usefulCitationToken(value) {
			return
		}
		key := strings.ToLower(value)
		if seen[key] {
			return
		}
		seen[key] = true
	}
	add(artifact.Title)
	var value any
	if len(artifact.Data) > 0 && json.Unmarshal(artifact.Data, &value) == nil {
		collectEvidenceTokens(value, "", add)
	}
	tokens := make([]string, 0, len(seen))
	for token := range seen {
		tokens = append(tokens, token)
	}
	return tokens
}

func collectEvidenceTokens(value any, key string, add func(string)) {
	switch typed := value.(type) {
	case map[string]any:
		addIdentityTokens(typed, add)
		for childKey, childValue := range typed {
			collectEvidenceTokens(childValue, childKey, add)
		}
	case []any:
		for _, item := range typed {
			collectEvidenceTokens(item, key, add)
		}
	case string:
		if importantCitationKey(key) {
			add(typed)
		}
		for _, token := range evidenceTokenPattern.FindAllString(typed, -1) {
			add(token)
		}
	case float64, bool, int, int64:
		if importantCitationKey(key) {
			add(fmt.Sprint(typed))
		}
	}
}

func addIdentityTokens(record map[string]any, add func(string)) {
	kind := textField(record, "kind")
	namespace := textField(record, "namespace")
	name := textField(record, "name")
	clusterID := textField(record, "clusterId")
	if kind != "" && name != "" {
		add(strings.Join(nonEmptyStrings(kind, namespace, name), "/"))
		add(strings.Join(nonEmptyStrings(clusterID, kind, namespace, name), "/"))
	}
	if namespace != "" && name != "" {
		add(namespace + "/" + name)
	}
}

func importantCitationKey(key string) bool {
	switch strings.ToLower(key) {
	case "id", "uid", "clusterid", "kind", "namespace", "name", "versionid", "changeid", "factkey", "factvalue", "rowid", "rowids", "label", "source", "target", "artifactid":
		return true
	default:
		return false
	}
}

func usefulCitationToken(token string) bool {
	token = strings.Trim(strings.TrimSpace(token), "`'\".,;:()[]{}")
	if len(token) < 3 {
		return false
	}
	lower := strings.ToLower(token)
	switch lower {
	case "the", "and", "for", "with", "true", "false", "null", "json", "text", "kind", "name", "type", "data", "status", "summary", "evidence":
		return false
	}
	if strings.ContainsAny(token, "/-_.:") {
		return true
	}
	for _, r := range token {
		if r >= 'A' && r <= 'Z' {
			return true
		}
	}
	return importantCitationKey(lower)
}

func answerMentionsEvidence(answer string, tokens []string) bool {
	for _, token := range tokens {
		token = strings.ToLower(strings.TrimSpace(token))
		if token != "" && strings.Contains(answer, token) {
			return true
		}
	}
	return false
}
