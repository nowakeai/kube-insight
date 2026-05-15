package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

func overlayDefaultYAML(data []byte) ([]byte, error) {
	var base yaml.Node
	if err := yaml.Unmarshal(defaultConfigYAML, &base); err != nil {
		return nil, fmt.Errorf("parse embedded default config: %w", err)
	}
	var overlay yaml.Node
	if err := yaml.Unmarshal(data, &overlay); err != nil {
		return nil, err
	}
	overlayContent := documentContent(&overlay)
	if isEmptyYAMLNode(overlayContent) {
		out, err := yaml.Marshal(&base)
		if err != nil {
			return nil, err
		}
		return out, nil
	}
	mergeYAMLNode(documentContent(&base), overlayContent)
	out, err := yaml.Marshal(&base)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func documentContent(node *yaml.Node) *yaml.Node {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		return node.Content[0]
	}
	return node
}

func isEmptyYAMLNode(node *yaml.Node) bool {
	if node == nil {
		return true
	}
	return node.Kind == 0 || node.Kind == yaml.ScalarNode && node.Tag == "!!null"
}

func mergeYAMLNode(base, overlay *yaml.Node) {
	if base == nil || overlay == nil {
		return
	}
	if base.Kind == yaml.MappingNode && overlay.Kind == yaml.MappingNode {
		mergeMappingNode(base, overlay)
		return
	}
	*base = *cloneYAMLNode(overlay)
}

func mergeMappingNode(base, overlay *yaml.Node) {
	for i := 0; i+1 < len(overlay.Content); i += 2 {
		key := overlay.Content[i]
		value := overlay.Content[i+1]
		baseValue := mappingValue(base, key.Value)
		if baseValue == nil {
			base.Content = append(base.Content, cloneYAMLNode(key), cloneYAMLNode(value))
			continue
		}
		mergeYAMLNode(baseValue, value)
	}
}

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func cloneYAMLNode(node *yaml.Node) *yaml.Node {
	if node == nil {
		return nil
	}
	out := *node
	if len(node.Content) > 0 {
		out.Content = make([]*yaml.Node, len(node.Content))
		for i, child := range node.Content {
			out.Content[i] = cloneYAMLNode(child)
		}
	}
	return &out
}
