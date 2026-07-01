package api

import (
	"encoding/json"
	"strings"

	"kube-insight/internal/agent"
)

func a2aArtifactsFromEvents(events []agent.RunEvent) []a2aArtifact {
	citationsByArtifact := map[string][]agent.Citation{}
	artifactsByID := map[string]agent.Artifact{}
	order := []string{}
	for _, event := range events {
		switch event.Type {
		case agent.EventArtifact, agent.EventArtifactUpdate:
			var data agent.ArtifactEventData
			if err := json.Unmarshal(event.Data, &data); err != nil || data.Artifact.ID == "" {
				continue
			}
			if _, exists := artifactsByID[data.Artifact.ID]; !exists {
				order = append(order, data.Artifact.ID)
			}
			artifactsByID[data.Artifact.ID] = data.Artifact
		case agent.EventCitation:
			var data agent.CitationEventData
			if err := json.Unmarshal(event.Data, &data); err != nil || data.Citation.ArtifactID == "" {
				continue
			}
			citationsByArtifact[data.Citation.ArtifactID] = append(citationsByArtifact[data.Citation.ArtifactID], data.Citation)
		}
	}
	out := make([]a2aArtifact, 0, len(order))
	for _, id := range order {
		artifact := artifactsByID[id]
		out = append(out, a2aArtifact{
			ArtifactID:  artifact.ID,
			Name:        artifact.Title,
			Description: artifact.Kind,
			Parts:       a2aPartsForArtifact(artifact),
			Metadata:    a2aArtifactMetadata(artifact, citationsByArtifact[id]),
		})
	}
	return out
}

func a2aPartsForArtifact(artifact agent.Artifact) []a2aPart {
	if len(artifact.Data) == 0 {
		return []a2aPart{{Kind: "text", Text: artifact.Title, MediaType: "text/plain"}}
	}
	if markdown := markdownFromArtifactData(artifact.Data); markdown != "" {
		return []a2aPart{{Kind: "text", Text: markdown, MediaType: "text/markdown"}}
	}
	return []a2aPart{{Kind: "data", Type: "data", Data: artifact.Data, MediaType: "application/json"}}
}

func markdownFromArtifactData(raw json.RawMessage) string {
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return ""
	}
	for _, key := range []string{"markdown", "text", "summary"} {
		value, _ := data[key].(string)
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func a2aArtifactMetadata(artifact agent.Artifact, citations []agent.Citation) map[string]any {
	metadata := map[string]any{
		"kubeInsightArtifactKind": artifact.Kind,
	}
	if len(artifact.Data) > 0 {
		metadata["kubeInsightArtifactData"] = json.RawMessage(artifact.Data)
	}
	if len(citations) > 0 {
		metadata["citations"] = citations
	}
	return metadata
}
