package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"kube-insight/internal/storage/sqlite"
)

const protocolVersion = "2025-06-18"

type ServerOptions struct {
	DBPath string
	Name   string
}

type Server struct {
	dbPath string
	name   string
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type promptGetParams struct {
	Name      string            `json:"name"`
	Arguments map[string]string `json:"arguments,omitempty"`
}

type sqlArguments struct {
	SQL     string `json:"sql"`
	MaxRows int    `json:"maxRows,omitempty"`
}

type healthArguments struct {
	ClusterID        string   `json:"cluster,omitempty"`
	Status           string   `json:"status,omitempty"`
	ErrorsOnly       bool     `json:"errorsOnly,omitempty"`
	StaleAfter       string   `json:"staleAfter,omitempty"`
	Limit            int      `json:"limit,omitempty"`
	ExcludeResources []string `json:"exclude,omitempty"`
	IncludeSkipped   bool     `json:"includeSkipped,omitempty"`
}

type historyArguments struct {
	ClusterID       string `json:"cluster,omitempty"`
	UID             string `json:"uid,omitempty"`
	Kind            string `json:"kind,omitempty"`
	Namespace       string `json:"namespace,omitempty"`
	Name            string `json:"name,omitempty"`
	From            string `json:"from,omitempty"`
	To              string `json:"to,omitempty"`
	MaxVersions     int    `json:"maxVersions,omitempty"`
	MaxObservations int    `json:"maxObservations,omitempty"`
	IncludeDocs     bool   `json:"includeDocs,omitempty"`
	Diffs           *bool  `json:"diffs,omitempty"`
}

type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func NewServer(opts ServerOptions) (*Server, error) {
	if opts.DBPath == "" {
		return nil, errors.New("mcp server requires a sqlite database path")
	}
	name := opts.Name
	if name == "" {
		name = "kube-insight"
	}
	return &Server{dbPath: opts.DBPath, name: name}, nil
}

func ServeStdio(ctx context.Context, in io.Reader, out io.Writer, opts ServerOptions) error {
	server, err := NewServer(opts)
	if err != nil {
		return err
	}
	return server.ServeStdio(ctx, in, out)
}

func ListenAndServe(ctx context.Context, listen string, opts ServerOptions) error {
	if listen == "" {
		listen = "127.0.0.1:8090"
	}
	server, err := NewServer(opts)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeHTTPJSON(w, http.StatusOK, map[string]any{"ok": true, "transport": "http"})
	})
	mux.HandleFunc("POST /mcp", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeHTTPJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		response, ok, err := server.HandleJSONRPC(r.Context(), body)
		if err != nil {
			writeHTTPJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		if !ok {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(response)
	})
	httpServer := &http.Server{
		Addr:              listen,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	done := make(chan error, 1)
	go func() {
		done <- httpServer.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return err
		}
		err := <-done
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (s *Server) ServeStdio(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		response, ok, err := s.HandleJSONRPC(ctx, line)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if _, err := out.Write(response); err != nil {
			return err
		}
		if _, err := out.Write([]byte("\n")); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

func (s *Server) HandleJSONRPC(ctx context.Context, payload []byte) ([]byte, bool, error) {
	response := s.handleLine(ctx, payload)
	if response == nil {
		return nil, false, nil
	}
	data, err := json.Marshal(response)
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func (s *Server) handleLine(ctx context.Context, line []byte) *rpcResponse {
	var request rpcRequest
	if err := json.Unmarshal(line, &request); err != nil {
		return errorResponse(nil, -32700, "parse error")
	}
	if len(request.ID) == 0 {
		return nil
	}
	id := rawID(request.ID)
	if request.JSONRPC != "2.0" {
		return errorResponse(id, -32600, "invalid JSON-RPC version")
	}
	switch request.Method {
	case "initialize":
		return resultResponse(id, map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities": map[string]any{
				"tools":   map[string]any{},
				"prompts": map[string]any{},
			},
			"serverInfo": map[string]any{
				"name":    s.name,
				"version": "0.1.0-dev",
			},
		})
	case "tools/list":
		return resultResponse(id, map[string]any{"tools": tools()})
	case "tools/call":
		result, err := s.callTool(ctx, request.Params)
		if err != nil {
			return errorResponse(id, -32602, err.Error())
		}
		return resultResponse(id, result)
	case "prompts/list":
		return resultResponse(id, map[string]any{"prompts": prompts()})
	case "prompts/get":
		result, err := promptResult(request.Params)
		if err != nil {
			return errorResponse(id, -32602, err.Error())
		}
		return resultResponse(id, result)
	default:
		return errorResponse(id, -32601, "method not found")
	}
}

func (s *Server) callTool(ctx context.Context, params json.RawMessage) (map[string]any, error) {
	var input toolCallParams
	if err := json.Unmarshal(params, &input); err != nil {
		return nil, fmt.Errorf("invalid tool call params: %w", err)
	}
	switch input.Name {
	case "kube_insight_schema":
		value, err := s.querySchema(ctx)
		return toolResult(value, err)
	case "kube_insight_sql":
		var args sqlArguments
		if err := json.Unmarshal(input.Arguments, &args); err != nil {
			return nil, fmt.Errorf("invalid sql arguments: %w", err)
		}
		value, err := s.querySQL(ctx, args)
		return toolResult(value, err)
	case "kube_insight_health":
		var args healthArguments
		if err := json.Unmarshal(input.Arguments, &args); err != nil {
			return nil, fmt.Errorf("invalid health arguments: %w", err)
		}
		value, err := s.queryHealth(ctx, args)
		return toolResult(value, err)
	case "kube_insight_history":
		var args historyArguments
		if err := json.Unmarshal(input.Arguments, &args); err != nil {
			return nil, fmt.Errorf("invalid history arguments: %w", err)
		}
		value, err := s.queryHistory(ctx, args)
		return toolResult(value, err)
	default:
		return nil, fmt.Errorf("unknown tool %q", input.Name)
	}
}

func (s *Server) querySchema(ctx context.Context) (any, error) {
	store, err := sqlite.OpenReadOnly(s.dbPath)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	return store.QuerySchema(ctx)
}

func (s *Server) querySQL(ctx context.Context, args sqlArguments) (any, error) {
	store, err := sqlite.OpenReadOnly(s.dbPath)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	return store.QuerySQL(ctx, sqlite.SQLQueryOptions{
		SQL:     args.SQL,
		MaxRows: args.MaxRows,
	})
}

func (s *Server) queryHealth(ctx context.Context, args healthArguments) (any, error) {
	opts := sqlite.ResourceHealthOptions{
		ClusterID:        args.ClusterID,
		Status:           args.Status,
		ErrorsOnly:       args.ErrorsOnly,
		Limit:            args.Limit,
		ExcludeResources: args.ExcludeResources,
		IncludeExcluded:  args.IncludeSkipped,
	}
	if args.StaleAfter != "" {
		value, err := time.ParseDuration(args.StaleAfter)
		if err != nil {
			return nil, fmt.Errorf("staleAfter: %w", err)
		}
		opts.StaleAfter = value
	}
	store, err := sqlite.OpenReadOnly(s.dbPath)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	return store.ResourceHealth(ctx, opts)
}

func (s *Server) queryHistory(ctx context.Context, args historyArguments) (any, error) {
	target := sqlite.ObjectTarget{
		ClusterID: args.ClusterID,
		UID:       args.UID,
		Kind:      args.Kind,
		Namespace: args.Namespace,
		Name:      args.Name,
	}
	opts := sqlite.ObjectHistoryOptions{
		MaxVersions:     args.MaxVersions,
		MaxObservations: args.MaxObservations,
		IncludeDocs:     args.IncludeDocs,
		IncludeDiffs:    true,
	}
	var err error
	if args.From != "" {
		opts.From, err = parseHistoryTime(args.From)
		if err != nil {
			return nil, fmt.Errorf("from: %w", err)
		}
	}
	if args.To != "" {
		opts.To, err = parseHistoryTime(args.To)
		if err != nil {
			return nil, fmt.Errorf("to: %w", err)
		}
	}
	if !opts.From.IsZero() && !opts.To.IsZero() && opts.From.After(opts.To) {
		return nil, fmt.Errorf("from must be before to")
	}
	if args.Diffs != nil {
		opts.IncludeDiffs = *args.Diffs
	}
	store, err := sqlite.OpenReadOnly(s.dbPath)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	return store.ObjectHistory(ctx, target, opts)
}

func toolResult(value any, err error) (map[string]any, error) {
	if err != nil {
		return map[string]any{
			"isError": true,
			"content": []toolContent{{
				Type: "text",
				Text: err.Error(),
			}},
		}, nil
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"content": []toolContent{{
			Type: "text",
			Text: string(data),
		}},
	}, nil
}

func tools() []map[string]any {
	return []map[string]any{
		{
			"name":        "kube_insight_schema",
			"description": "Return kube-insight SQL tables, columns, indexes, and join hints.",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			"name":        "kube_insight_sql",
			"description": "Run read-only SQL against kube-insight evidence storage.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"sql": map[string]any{
						"type":        "string",
						"description": "Read-only SELECT/WITH/EXPLAIN SQL.",
					},
					"maxRows": map[string]any{
						"type":        "integer",
						"description": "Maximum rows to return.",
					},
				},
				"required": []string{"sql"},
			},
		},
		{
			"name":        "kube_insight_health",
			"description": "Return collector coverage, staleness, and per-resource health.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"cluster": map[string]any{"type": "string"},
					"status":  map[string]any{"type": "string"},
					"errorsOnly": map[string]any{
						"type":        "boolean",
						"description": "Only include resources with list/watch errors.",
					},
					"staleAfter": map[string]any{
						"type":        "string",
						"description": "Duration such as 10m or 1h.",
					},
					"limit": map[string]any{"type": "integer"},
					"exclude": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Resource names to mark as skipped, such as events or leases.coordination.k8s.io.",
					},
					"includeSkipped": map[string]any{
						"type":        "boolean",
						"description": "Include skipped resources in the returned rows.",
					},
				},
			},
		},
		{
			"name":        "kube_insight_history",
			"description": "Return one object's retained content versions, observation trail, and optional version diffs.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"cluster":         map[string]any{"type": "string"},
					"uid":             map[string]any{"type": "string"},
					"kind":            map[string]any{"type": "string"},
					"namespace":       map[string]any{"type": "string"},
					"name":            map[string]any{"type": "string"},
					"from":            map[string]any{"type": "string", "description": "RFC3339 or YYYY-MM-DD."},
					"to":              map[string]any{"type": "string", "description": "RFC3339 or YYYY-MM-DD."},
					"maxVersions":     map[string]any{"type": "integer"},
					"maxObservations": map[string]any{"type": "integer"},
					"includeDocs":     map[string]any{"type": "boolean"},
					"diffs":           map[string]any{"type": "boolean"},
				},
			},
		},
	}
}

func prompts() []map[string]any {
	return []map[string]any{
		{
			"name":        "kube_insight_coverage_first",
			"description": "Start an investigation by checking collector health and cluster scope.",
			"arguments": []map[string]any{
				{"name": "cluster", "description": "Optional kube-insight cluster name.", "required": false},
				{"name": "symptom", "description": "Short description of the incident or question.", "required": false},
			},
		},
		{
			"name":        "kube_insight_event_history",
			"description": "Investigate retained Kubernetes Events and follow Event edges to affected resources.",
			"arguments": []map[string]any{
				{"name": "cluster", "description": "Optional kube-insight cluster name.", "required": false},
				{"name": "reason", "description": "Optional Event reason such as PolicyViolation or FailedScheduling.", "required": false},
				{"name": "keyword", "description": "Optional lowercase keyword to search in message previews.", "required": false},
			},
		},
		{
			"name":        "kube_insight_object_history",
			"description": "Inspect one object's retained versions, observations, and diffs as proof.",
			"arguments": []map[string]any{
				{"name": "cluster", "description": "Optional kube-insight cluster name.", "required": false},
				{"name": "kind", "description": "Kubernetes Kind.", "required": false},
				{"name": "namespace", "description": "Namespace for namespaced resources.", "required": false},
				{"name": "name", "description": "Object name.", "required": false},
				{"name": "uid", "description": "Object UID when known.", "required": false},
			},
		},
	}
}

func promptResult(params json.RawMessage) (map[string]any, error) {
	var input promptGetParams
	if err := json.Unmarshal(params, &input); err != nil {
		return nil, fmt.Errorf("invalid prompt params: %w", err)
	}
	text, description, err := promptText(input.Name, input.Arguments)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"description": description,
		"messages": []map[string]any{
			{
				"role": "user",
				"content": map[string]any{
					"type": "text",
					"text": text,
				},
			},
		},
	}, nil
}

func promptText(name string, args map[string]string) (string, string, error) {
	switch name {
	case "kube_insight_coverage_first":
		symptom := promptArg(args, "symptom", "the reported Kubernetes problem")
		cluster := promptArg(args, "cluster", "the relevant cluster")
		return fmt.Sprintf(`Investigate %s with kube-insight.

Use this order:
1. Call kube_insight_health for %s. Treat list/watch errors and stale resources as evidence gaps.
2. Call kube_insight_schema and list clusters with SQL: select id, name, source from clusters order by id.
3. Pick the relevant cluster id and keep cluster_id in follow-up SQL.
4. Query facts and edges for candidate resources. Prefer exact fact_key/fact_value predicates before broad text search.
5. Use kube_insight_history only for the final candidate objects or as proof for claims.

Do not claim absence unless collector coverage is healthy for the resource types involved.`, symptom, cluster), "Coverage-first kube-insight investigation", nil
	case "kube_insight_event_history":
		cluster := promptArg(args, "cluster", "the relevant cluster")
		reason := promptArg(args, "reason", "the Event reason")
		keyword := promptArg(args, "keyword", "the message keyword")
		return fmt.Sprintf(`Investigate retained Kubernetes Events in %s.

Use kube_insight_sql to:
1. List clusters and select one cluster_id.
2. Count Warning Events by k8s_event.reason, narrowing to %s when provided.
3. Search k8s_event.message_preview for %s only after the reason query is scoped by cluster_id.
4. Join Event objects through object_edges with event_regarding_object, event_related_object, or event_involves_object to identify affected resources.
5. Fetch kube_insight_history for the affected resource and the Event when proof is needed.

Compare retained Event history with current kubectl only as separate evidence; kubectl shows live apiserver state, not the retained window.`, cluster, reason, keyword), "Retained Event history investigation", nil
	case "kube_insight_object_history":
		target := promptObjectTarget(args)
		return fmt.Sprintf(`Inspect object history for %s.

Use kube_insight_history with the most specific identifier available, preferring uid when known. Include diffs and keep maxVersions/maxObservations bounded at first.

Summarize:
1. First and last observed times.
2. Content-changing versions versus unchanged observations.
3. Delete observations, if any. Treat deleted_at as the kube-insight delete observation time, not metadata.deletionTimestamp.
4. Relevant version diffs and facts/edges that explain the incident.

Use retained documents as proof before making a final claim.`, target), "Object history proof workflow", nil
	default:
		return "", "", fmt.Errorf("unknown prompt %q", name)
	}
}

func promptArg(args map[string]string, key, fallback string) string {
	if args == nil || args[key] == "" {
		return fallback
	}
	return args[key]
}

func promptObjectTarget(args map[string]string) string {
	if args == nil {
		return "the target object"
	}
	if args["uid"] != "" {
		return "uid " + args["uid"]
	}
	kind := promptArg(args, "kind", "Kind")
	name := promptArg(args, "name", "name")
	if args["namespace"] != "" {
		return kind + " " + args["namespace"] + "/" + name
	}
	return kind + " " + name
}

func parseHistoryTime(value string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02", value)
}

func resultResponse(id any, result any) *rpcResponse {
	return &rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func errorResponse(id any, code int, message string) *rpcResponse {
	return &rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: message},
	}
}

func writeHTTPJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func rawID(raw json.RawMessage) any {
	var id any
	if err := json.Unmarshal(raw, &id); err != nil {
		return nil
	}
	return id
}
