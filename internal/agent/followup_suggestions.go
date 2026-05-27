package agent

import (
	"regexp"
	"strings"
)

const maxFollowUpSuggestions = 4

var followUpLabelPattern = regexp.MustCompile(`\{\{followup:\s*([^{}\n]{1,180})\s*\}\}`)

func extractFollowUpSuggestions(answer string) (string, []string) {
	suggestions := followUpSuggestionsInAnswer(answer)
	clean := stripFollowUpLabels(answer)
	return clean, suggestions
}

func followUpSuggestionsInAnswer(answer string) []string {
	seen := map[string]bool{}
	var out []string
	for _, match := range followUpLabelPattern.FindAllStringSubmatch(answer, -1) {
		if len(match) < 2 {
			continue
		}
		suggestion := sanitizeFollowUpSuggestion(match[1])
		key := strings.ToLower(suggestion)
		if suggestion == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, suggestion)
		if len(out) >= maxFollowUpSuggestions {
			break
		}
	}
	return out
}

func stripFollowUpLabels(answer string) string {
	return strings.TrimSpace(followUpLabelPattern.ReplaceAllString(answer, ""))
}

func sanitizeFollowUpSuggestion(value string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	value = strings.Trim(value, "`*_[]{}<>#")
	if value == "" {
		return ""
	}
	runes := []rune(value)
	if len(runes) > 120 {
		value = strings.TrimSpace(string(runes[:120]))
	}
	return value
}
