package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/dop251/goja"
)

const (
	defaultScratchReadBytes = 64 * 1024
	maxScratchReadBytes     = 256 * 1024
	maxScratchWriteBytes    = 4 * 1024 * 1024
	maxScratchListEntries   = 200
	defaultScratchStoreTTL  = 24 * time.Hour
)

type scratchScope struct {
	SessionID string
	RunID     string
}

type scratchHandle struct {
	Path      string         `json:"path"`
	MIME      string         `json:"mime"`
	Bytes     int64          `json:"bytes"`
	SHA256    string         `json:"sha256"`
	SessionID string         `json:"sessionId,omitempty"`
	RunID     string         `json:"runId,omitempty"`
	Preview   string         `json:"preview,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	StoredAt  string         `json:"storedAt,omitempty"`
}

func installScratchHelpers(ctx context.Context, vm *goja.Runtime, writes *[]map[string]any) error {
	scope := scratchScopeFromContext(ctx)
	store := tempScratchStore{scope: scope}
	scratch := map[string]any{
		"write": func(call goja.FunctionCall) goja.Value {
			handle, err := store.write(call)
			if err != nil {
				panic(vm.ToValue(err.Error()))
			}
			out := handle.toMap()
			if writes != nil {
				*writes = append(*writes, out)
			}
			return vm.ToValue(out)
		},
		"read": func(call goja.FunctionCall) goja.Value {
			result, err := store.read(call)
			if err != nil {
				panic(vm.ToValue(err.Error()))
			}
			return vm.ToValue(result)
		},
		"load": func(call goja.FunctionCall) goja.Value {
			result, err := store.load(call)
			if err != nil {
				panic(vm.ToValue(err.Error()))
			}
			return vm.ToValue(result)
		},
		"list": func(call goja.FunctionCall) goja.Value {
			result, err := store.list(call)
			if err != nil {
				panic(vm.ToValue(err.Error()))
			}
			out := make([]map[string]any, 0, len(result))
			for _, handle := range result {
				out = append(out, handle.toMap())
			}
			return vm.ToValue(out)
		},
	}
	return vm.Set("scratch", scratch)
}

func scratchScopeFromContext(ctx context.Context) scratchScope {
	runCtx, ok := RunExecutionContextFromContext(ctx)
	if !ok {
		return scratchScope{SessionID: "standalone"}
	}
	scope := scratchScope{RunID: runCtx.RunID, SessionID: runCtx.RunID}
	if run, err := runCtx.Store.GetRun(ctx, runCtx.RunID); err == nil && run.SessionID != "" {
		scope.SessionID = run.SessionID
	}
	return scope
}

type tempScratchStore struct {
	scope scratchScope
}

func (s tempScratchStore) write(call goja.FunctionCall) (scratchHandle, error) {
	if len(call.Arguments) < 2 {
		return scratchHandle{}, errors.New("scratch.write(path, value, metadata) requires path and value")
	}
	virtualPath, physicalPath, err := s.paths(call.Arguments[0].String())
	if err != nil {
		return scratchHandle{}, err
	}
	value := call.Arguments[1].Export()
	data, err := json.Marshal(value)
	if err != nil {
		return scratchHandle{}, fmt.Errorf("scratch.write value is not JSON serializable: %w", err)
	}
	if len(data) > maxScratchWriteBytes {
		return scratchHandle{}, fmt.Errorf("scratch.write payload too large: %d bytes > %d", len(data), maxScratchWriteBytes)
	}
	metadata := map[string]any{}
	if len(call.Arguments) >= 3 {
		if raw, ok := call.Arguments[2].Export().(map[string]any); ok {
			metadata = raw
		}
	}
	if err := os.MkdirAll(filepath.Dir(physicalPath), 0o700); err != nil {
		return scratchHandle{}, err
	}
	tmpPath := physicalPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return scratchHandle{}, err
	}
	if err := os.Rename(tmpPath, physicalPath); err != nil {
		_ = os.Remove(tmpPath)
		return scratchHandle{}, err
	}
	return s.handle(virtualPath, data, metadata), nil
}

func (s tempScratchStore) read(call goja.FunctionCall) (map[string]any, error) {
	if len(call.Arguments) < 1 {
		return nil, errors.New("scratch.read(path, options) requires path")
	}
	virtualPath, physicalPath, err := s.paths(call.Arguments[0].String())
	if err != nil {
		return nil, err
	}
	opts := map[string]any{}
	if len(call.Arguments) >= 2 {
		if raw, ok := call.Arguments[1].Export().(map[string]any); ok {
			opts = raw
		}
	}
	data, err := os.ReadFile(physicalPath)
	if err != nil {
		return nil, err
	}
	offset := boundedScratchInt(opts["offset"], 0, len(data))
	limit := boundedScratchInt(opts["limit"], defaultScratchReadBytes, maxScratchReadBytes)
	end := offset + limit
	if end > len(data) {
		end = len(data)
	}
	chunk := data[offset:end]
	out := map[string]any{
		"path":      virtualPath,
		"mime":      "application/json",
		"bytes":     len(data),
		"offset":    offset,
		"limit":     limit,
		"truncated": end < len(data),
		"sha256":    scratchHash(data),
		"sessionId": s.scope.SessionID,
		"runId":     s.scope.RunID,
		"content":   string(chunk),
		"readAt":    time.Now().UTC().Format(time.RFC3339Nano),
	}
	if offset == 0 && end == len(data) {
		var value any
		if err := json.Unmarshal(data, &value); err == nil {
			out["value"] = value
			delete(out, "content")
		}
	}
	return out, nil
}

func (s tempScratchStore) load(call goja.FunctionCall) (any, error) {
	if len(call.Arguments) < 1 {
		return nil, errors.New("scratch.load(path) requires path")
	}
	_, physicalPath, err := s.paths(call.Arguments[0].String())
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(physicalPath)
	if err != nil {
		return nil, err
	}
	if len(data) > maxScratchWriteBytes {
		return nil, fmt.Errorf("scratch.load payload too large: %d bytes > %d", len(data), maxScratchWriteBytes)
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, fmt.Errorf("scratch.load could not parse JSON: %w", err)
	}
	return value, nil
}

func (s tempScratchStore) list(call goja.FunctionCall) ([]scratchHandle, error) {
	prefix := ""
	if len(call.Arguments) > 0 {
		prefix = call.Arguments[0].String()
	}
	root := s.root()
	entries := []scratchHandle{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return err
		}
		if len(entries) >= maxScratchListEntries {
			return filepath.SkipAll
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		virtualPath := "/" + filepath.ToSlash(rel)
		if prefix != "" && !strings.HasPrefix(virtualPath, prefix) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		entries = append(entries, s.handle(virtualPath, data, nil))
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return entries, nil
	}
	return entries, err
}

func (s tempScratchStore) paths(path string) (string, string, error) {
	clean, err := cleanScratchPath(path)
	if err != nil {
		return "", "", err
	}
	return "/" + filepath.ToSlash(clean), filepath.Join(s.root(), clean), nil
}

func (s tempScratchStore) root() string {
	scope := s.scope.SessionID
	if scope == "" {
		scope = "standalone"
	}
	return scratchSessionRoot(scope)
}

func (s tempScratchStore) handle(path string, data []byte, metadata map[string]any) scratchHandle {
	return scratchHandle{
		Path:      path,
		MIME:      "application/json",
		Bytes:     int64(len(data)),
		SHA256:    scratchHash(data),
		SessionID: s.scope.SessionID,
		RunID:     s.scope.RunID,
		Preview:   compactText(string(data), 500),
		Metadata:  metadata,
		StoredAt:  time.Now().UTC().Format(time.RFC3339Nano),
	}
}

func (h scratchHandle) toMap() map[string]any {
	out := map[string]any{
		"path":      h.Path,
		"mime":      h.MIME,
		"bytes":     h.Bytes,
		"sha256":    h.SHA256,
		"preview":   h.Preview,
		"storedAt":  h.StoredAt,
		"metadata":  h.Metadata,
		"sessionId": h.SessionID,
		"runId":     h.RunID,
	}
	if h.Metadata == nil {
		out["metadata"] = map[string]any{}
	}
	return out
}

func cleanScratchPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("scratch path is required")
	}
	path = strings.TrimPrefix(path, "/")
	clean := filepath.Clean(path)
	if clean == "." || strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return "", fmt.Errorf("invalid scratch path: %q", path)
	}
	return clean, nil
}

func safeScratchSegment(value string) string {
	var b strings.Builder
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	if b.Len() == 0 {
		return "standalone"
	}
	return b.String()
}

func boundedScratchInt(value any, fallback, maximum int) int {
	out := fallback
	switch v := value.(type) {
	case int:
		out = v
	case int64:
		out = int(v)
	case float64:
		out = int(v)
	case string:
		var parsed int
		if _, err := fmt.Sscanf(v, "%d", &parsed); err == nil {
			out = parsed
		}
	}
	if out < 0 {
		return 0
	}
	if out > maximum {
		return maximum
	}
	return out
}

func scratchHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func scratchHandlesFromToolOutput(data json.RawMessage) []map[string]any {
	var value any
	if len(data) == 0 || json.Unmarshal(data, &value) != nil {
		return nil
	}
	seen := map[string]bool{}
	var handles []map[string]any
	collectScratchHandles(value, seen, &handles)
	return handles
}

func collectScratchHandles(value any, seen map[string]bool, handles *[]map[string]any) {
	switch typed := value.(type) {
	case map[string]any:
		if handle, ok := scratchHandleMap(typed); ok {
			key := fmt.Sprint(handle["path"]) + "#" + fmt.Sprint(handle["sha256"])
			if !seen[key] {
				seen[key] = true
				*handles = append(*handles, handle)
			}
		}
		for _, child := range typed {
			collectScratchHandles(child, seen, handles)
		}
	case []any:
		for _, child := range typed {
			collectScratchHandles(child, seen, handles)
		}
	}
}

func scratchHandleMap(record map[string]any) (map[string]any, bool) {
	path, _ := record["path"].(string)
	hash, _ := record["sha256"].(string)
	if !strings.HasPrefix(path, "/") || len(hash) < 16 {
		return nil, false
	}
	out := map[string]any{
		"path":   path,
		"sha256": hash,
	}
	for _, key := range []string{"mime", "bytes", "sessionId", "runId", "preview", "storedAt", "metadata"} {
		if value, ok := record[key]; ok {
			out[key] = value
		}
	}
	return out, true
}

func scratchBaseRoot() string {
	return filepath.Join(os.TempDir(), "kube-insight-agent-scratch")
}

func scratchSessionRoot(sessionID string) string {
	return filepath.Join(scratchBaseRoot(), safeScratchSegment(sessionID))
}

func CleanupScratchStores(ctx context.Context, runs []Run, opts RetentionOptions, now time.Time) (RetentionReport, error) {
	report := RetentionReport{}
	if !opts.PruneScratchStores {
		return report, nil
	}
	maxAge := defaultScratchStoreTTL
	if opts.ScratchMaxAgeSeconds > 0 {
		maxAge = time.Duration(opts.ScratchMaxAgeSeconds) * time.Second
	}
	protected := map[string]struct{}{}
	for _, run := range runs {
		if run.SessionID == "" || statusTerminal(run.Status) {
			continue
		}
		protected[safeScratchSegment(run.SessionID)] = struct{}{}
	}
	root := scratchBaseRoot()
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return report, nil
	}
	if err != nil {
		return report, err
	}
	cutoff := now.Add(-maxAge)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		if !entry.IsDir() {
			continue
		}
		sessionSegment := entry.Name()
		if _, ok := protected[sessionSegment]; ok {
			continue
		}
		path := filepath.Join(root, sessionSegment)
		modTime, bytes, err := scratchDirStats(path)
		if err != nil {
			return report, err
		}
		if modTime.IsZero() || modTime.After(cutoff) {
			continue
		}
		report.ScratchStoresDeleted++
		report.ScratchBytesDeleted += bytes
		report.ScratchSessionIDs = append(report.ScratchSessionIDs, sessionSegment)
		if opts.DryRun {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			return report, err
		}
	}
	return report, nil
}

func scratchDirStats(root string) (time.Time, int64, error) {
	var latest time.Time
	var bytes int64
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d == nil {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.ModTime().After(latest) {
			latest = info.ModTime()
		}
		if !d.IsDir() {
			bytes += info.Size()
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return time.Time{}, 0, nil
	}
	return latest, bytes, err
}
