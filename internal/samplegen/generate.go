package samplegen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Options struct {
	FixturesDir string
	OutputDir   string
	Clusters    int
	Copies      int
}

type Manifest struct {
	GeneratedAt time.Time      `json:"generatedAt"`
	FixturesDir string         `json:"fixturesDir"`
	OutputDir   string         `json:"outputDir"`
	Clusters    int            `json:"clusters"`
	Copies      int            `json:"copies"`
	Entries     []ClusterEntry `json:"entries"`
}

type ClusterEntry struct {
	Alias string `json:"alias"`
	File  string `json:"file"`
	Items int    `json:"items"`
}

func Generate(_ context.Context, opts Options) (Manifest, error) {
	if opts.FixturesDir == "" {
		opts.FixturesDir = filepath.Join("testdata", "fixtures", "kube")
	}
	if opts.OutputDir == "" {
		opts.OutputDir = filepath.Join("testdata", "generated", "kube-samples")
	}
	if opts.Clusters <= 0 {
		opts.Clusters = 2
	}
	if opts.Copies <= 0 {
		opts.Copies = 10
	}
	fixtures, err := loadFixtures(opts.FixturesDir)
	if err != nil {
		return Manifest{}, err
	}
	if len(fixtures) == 0 {
		return Manifest{}, fmt.Errorf("no fixture objects found in %s", opts.FixturesDir)
	}
	if err := os.MkdirAll(opts.OutputDir, 0o755); err != nil {
		return Manifest{}, err
	}

	manifest := Manifest{
		GeneratedAt: time.Now().UTC(),
		FixturesDir: opts.FixturesDir,
		OutputDir:   opts.OutputDir,
		Clusters:    opts.Clusters,
		Copies:      opts.Copies,
	}
	for c := 0; c < opts.Clusters; c++ {
		cluster := fmt.Sprintf("cluster-gen-%02d", c+1)
		clusterDir := filepath.Join(opts.OutputDir, cluster)
		if err := os.MkdirAll(clusterDir, 0o755); err != nil {
			return manifest, err
		}
		items := make([]any, 0, len(fixtures)*opts.Copies)
		for copyIndex := 0; copyIndex < opts.Copies; copyIndex++ {
			state := newMutationState(c, copyIndex, fixtures)
			for _, fixture := range fixtures {
				mutated := state.mutateObject(fixture)
				state.applyIncidentFinal(mutated)
				for _, variant := range state.historicalVariants(mutated) {
					items = append(items, variant)
				}
				items = append(items, mutated)
			}
		}
		doc := map[string]any{
			"apiVersion": "v1",
			"kind":       "List",
			"items":      items,
		}
		data, err := marshalJSON(doc)
		if err != nil {
			return manifest, err
		}
		filePath := filepath.Join(clusterDir, "generated.json")
		if err := os.WriteFile(filePath, data, 0o600); err != nil {
			return manifest, err
		}
		manifest.Entries = append(manifest.Entries, ClusterEntry{
			Alias: cluster,
			File:  filepath.ToSlash(filepath.Join(cluster, "generated.json")),
			Items: len(items),
		})
	}
	manifestData, err := marshalJSON(manifest)
	if err != nil {
		return manifest, err
	}
	if err := os.WriteFile(filepath.Join(opts.OutputDir, "manifest.json"), manifestData, 0o600); err != nil {
		return manifest, err
	}
	return manifest, nil
}

func loadFixtures(dir string) ([]map[string]any, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	var objects []map[string]any
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		decoded, err := decodeObjects(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		objects = append(objects, decoded...)
	}
	return objects, nil
}

func decodeObjects(data []byte) ([]map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var root map[string]any
	if err := decoder.Decode(&root); err != nil {
		return nil, err
	}
	if items, ok := root["items"].([]any); ok {
		out := make([]map[string]any, 0, len(items))
		for i, raw := range items {
			obj, ok := raw.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("items[%d] is not an object", i)
			}
			out = append(out, obj)
		}
		return out, nil
	}
	return []map[string]any{root}, nil
}

type mutationState struct {
	clusterIndex int
	copyIndex    int
	namespace    string
	namesByKey   map[string]string
	uidsByUID    map[string]string
}

func newMutationState(clusterIndex, copyIndex int, fixtures []map[string]any) *mutationState {
	s := &mutationState{
		clusterIndex: clusterIndex,
		copyIndex:    copyIndex,
		namespace:    fmt.Sprintf("ns-%02d-%04d", clusterIndex+1, copyIndex+1),
		namesByKey:   map[string]string{},
		uidsByUID:    map[string]string{},
	}
	for _, obj := range fixtures {
		kind, _ := obj["kind"].(string)
		metadata, _ := obj["metadata"].(map[string]any)
		name, _ := metadata["name"].(string)
		namespace, _ := metadata["namespace"].(string)
		uid, _ := metadata["uid"].(string)
		if name != "" {
			s.namesByKey[objectKey(kind, namespace, name)] = generatedName(kind, name, copyIndex)
			s.namesByKey[objectKey(kind, "", name)] = generatedName(kind, name, copyIndex)
		}
		if uid != "" {
			s.uidsByUID[uid] = generatedUID(kind, uid, clusterIndex, copyIndex)
		}
	}
	return s
}

func (s *mutationState) mutateObject(obj map[string]any) map[string]any {
	kind, _ := obj["kind"].(string)
	return s.mutateMap(obj, nil, kind)
}

func (s *mutationState) applyIncidentFinal(obj map[string]any) {
	kind, _ := obj["kind"].(string)
	switch kind {
	case "Pod":
		obj["status"] = map[string]any{
			"phase":      "Running",
			"conditions": []any{map[string]any{"type": "Ready", "status": "False"}},
			"containerStatuses": []any{map[string]any{
				"name":         "app",
				"restartCount": float64(2 + s.copyIndex%4),
				"lastState":    map[string]any{"terminated": map[string]any{"reason": "OOMKilled"}},
				"state":        map[string]any{"waiting": map[string]any{"reason": "CrashLoopBackOff"}},
			}},
		}
	case "EndpointSlice":
		setEndpointConditions(obj, false)
	case "Node":
		setNodeCondition(obj, "Ready", "False")
		setNodeCondition(obj, "MemoryPressure", "True")
	case "Event":
		obj["count"] = float64(3 + s.copyIndex%5)
	}
}

func (s *mutationState) historicalVariants(obj map[string]any) []map[string]any {
	kind, _ := obj["kind"].(string)
	switch kind {
	case "Pod":
		before := cloneMap(obj)
		setResourceVersionOffset(before, -1000)
		before["status"] = map[string]any{
			"phase":      "Running",
			"conditions": []any{map[string]any{"type": "Ready", "status": "True"}},
			"containerStatuses": []any{map[string]any{
				"name":         "app",
				"restartCount": float64(0),
				"state":        map[string]any{"running": map[string]any{"startedAt": "2026-05-14T09:55:00Z"}},
			}},
		}
		return []map[string]any{before}
	case "EndpointSlice":
		before := cloneMap(obj)
		setResourceVersionOffset(before, -1000)
		setEndpointConditions(before, true)
		return []map[string]any{before}
	case "Node":
		before := cloneMap(obj)
		setResourceVersionOffset(before, -1000)
		setNodeCondition(before, "Ready", "True")
		setNodeCondition(before, "MemoryPressure", "False")
		return []map[string]any{before}
	case "Event":
		before := cloneMap(obj)
		setResourceVersionOffset(before, -1000)
		before["count"] = float64(1)
		return []map[string]any{before}
	default:
		return nil
	}
}

func (s *mutationState) mutateValue(value any, path []string, kind string) any {
	switch typed := value.(type) {
	case map[string]any:
		return s.mutateMap(typed, path, kind)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = s.mutateValue(item, append(path, "[]"), kind)
		}
		return out
	default:
		return value
	}
}

func (s *mutationState) mutateMap(in map[string]any, path []string, inheritedKind string) map[string]any {
	kind, _ := in["kind"].(string)
	if kind == "" {
		kind = inheritedKind
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		switch key {
		case "metadata":
			if metadata, ok := value.(map[string]any); ok {
				out[key] = s.mutateMetadata(metadata, kind)
				continue
			}
		case "involvedObject", "regarding", "related", "scaleTargetRef", "targetRef":
			if ref, ok := value.(map[string]any); ok {
				out[key] = s.mutateObjectRef(ref)
				continue
			}
		case "issuerRef":
			if ref, ok := value.(map[string]any); ok {
				out[key] = s.mutateIssuerRef(ref)
				continue
			}
		case "roleRef":
			if ref, ok := value.(map[string]any); ok {
				out[key] = s.mutateRoleRef(ref)
				continue
			}
		case "certificateRef":
			if ref, ok := value.(map[string]any); ok {
				out[key] = s.mutateNamedRef(ref, "Certificate", s.namespace)
				continue
			}
		case "privateKeySecretRef":
			if ref, ok := value.(map[string]any); ok {
				out[key] = s.mutateNamedRef(ref, "Secret", s.namespace)
				continue
			}
		case "requestRef":
			if ref, ok := value.(map[string]any); ok {
				out[key] = s.mutateNamedRef(ref, "CertificateRequest", s.namespace)
				continue
			}
		case "orderRef":
			if ref, ok := value.(map[string]any); ok {
				out[key] = s.mutateNamedRef(ref, "Order", s.namespace)
				continue
			}
		case "service":
			if ref, ok := value.(map[string]any); ok && refHasName(ref) {
				out[key] = s.mutateNamedRef(ref, "Service", s.namespace)
				continue
			}
		case "secret", "secretRef", "secretKeyRef":
			if ref, ok := value.(map[string]any); ok && refHasAnyName(ref) {
				out[key] = s.mutateNamedRef(ref, "Secret", s.namespace)
				continue
			}
		case "configMap", "configMapRef", "configMapKeyRef":
			if ref, ok := value.(map[string]any); ok && refHasName(ref) {
				out[key] = s.mutateNamedRef(ref, "ConfigMap", s.namespace)
				continue
			}
		case "persistentVolumeClaim":
			if ref, ok := value.(map[string]any); ok && refHasClaimName(ref) {
				out[key] = s.mutateNamedRef(ref, "PersistentVolumeClaim", s.namespace)
				continue
			}
		case "claimRef":
			if ref, ok := value.(map[string]any); ok {
				out[key] = s.mutateNamedRef(ref, "PersistentVolumeClaim", s.namespace)
				continue
			}
		case "imagePullSecrets":
			out[key] = s.mutateNamedRefList(value, "Secret", s.namespace)
			continue
		case "secrets":
			if kind == "ServiceAccount" {
				out[key] = s.mutateNamedRefList(value, "Secret", s.namespace)
				continue
			}
		case "ownerReferences":
			out[key] = s.mutateOwnerRefs(value)
			continue
		case "parentRefs":
			out[key] = s.mutateGatewayRefList(value, "Gateway", s.namespace)
			continue
		case "backendRefs":
			out[key] = s.mutateGatewayRefList(value, "Service", s.namespace)
			continue
		case "subjects":
			out[key] = s.mutateSubjects(value)
			continue
		case "nodeName":
			if name, ok := value.(string); ok {
				out[key] = s.mappedName("Node", "", name)
				continue
			}
		case "serviceName":
			if name, ok := value.(string); ok {
				out[key] = s.mappedName("Service", s.namespace, name)
				continue
			}
		case "secretName":
			if name, ok := value.(string); ok {
				out[key] = s.mappedName("Secret", s.namespace, name)
				continue
			}
		case "serviceAccountName":
			if name, ok := value.(string); ok {
				out[key] = s.mappedName("ServiceAccount", s.namespace, name)
				continue
			}
		case "storageClassName":
			if name, ok := value.(string); ok {
				out[key] = s.mappedName("StorageClass", "", name)
				continue
			}
		case "volumeName":
			if name, ok := value.(string); ok {
				out[key] = s.mappedName("PersistentVolume", "", name)
				continue
			}
		case "claimName":
			if name, ok := value.(string); ok {
				out[key] = s.mappedName("PersistentVolumeClaim", s.namespace, name)
				continue
			}
		case "gatewayClassName":
			if name, ok := value.(string); ok {
				out[key] = s.mappedName("GatewayClass", "", name)
				continue
			}
		}
		out[key] = s.mutateValue(value, append(path, key), kind)
	}
	return out
}

func (s *mutationState) mutateMetadata(metadata map[string]any, kind string) map[string]any {
	out := make(map[string]any, len(metadata))
	originalNamespace, _ := metadata["namespace"].(string)
	for key, value := range metadata {
		switch key {
		case "name":
			if name, ok := value.(string); ok {
				out[key] = s.mappedName(kind, originalNamespace, name)
				continue
			}
		case "namespace":
			out[key] = s.namespace
			continue
		case "uid":
			if uid, ok := value.(string); ok {
				out[key] = s.mappedUID(uid)
				continue
			}
		case "resourceVersion":
			out[key] = strconv.Itoa((s.clusterIndex+1)*1000000 + s.copyIndex + 1)
			continue
		case "labels":
			out[key] = s.mutateLabelMap(value)
			continue
		}
		out[key] = s.mutateValue(value, []string{"metadata", key}, kind)
	}
	return out
}

func (s *mutationState) mutateObjectRef(ref map[string]any) map[string]any {
	out := make(map[string]any, len(ref))
	kind, _ := ref["kind"].(string)
	namespace, _ := ref["namespace"].(string)
	for key, value := range ref {
		switch key {
		case "name":
			if name, ok := value.(string); ok {
				out[key] = s.mappedName(kind, namespace, name)
				continue
			}
		case "namespace":
			out[key] = s.namespace
			continue
		case "uid":
			if uid, ok := value.(string); ok {
				out[key] = s.mappedUID(uid)
				continue
			}
		}
		out[key] = value
	}
	return out
}

func (s *mutationState) mutateIssuerRef(ref map[string]any) map[string]any {
	kind, _ := ref["kind"].(string)
	if kind == "" {
		kind = "Issuer"
	}
	namespace := s.namespace
	if kind == "ClusterIssuer" {
		namespace = ""
	}
	return s.mutateNamedRef(ref, kind, namespace)
}

func (s *mutationState) mutateRoleRef(ref map[string]any) map[string]any {
	kind, _ := ref["kind"].(string)
	if kind == "" {
		kind = "Role"
	}
	namespace := s.namespace
	if kind == "ClusterRole" {
		namespace = ""
	}
	return s.mutateNamedRef(ref, kind, namespace)
}

func (s *mutationState) mutateNamedRef(ref map[string]any, kind, namespace string) map[string]any {
	out := make(map[string]any, len(ref))
	for key, value := range ref {
		switch key {
		case "claimName", "name", "secretName":
			if name, ok := value.(string); ok {
				out[key] = s.mappedName(kind, namespace, name)
				continue
			}
		case "namespace":
			if namespace != "" {
				out[key] = namespace
				continue
			}
		case "uid":
			if uid, ok := value.(string); ok {
				out[key] = s.mappedUID(uid)
				continue
			}
		}
		out[key] = s.mutateValue(value, []string{key}, kind)
	}
	return out
}

func (s *mutationState) mutateNamedRefList(value any, kind, namespace string) any {
	items, ok := value.([]any)
	if !ok {
		return value
	}
	out := make([]any, len(items))
	for i, item := range items {
		if ref, ok := item.(map[string]any); ok {
			out[i] = s.mutateNamedRef(ref, kind, namespace)
		} else {
			out[i] = item
		}
	}
	return out
}

func (s *mutationState) mutateGatewayRefList(value any, defaultKind, defaultNamespace string) any {
	items, ok := value.([]any)
	if !ok {
		return value
	}
	out := make([]any, len(items))
	for i, item := range items {
		ref, ok := item.(map[string]any)
		if !ok {
			out[i] = item
			continue
		}
		kind, _ := ref["kind"].(string)
		if kind == "" {
			kind = defaultKind
		}
		namespace := defaultNamespace
		if kind == "GatewayClass" {
			namespace = ""
		}
		out[i] = s.mutateNamedRef(ref, kind, namespace)
	}
	return out
}

func (s *mutationState) mutateOwnerRefs(value any) any {
	items, ok := value.([]any)
	if !ok {
		return value
	}
	out := make([]any, len(items))
	for i, item := range items {
		if ref, ok := item.(map[string]any); ok {
			out[i] = s.mutateObjectRef(ref)
		} else {
			out[i] = item
		}
	}
	return out
}

func (s *mutationState) mutateSubjects(value any) any {
	items, ok := value.([]any)
	if !ok {
		return value
	}
	out := make([]any, len(items))
	for i, item := range items {
		subject, ok := item.(map[string]any)
		if !ok {
			out[i] = item
			continue
		}
		kind, _ := subject["kind"].(string)
		namespace := s.namespace
		if kind == "User" || kind == "Group" {
			namespace = ""
		}
		out[i] = s.mutateNamedRef(subject, kind, namespace)
	}
	return out
}

func refHasName(ref map[string]any) bool {
	_, ok := ref["name"].(string)
	return ok
}

func refHasAnyName(ref map[string]any) bool {
	if refHasName(ref) {
		return true
	}
	_, ok := ref["secretName"].(string)
	return ok
}

func refHasClaimName(ref map[string]any) bool {
	_, ok := ref["claimName"].(string)
	return ok
}

func setEndpointConditions(obj map[string]any, ready bool) {
	endpoints, ok := obj["endpoints"].([]any)
	if !ok {
		return
	}
	for _, item := range endpoints {
		endpoint, ok := item.(map[string]any)
		if !ok {
			continue
		}
		conditions, _ := endpoint["conditions"].(map[string]any)
		if conditions == nil {
			conditions = map[string]any{}
			endpoint["conditions"] = conditions
		}
		conditions["ready"] = ready
		conditions["serving"] = !ready
		conditions["terminating"] = false
	}
}

func setNodeCondition(obj map[string]any, conditionType, status string) {
	statusMap, _ := obj["status"].(map[string]any)
	if statusMap == nil {
		statusMap = map[string]any{}
		obj["status"] = statusMap
	}
	conditions, _ := statusMap["conditions"].([]any)
	for _, item := range conditions {
		condition, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if got, _ := condition["type"].(string); got == conditionType {
			condition["status"] = status
			return
		}
	}
	statusMap["conditions"] = append(conditions, map[string]any{"type": conditionType, "status": status})
}

func setResourceVersionOffset(obj map[string]any, delta int) {
	metadata, _ := obj["metadata"].(map[string]any)
	if metadata == nil {
		return
	}
	current, _ := metadata["resourceVersion"].(string)
	n, err := strconv.Atoi(current)
	if err != nil {
		metadata["resourceVersion"] = current + "-previous"
		return
	}
	if n+delta < 1 {
		metadata["resourceVersion"] = "1"
		return
	}
	metadata["resourceVersion"] = strconv.Itoa(n + delta)
}

func cloneMap(in map[string]any) map[string]any {
	out, _ := cloneValue(in).(map[string]any)
	if out == nil {
		return map[string]any{}
	}
	return out
}

func cloneValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, value := range typed {
			out[key] = cloneValue(value)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, value := range typed {
			out[i] = cloneValue(value)
		}
		return out
	default:
		return typed
	}
}

func (s *mutationState) mutateLabelMap(value any) any {
	m, ok := value.(map[string]any)
	if !ok {
		return value
	}
	out := make(map[string]any, len(m))
	for key, value := range m {
		if text, ok := value.(string); ok {
			out[key] = fmt.Sprintf("%s-%04d", text, s.copyIndex+1)
		} else {
			out[key] = value
		}
	}
	return out
}

func (s *mutationState) mappedName(kind, namespace, name string) string {
	if namespace == "" {
		namespace = s.namespace
	}
	if got, ok := s.namesByKey[objectKey(kind, namespace, name)]; ok {
		return got
	}
	if got, ok := s.namesByKey[objectKey(kind, "", name)]; ok {
		return got
	}
	return generatedName(kind, name, s.copyIndex)
}

func (s *mutationState) mappedUID(uid string) string {
	if got, ok := s.uidsByUID[uid]; ok {
		return got
	}
	return fmt.Sprintf("uid-%02d-%04d-%s", s.clusterIndex+1, s.copyIndex+1, sanitizeName(uid))
}

func objectKey(kind, namespace, name string) string {
	return kind + "\x00" + namespace + "\x00" + name
}

func generatedName(kind, name string, index int) string {
	if kind == "CustomResourceDefinition" {
		return name
	}
	return fmt.Sprintf("%s-%04d", sanitizeName(name), index+1)
}

func generatedUID(kind, uid string, clusterIndex, copyIndex int) string {
	if kind == "CustomResourceDefinition" {
		return fmt.Sprintf("uid-%02d-%s", clusterIndex+1, sanitizeName(uid))
	}
	return fmt.Sprintf("uid-%02d-%04d-%s", clusterIndex+1, copyIndex+1, sanitizeName(uid))
}

func sanitizeName(value string) string {
	value = strings.ToLower(value)
	value = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '.' {
			return r
		}
		return '-'
	}, value)
	value = strings.Trim(value, "-.")
	if value == "" {
		return "object"
	}
	return value
}

func marshalJSON(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
