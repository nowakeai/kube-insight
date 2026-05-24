package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

const maxVerifiedAnswerCitations = 8

var evidenceTokenPattern = regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9._:/-]{2,}`)

func (r einoRunRecorder) appendVerifiedAnswerCitations(ctx context.Context, finalAnswer string) error {
	finalAnswer = strings.TrimSpace(finalAnswer)
	if !r.enabled() || finalAnswer == "" {
		return nil
	}
	events, err := r.store.ListRunEvents(ctx, r.runID)
	if err != nil {
		return err
	}
	answer := strings.ToLower(finalAnswer)
	seenArtifacts := map[string]bool{}
	emitted := 0
	for _, event := range events {
		if event.Type != EventArtifact {
			continue
		}
		var data ArtifactEventData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			continue
		}
		artifact := data.Artifact
		if artifact.ID == "" || artifact.Kind == ArtifactKindToolCall || seenArtifacts[artifact.ID] {
			continue
		}
		meta := citationMetadataFromArtifact(artifact)
		tokens := evidenceCitationTokens(artifact)
		if !answerMentionsEvidence(answer, tokens) {
			continue
		}
		seenArtifacts[artifact.ID] = true
		citation := Citation{
			ID:         NewCitationID(),
			ArtifactID: artifact.ID,
			Text:       firstNonEmptyString(meta.Text, artifact.Title, artifact.Kind),
			Target:     meta.Target,
		}
		if len(citation.Target) == 0 {
			citation.Target = jsonRaw(map[string]any{"type": CitationTargetArtifact, "artifactId": artifact.ID})
		}
		if err := r.append(ctx, EventCitation, CitationEventData{Citation: citation}); err != nil {
			return err
		}
		emitted++
		if emitted >= maxVerifiedAnswerCitations {
			break
		}
	}
	return nil
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
