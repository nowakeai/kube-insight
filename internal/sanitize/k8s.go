package sanitize

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strings"
)

type KubernetesOptions struct {
	Salt string
}

type KubernetesSanitizer struct {
	salt string
}

func NewKubernetesSanitizer(opts KubernetesOptions) *KubernetesSanitizer {
	salt := opts.Salt
	if salt == "" {
		salt = "kube-insight-samples"
	}
	return &KubernetesSanitizer{salt: salt}
}

func KubernetesJSON(data []byte, opts KubernetesOptions) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()

	var root any
	if err := decoder.Decode(&root); err != nil {
		return nil, err
	}
	sanitized := NewKubernetesSanitizer(opts).Value(root)
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(sanitized); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func (s *KubernetesSanitizer) Value(value any) any {
	return s.sanitizeValue(value, nil, "")
}

func (s *KubernetesSanitizer) sanitizeValue(value any, path []string, kind string) any {
	switch typed := value.(type) {
	case map[string]any:
		return s.sanitizeMap(typed, path, kind)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = s.sanitizeValue(item, append(path, "[]"), kind)
		}
		return out
	case string:
		return s.sanitizeStringForKey(last(path), typed, path, kind)
	default:
		return value
	}
}

func (s *KubernetesSanitizer) sanitizeMap(in map[string]any, path []string, inheritedKind string) map[string]any {
	kind, _ := in["kind"].(string)
	if kind == "" {
		kind = inheritedKind
	}
	if kind == "List" {
		kind = ""
	}

	if isObjectRef(path) {
		return s.sanitizeObjectRef(in)
	}

	out := make(map[string]any, len(in))
	keys := sortedKeys(in)
	for _, key := range keys {
		value := in[key]
		nextPath := append(path, key)
		lower := strings.ToLower(key)

		if isMetadataManagedFields(path, key) || isLastAppliedAnnotation(path, key) {
			continue
		}
		if isPayloadMapKey(lower) {
			out[key] = s.payloadMap(value, kind, key)
			continue
		}
		if key == "metadata" {
			if metadata, ok := value.(map[string]any); ok {
				out[key] = s.sanitizeMetadata(metadata, kind)
				continue
			}
		}
		if key == "labels" || key == "matchLabels" || key == "annotations" {
			out[key] = s.sanitizeStringMap(value, strings.TrimPrefix(key, "match"))
			continue
		}
		if key == "selector" {
			out[key] = s.sanitizeSelector(value, nextPath, kind)
			continue
		}
		if isSensitiveKey(lower) {
			out[key] = "<redacted>"
			continue
		}
		if key == "message" || key == "note" {
			if text, ok := value.(string); ok {
				out[key] = s.alias("message", text)
				continue
			}
		}
		out[key] = s.sanitizeValue(value, nextPath, kind)
	}
	return out
}

func (s *KubernetesSanitizer) sanitizeMetadata(metadata map[string]any, kind string) map[string]any {
	out := make(map[string]any, len(metadata))
	for _, key := range sortedKeys(metadata) {
		value := metadata[key]
		switch key {
		case "managedFields":
			continue
		case "name":
			if name, ok := value.(string); ok {
				if kind == "CustomResourceDefinition" {
					out[key] = name
					continue
				}
				out[key] = s.alias(kindPrefix(kind), name)
				continue
			}
		case "generateName":
			if name, ok := value.(string); ok {
				out[key] = s.alias(kindPrefix(kind)+"-prefix", name)
				continue
			}
		case "namespace":
			if namespace, ok := value.(string); ok {
				out[key] = s.alias("ns", namespace)
				continue
			}
		case "uid":
			if uid, ok := value.(string); ok {
				out[key] = s.alias("uid", uid)
				continue
			}
		case "labels":
			out[key] = s.sanitizeStringMap(value, "label")
			continue
		case "annotations":
			out[key] = s.sanitizeStringMap(value, "annotation")
			continue
		case "ownerReferences":
			out[key] = s.sanitizeOwnerReferences(value)
			continue
		}
		out[key] = s.sanitizeValue(value, []string{"metadata", key}, kind)
	}
	return out
}

func (s *KubernetesSanitizer) sanitizeOwnerReferences(value any) any {
	items, ok := value.([]any)
	if !ok {
		return s.sanitizeValue(value, []string{"metadata", "ownerReferences"}, "")
	}
	out := make([]any, len(items))
	for i, item := range items {
		ref, ok := item.(map[string]any)
		if !ok {
			out[i] = item
			continue
		}
		out[i] = s.sanitizeObjectRef(ref)
	}
	return out
}

func (s *KubernetesSanitizer) sanitizeObjectRef(in map[string]any) map[string]any {
	kind, _ := in["kind"].(string)
	out := make(map[string]any, len(in))
	for _, key := range sortedKeys(in) {
		value := in[key]
		switch key {
		case "name":
			if name, ok := value.(string); ok {
				out[key] = s.alias(kindPrefix(kind), name)
				continue
			}
		case "namespace":
			if namespace, ok := value.(string); ok {
				out[key] = s.alias("ns", namespace)
				continue
			}
		case "uid":
			if uid, ok := value.(string); ok {
				out[key] = s.alias("uid", uid)
				continue
			}
		}
		out[key] = s.sanitizeValue(value, []string{key}, kind)
	}
	return out
}

func (s *KubernetesSanitizer) sanitizeStringMap(value any, prefix string) any {
	m, ok := value.(map[string]any)
	if !ok {
		return s.sanitizeValue(value, []string{prefix}, "")
	}
	out := make(map[string]any, len(m))
	for _, key := range sortedKeys(m) {
		if strings.EqualFold(key, "kubectl.kubernetes.io/last-applied-configuration") {
			continue
		}
		safeKey := key
		if !isKnownKubernetesKey(key) {
			safeKey = prefix + "." + s.shortHash(key)
		}
		if isSensitiveKey(strings.ToLower(key)) {
			out[safeKey] = "<redacted>"
			continue
		}
		if text, ok := m[key].(string); ok {
			out[safeKey] = s.alias(prefix+"-value", text)
			continue
		}
		out[safeKey] = s.sanitizeValue(m[key], []string{prefix, key}, "")
	}
	return out
}

func (s *KubernetesSanitizer) sanitizeSelector(value any, path []string, kind string) any {
	m, ok := value.(map[string]any)
	if !ok {
		return s.sanitizeValue(value, path, kind)
	}
	for _, raw := range m {
		if _, ok := raw.(string); !ok {
			return s.sanitizeValue(value, path, kind)
		}
	}
	return s.sanitizeStringMap(value, "selector")
}

func (s *KubernetesSanitizer) payloadMap(value any, kind, field string) any {
	m, ok := value.(map[string]any)
	if !ok {
		return "<redacted>"
	}
	out := make(map[string]any, len(m))
	for _, key := range sortedKeys(m) {
		if kind == "Secret" {
			out[key] = s.fakeSecretValue(field, key, m[key])
			continue
		}
		out[s.sanitizePayloadKey(key)] = "<redacted>"
	}
	return out
}

func (s *KubernetesSanitizer) sanitizePayloadKey(key string) string {
	if looksLikeSensitiveString(key) || privateIPPattern.MatchString(key) {
		return "key-" + s.shortHash(key)
	}
	return key
}

func (s *KubernetesSanitizer) fakeSecretValue(field, key string, original any) string {
	value := "fake-secret-" + s.shortHash(field+"\x00"+key+"\x00"+fmt.Sprint(original))
	if field == "data" || field == "binaryData" {
		return base64.StdEncoding.EncodeToString([]byte(value))
	}
	return value
}

func (s *KubernetesSanitizer) sanitizeStringForKey(key, value string, path []string, kind string) string {
	if kind == "CustomResourceDefinition" {
		return s.sanitizeCRDStringForKey(key, value)
	}
	switch key {
	case "name":
		return s.alias("name", value)
	case "namespace":
		return s.alias("ns", value)
	case "uid":
		return s.alias("uid", value)
	case "nodeName":
		return s.alias("node", value)
	case "serviceName":
		return s.alias("service", value)
	case "secretName":
		return s.alias("secret", value)
	case "configMapName":
		return s.alias("configmap", value)
	case "claimName":
		return s.alias("pvc", value)
	case "serviceAccountName":
		return s.alias("serviceaccount", value)
	case "serviceAccount":
		return s.alias("serviceaccount", value)
	case "image":
		return s.sanitizeImage(value)
	case "host", "hostname":
		return s.sanitizeHost(value)
	case "description":
		return s.sanitizeEmbeddedIdentifiers(value)
	case "server", "url", "endpoint":
		return s.alias(key, value)
	}
	if pathContains(path, "args") || pathContains(path, "command") {
		return s.sanitizeArgument(value)
	}
	if pathContains(path, "hosts") || pathContains(path, "hostnames") {
		return s.sanitizeHost(value)
	}
	if pathContains(path, "resourceNames") {
		return s.alias("resource", value)
	}
	if pathContains(path, "matchExpressions") && pathContains(path, "values") {
		return s.alias("selector-value", value)
	}
	lower := strings.ToLower(key)
	if strings.Contains(lower, "ip") {
		return s.sanitizeIP(value)
	}
	if key == "address" || key == "addresses" {
		return s.sanitizeAddress(value)
	}
	if isSensitiveKey(lower) {
		return "<redacted>"
	}
	if pathContains(path, "env") && key == "value" {
		return "<redacted>"
	}
	return s.sanitizeEmbeddedIdentifiers(value)
}

func (s *KubernetesSanitizer) sanitizeCRDStringForKey(key, value string) string {
	lower := strings.ToLower(key)
	if isSensitiveKey(lower) {
		return "<redacted>"
	}
	switch key {
	case "url", "server", "endpoint":
		return s.alias(key, value)
	case "host", "hostname":
		return s.sanitizeHost(value)
	case "address", "addresses":
		return s.sanitizeAddress(value)
	case "caBundle":
		return "<redacted>"
	default:
		return value
	}
}

func (s *KubernetesSanitizer) sanitizeImage(value string) string {
	if value == "" {
		return value
	}
	tag := "latest"
	name := value
	if i := strings.LastIndex(value, ":"); i > strings.LastIndex(value, "/") {
		tag = value[i+1:]
		name = value[:i]
	}
	return fmt.Sprintf("registry.example.invalid/image-%s:%s", s.shortHash(name), sanitizeImageTag(tag))
}

func sanitizeImageTag(tag string) string {
	tag = regexp.MustCompile(`[^A-Za-z0-9_.-]`).ReplaceAllString(tag, "-")
	if tag == "" {
		return "latest"
	}
	if !isSafeImageTag(tag) || looksLikeSensitiveString(tag) {
		sum := sha256.Sum256([]byte(tag))
		return "tag-" + hex.EncodeToString(sum[:])[:10]
	}
	if len(tag) > 32 {
		return tag[:32]
	}
	return tag
}

func isSafeImageTag(tag string) bool {
	return regexp.MustCompile(`^v?\d+([._-]\d+)*([._-](alpha|beta|rc)\d*)?$`).MatchString(tag) ||
		regexp.MustCompile(`^sha256-[a-f0-9]{8,32}$`).MatchString(tag)
}

func (s *KubernetesSanitizer) sanitizeIP(value string) string {
	if net.ParseIP(value) == nil {
		return value
	}
	hash := s.shortHash(value)
	return fmt.Sprintf("192.0.2.%d", int(hash[0])%250+1)
}

func (s *KubernetesSanitizer) sanitizeAddress(value string) string {
	if net.ParseIP(value) != nil {
		return s.sanitizeIP(value)
	}
	if privateIPPattern.MatchString(value) || strings.Contains(value, ".internal") || strings.Contains(value, "-") {
		return s.sanitizeHost(value)
	}
	return s.sanitizeEmbeddedIdentifiers(value)
}

func (s *KubernetesSanitizer) sanitizeHost(value string) string {
	if value == "" || value == "localhost" {
		return value
	}
	if net.ParseIP(value) != nil {
		return s.sanitizeIP(value)
	}
	return "host-" + s.shortHash(value) + ".example.invalid"
}

func (s *KubernetesSanitizer) sanitizeArgument(value string) string {
	if value == "" {
		return value
	}
	if strings.HasPrefix(value, "--") {
		if i := strings.IndexByte(value, '='); i > 2 {
			return value[:i+1] + s.alias("arg", value[i+1:])
		}
		return "--" + s.alias("flag", strings.TrimPrefix(value, "--"))
	}
	if strings.HasPrefix(value, "-") {
		return "-" + s.alias("flag", strings.TrimLeft(value, "-"))
	}
	return s.alias("arg", value)
}

func (s *KubernetesSanitizer) sanitizeEmbeddedIdentifiers(value string) string {
	if value == "" {
		return value
	}
	if looksLikeSensitiveString(value) {
		return s.alias("text", value)
	}
	value = privateIPPattern.ReplaceAllStringFunc(value, s.sanitizeIP)
	return value
}

func (s *KubernetesSanitizer) alias(prefix, value string) string {
	if value == "" {
		return value
	}
	if prefix == "" {
		prefix = "object"
	}
	return prefix + "-" + s.shortHash(value)
}

func (s *KubernetesSanitizer) shortHash(value string) string {
	sum := sha256.Sum256([]byte(s.salt + "\x00" + value))
	return hex.EncodeToString(sum[:])[:10]
}

func kindPrefix(kind string) string {
	switch strings.ToLower(kind) {
	case "pod":
		return "pod"
	case "node":
		return "node"
	case "service":
		return "svc"
	case "endpointslice":
		return "eps"
	case "deployment":
		return "deploy"
	case "replicaset":
		return "rs"
	case "statefulset":
		return "sts"
	case "daemonset":
		return "ds"
	case "job":
		return "job"
	case "cronjob":
		return "cronjob"
	case "secret":
		return "secret"
	case "configmap":
		return "configmap"
	case "persistentvolumeclaim":
		return "pvc"
	case "persistentvolume":
		return "pv"
	case "namespace":
		return "ns"
	case "event":
		return "event"
	default:
		if kind == "" {
			return "object"
		}
		return strings.ToLower(kind)
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func last(path []string) string {
	if len(path) == 0 {
		return ""
	}
	return path[len(path)-1]
}

func pathContains(path []string, target string) bool {
	for _, item := range path {
		if item == target {
			return true
		}
	}
	return false
}

func isObjectRef(path []string) bool {
	switch last(path) {
	case "targetRef", "involvedObject", "regarding", "related":
		return true
	default:
		return false
	}
}

func isMetadataManagedFields(path []string, key string) bool {
	return len(path) > 0 && last(path) == "metadata" && key == "managedFields"
}

func isLastAppliedAnnotation(path []string, key string) bool {
	return len(path) > 0 &&
		last(path) == "annotations" &&
		key == "kubectl.kubernetes.io/last-applied-configuration"
}

func isPayloadMapKey(lower string) bool {
	return lower == "data" || lower == "binarydata" || lower == "stringdata"
}

func isSensitiveKey(lower string) bool {
	for _, token := range []string{
		"password",
		"passwd",
		"token",
		"secret",
		"credential",
		"apikey",
		"api_key",
		"authorization",
		"auth",
		"client-key",
		"privatekey",
		"private_key",
		"tls.key",
		"ca.crt",
		"certificate",
	} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

func isKnownKubernetesKey(key string) bool {
	return strings.HasPrefix(key, "app.kubernetes.io/") ||
		strings.HasPrefix(key, "kubernetes.io/") ||
		strings.HasPrefix(key, "k8s.io/") ||
		strings.HasPrefix(key, "helm.sh/") ||
		key == "app" ||
		key == "component" ||
		key == "name" ||
		key == "version"
}

var privateIPPattern = regexp.MustCompile(`\b(?:10\.\d{1,3}\.\d{1,3}\.\d{1,3}|172\.(?:1[6-9]|2\d|3[01])\.\d{1,3}\.\d{1,3}|192\.168\.\d{1,3}\.\d{1,3})\b`)

func looksLikeSensitiveString(value string) bool {
	lower := strings.ToLower(value)
	if strings.Contains(lower, "gcp-cluster") ||
		strings.Contains(lower, "rancher") ||
		strings.Contains(lower, "staging") ||
		strings.Contains(lower, "prod") ||
		strings.Contains(lower, ".internal") ||
		strings.Contains(lower, ".network") ||
		strings.Contains(lower, ".com") ||
		strings.Contains(lower, ".io") {
		return true
	}
	return false
}
