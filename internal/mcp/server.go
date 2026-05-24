package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"kube-insight/internal/storage"
	"kube-insight/internal/storage/sqlite"
)

type ServerOptions struct {
	DBPath        string
	Name          string
	OpenStore     StoreOpener
	KeepStoreOpen bool
	Close         func() error
}

type StoreOpener func(context.Context) (ReadStore, error)

type ReadStore interface {
	Close() error
}

type Server struct {
	dbPath              string
	name                string
	openStore           StoreOpener
	closeStoreOnRequest bool
	closeFunc           func() error
	sdkServer           *sdkmcp.Server
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

func NewServer(opts ServerOptions) (*Server, error) {
	openStore := opts.OpenStore
	if openStore == nil {
		if opts.DBPath == "" {
			return nil, errors.New("mcp server requires a read store or sqlite database path")
		}
		openStore = sqliteStoreOpener(opts.DBPath)
	}
	name := opts.Name
	if name == "" {
		name = "kube-insight"
	}
	server := &Server{
		dbPath:              opts.DBPath,
		name:                name,
		openStore:           openStore,
		closeStoreOnRequest: !opts.KeepStoreOpen,
		closeFunc:           opts.Close,
	}
	server.sdkServer = sdkmcp.NewServer(&sdkmcp.Implementation{
		Name:    name,
		Version: "0.1.0-dev",
	}, &sdkmcp.ServerOptions{
		Capabilities: &sdkmcp.ServerCapabilities{},
	})
	server.registerSDKFeatures()
	return server, nil
}

func sqliteStoreOpener(path string) StoreOpener {
	return func(context.Context) (ReadStore, error) {
		return sqlite.OpenReadOnly(path)
	}
}

func ServeStdio(ctx context.Context, in io.Reader, out io.Writer, opts ServerOptions) error {
	server, err := NewServer(opts)
	if err != nil {
		return err
	}
	defer server.Close()
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
	defer server.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeHTTPJSON(w, http.StatusOK, map[string]any{"ok": true, "transport": "streamable-http+sse"})
	})
	mux.Handle("/mcp", server.StreamableHTTPHandler(30*time.Minute))
	mux.Handle("/sse", sdkmcp.NewSSEHandler(func(*http.Request) *sdkmcp.Server {
		return server.sdkServer
	}, nil))
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
	return s.sdkServer.Run(ctx, &sdkmcp.IOTransport{
		Reader: nopReadCloser{Reader: in},
		Writer: nopWriteCloser{Writer: out},
	})
}

func (s *Server) StreamableHTTPHandler(sessionTimeout time.Duration) http.Handler {
	if sessionTimeout <= 0 {
		sessionTimeout = 30 * time.Minute
	}
	return sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server {
		return s.sdkServer
	}, &sdkmcp.StreamableHTTPOptions{
		SessionTimeout: sessionTimeout,
	})
}

func (s *Server) Close() error {
	if s.closeFunc == nil {
		return nil
	}
	return s.closeFunc()
}

func (s *Server) openReadStore(ctx context.Context) (ReadStore, error) {
	return s.openStore(ctx)
}

func (s *Server) closeReadStore(store ReadStore) {
	if s.closeStoreOnRequest && store != nil {
		_ = store.Close()
	}
}

func (s *Server) registerSDKFeatures() {
	for _, tool := range tools() {
		current := tool
		s.sdkServer.AddTool(&current, s.callTool)
	}
	for _, prompt := range prompts() {
		current := prompt
		s.sdkServer.AddPrompt(&current, promptResult)
	}
}

func (s *Server) callTool(ctx context.Context, request *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
	switch request.Params.Name {
	case "kube_insight_schema":
		value, err := s.querySchema(ctx)
		return toolResult(value, err)
	case "kube_insight_sql":
		var args sqlArguments
		if err := unmarshalToolArguments(request.Params.Arguments, &args); err != nil {
			return nil, fmt.Errorf("invalid sql arguments: %w", err)
		}
		value, err := s.querySQL(ctx, args)
		return toolResult(value, err)
	case "kube_insight_health":
		var args healthArguments
		if err := unmarshalToolArguments(request.Params.Arguments, &args); err != nil {
			return nil, fmt.Errorf("invalid health arguments: %w", err)
		}
		value, err := s.queryHealth(ctx, args)
		return toolResult(value, err)
	case "kube_insight_search":
		var args searchArguments
		if err := unmarshalToolArguments(request.Params.Arguments, &args); err != nil {
			return nil, fmt.Errorf("invalid search arguments: %w", err)
		}
		value, err := s.querySearch(ctx, args)
		return toolResult(value, err)
	case "kube_insight_history":
		var args historyArguments
		if err := unmarshalToolArguments(request.Params.Arguments, &args); err != nil {
			return nil, fmt.Errorf("invalid history arguments: %w", err)
		}
		value, err := s.queryHistory(ctx, args)
		return toolResult(value, err)
	case "kube_insight_topology":
		var args topologyArguments
		if err := unmarshalToolArguments(request.Params.Arguments, &args); err != nil {
			return nil, fmt.Errorf("invalid topology arguments: %w", err)
		}
		value, err := s.queryTopology(ctx, args)
		return toolResult(value, err)
	case "kube_insight_service_investigation":
		var args serviceInvestigationArguments
		if err := unmarshalToolArguments(request.Params.Arguments, &args); err != nil {
			return nil, fmt.Errorf("invalid service investigation arguments: %w", err)
		}
		value, err := s.queryServiceInvestigation(ctx, args)
		return toolResult(value, err)
	default:
		return nil, fmt.Errorf("unknown tool %q", request.Params.Name)
	}
}

func (s *Server) querySchema(ctx context.Context) (any, error) {
	store, err := s.openReadStore(ctx)
	if err != nil {
		return nil, err
	}
	defer s.closeReadStore(store)
	queryStore, ok := store.(storage.SQLQueryStore)
	if !ok {
		return nil, fmt.Errorf("configured store does not support schema queries")
	}
	schema, err := queryStore.QuerySchema(ctx)
	if err != nil {
		return nil, err
	}
	return formatSQLSchemaDSL(schema), nil
}

func (s *Server) querySQL(ctx context.Context, args sqlArguments) (any, error) {
	store, err := s.openReadStore(ctx)
	if err != nil {
		return nil, err
	}
	defer s.closeReadStore(store)
	queryStore, ok := store.(storage.SQLQueryStore)
	if !ok {
		return nil, fmt.Errorf("configured store does not support SQL queries")
	}
	return queryStore.QuerySQL(ctx, storage.SQLQueryOptions{
		SQL:     args.SQL,
		MaxRows: args.MaxRows,
	})
}

func (s *Server) queryHealth(ctx context.Context, args healthArguments) (any, error) {
	opts := storage.ResourceHealthOptions{
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
	store, err := s.openReadStore(ctx)
	if err != nil {
		return nil, err
	}
	defer s.closeReadStore(store)
	healthStore, ok := store.(storage.ResourceHealthStore)
	if !ok {
		return nil, fmt.Errorf("configured store does not support resource health")
	}
	report, err := healthStore.ResourceHealth(ctx, opts)
	if err != nil {
		return nil, err
	}
	return formatResourceHealthDSL(report, 10), nil
}

func (s *Server) queryHistory(ctx context.Context, args historyArguments) (any, error) {
	target := storage.ObjectTarget{
		ClusterID: args.ClusterID,
		UID:       args.UID,
		Kind:      args.Kind,
		Namespace: args.Namespace,
		Name:      args.Name,
	}
	opts := storage.ObjectHistoryOptions{
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
	store, err := s.openReadStore(ctx)
	if err != nil {
		return nil, err
	}
	defer s.closeReadStore(store)
	historyStore, ok := store.(storage.ObjectHistoryStore)
	if !ok {
		return nil, fmt.Errorf("configured store does not support object history")
	}
	return historyStore.ObjectHistory(ctx, target, opts)
}

func toolResult(value any, err error) (*sdkmcp.CallToolResult, error) {
	if err != nil {
		return &sdkmcp.CallToolResult{
			IsError: true,
			Content: []sdkmcp.Content{&sdkmcp.TextContent{
				Text: err.Error(),
			}},
		}, nil
	}
	var text string
	if valueText, ok := value.(string); ok {
		text = valueText
	} else {
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return nil, err
		}
		text = string(data)
	}
	return &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{&sdkmcp.TextContent{
			Text: text,
		}},
	}, nil
}

func tools() []sdkmcp.Tool {
	return []sdkmcp.Tool{
		{
			Name:        "kube_insight_schema",
			Description: "Return the active kube-insight backend schema as a compact DSL for LLM SQL planning, including dialect notes, useful tables, columns, indexes, joins, and recipes. Call this before writing SQL because SQLite and ClickHouse table names differ. Do not call schema when health, search, history, topology, or service investigation already provides enough evidence.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "kube_insight_sql",
			Description: "Run read-only SQL against the configured kube-insight evidence store for precise discovery, ranking, aggregation, and proof rows that typed tools cannot already provide. Always call kube_insight_schema first and write SQL for the reported backend/dialect; do not assume SQLite table names when schema notes show ClickHouse-compatible tables. Keep maxRows bounded. Do not use SQL to re-confirm facts, changes, versions, or topology already returned by typed tools.",
			InputSchema: map[string]any{
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
			Name:        "kube_insight_health",
			Description: "Summarize collector coverage, staleness, and resource stream health before making current-state claims. Returns a compact DSL summary plus a bounded list of problematic resources by default; the HTTP /api/v1/health endpoint is much larger and intended for dashboards.",
			InputSchema: map[string]any{
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
			Name:        "kube_insight_search",
			Description: "Search kube-insight evidence to find candidate Kubernetes objects from symptoms, names, labels, statuses, facts, changes, retained documents, and indexed evidence. Use after kube_insight_health for broad discovery. Start with includeBundles=false and narrow by kind, namespace, cluster, or time; set includeBundles=true only for top targets that need compact proof.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query":                map[string]any{"type": "string", "description": "Search terms, symptoms, object names, labels, statuses, error text, or other evidence keywords."},
					"clusterId":            map[string]any{"type": "string"},
					"kind":                 map[string]any{"type": "string"},
					"namespace":            map[string]any{"type": "string"},
					"from":                 map[string]any{"type": "string", "description": "RFC3339 timestamp or YYYY-MM-DD lower bound."},
					"to":                   map[string]any{"type": "string", "description": "RFC3339 timestamp or YYYY-MM-DD upper bound."},
					"limit":                map[string]any{"type": "integer", "description": "Maximum matches to return. Defaults to 20 and caps at 100."},
					"maxVersionsPerObject": map[string]any{"type": "integer", "description": "When includeBundles is true, cap retained versions per object. Caps at 5."},
					"includeBundles":       map[string]any{"type": "boolean", "description": "Include compact evidence bundles for matched objects."},
					"includeHealth":        map[string]any{"type": "boolean", "description": "Include collector coverage summary. Defaults to true."},
					"healthStaleAfter":     map[string]any{"type": "string", "description": "Go duration such as 5m or 1h for coverage freshness when includeHealth is true."},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "kube_insight_history",
			Description: "Return one known object's retained content versions, observation trail, and optional version diffs. Use after search or SQL identifies the exact object. Keep maxVersions and maxObservations bounded; leave includeDocs=false unless raw YAML/JSON proof is explicitly needed.",
			InputSchema: map[string]any{
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
		{
			Name:        "kube_insight_topology",
			Description: "Load the retained topology graph around one known Kubernetes object. Use this to inspect Service, EndpointSlice, Pod, Node, owner, and event relationships after search or SQL identifies a target; do not use it for broad discovery. One call around the best root is usually enough for a namespace map; do not call repeatedly for every returned node unless the graph is incomplete.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"clusterId": map[string]any{"type": "string"},
					"kind":      map[string]any{"type": "string", "description": "Kubernetes kind such as Service, Pod, Node, Deployment, or Event."},
					"namespace": map[string]any{"type": "string"},
					"name":      map[string]any{"type": "string"},
					"uid":       map[string]any{"type": "string", "description": "Optional Kubernetes UID to disambiguate recreated objects."},
				},
				"required": []string{"kind", "name"},
			},
		},
		{
			Name:        "kube_insight_service_investigation",
			Description: "Load a compact typed Service investigation bundle, including Service evidence, related EndpointSlices, Pods, Nodes, Events, facts, changes, and topology edges. Use only when the target Kubernetes object is an exact Service namespace/name. For Service health, this result plus kube_insight_health is usually enough; answer instead of calling schema, SQL, history, or topology again unless the bundle is incomplete. Do not use this tool just to answer recent-changes questions when search and history already returned changes. Start with low limits, then expand only if the compact bundle does not answer the question.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"clusterId":            map[string]any{"type": "string"},
					"namespace":            map[string]any{"type": "string", "description": "Service namespace."},
					"name":                 map[string]any{"type": "string", "description": "Service name."},
					"from":                 map[string]any{"type": "string", "description": "RFC3339 timestamp or YYYY-MM-DD lower bound."},
					"to":                   map[string]any{"type": "string", "description": "RFC3339 timestamp or YYYY-MM-DD upper bound."},
					"maxEvidenceObjects":   map[string]any{"type": "integer", "description": "Maximum related evidence objects. Defaults to 20 and caps at 100."},
					"maxVersionsPerObject": map[string]any{"type": "integer", "description": "Maximum retained versions per object. Defaults to 3 and caps at 10."},
					"maxFactsPerObject":    map[string]any{"type": "integer", "description": "Maximum facts per object. Defaults to 20 and caps at 100."},
					"maxChangesPerObject":  map[string]any{"type": "integer", "description": "Maximum changes per object. Defaults to 20 and caps at 100."},
				},
				"required": []string{"namespace", "name"},
			},
		},
	}
}

func prompts() []sdkmcp.Prompt {
	return []sdkmcp.Prompt{
		{
			Name:        "kube_insight_coverage_first",
			Description: "Start an investigation by checking collector health and cluster scope.",
			Arguments: []*sdkmcp.PromptArgument{
				{Name: "cluster", Description: "Optional kube-insight cluster name.", Required: false},
				{Name: "symptom", Description: "Short description of the incident or question.", Required: false},
			},
		},
		{
			Name:        "kube_insight_event_history",
			Description: "Investigate retained Kubernetes Events and follow Event edges to affected resources.",
			Arguments: []*sdkmcp.PromptArgument{
				{Name: "cluster", Description: "Optional kube-insight cluster name.", Required: false},
				{Name: "reason", Description: "Optional Event reason such as PolicyViolation or FailedScheduling.", Required: false},
				{Name: "keyword", Description: "Optional lowercase keyword to search in message previews.", Required: false},
			},
		},
		{
			Name:        "kube_insight_object_history",
			Description: "Inspect one object's retained versions, observations, and diffs as proof.",
			Arguments: []*sdkmcp.PromptArgument{
				{Name: "cluster", Description: "Optional kube-insight cluster name.", Required: false},
				{Name: "kind", Description: "Kubernetes Kind.", Required: false},
				{Name: "namespace", Description: "Namespace for namespaced resources.", Required: false},
				{Name: "name", Description: "Object name.", Required: false},
				{Name: "uid", Description: "Object UID when known.", Required: false},
			},
		},
	}
}

func promptResult(_ context.Context, request *sdkmcp.GetPromptRequest) (*sdkmcp.GetPromptResult, error) {
	text, description, err := promptText(request.Params.Name, request.Params.Arguments)
	if err != nil {
		return nil, err
	}
	return &sdkmcp.GetPromptResult{
		Description: description,
		Messages: []*sdkmcp.PromptMessage{
			{
				Role:    "user",
				Content: &sdkmcp.TextContent{Text: text},
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

Default to SQL after schema detection. Typed tools such as kube_insight_health and kube_insight_history are guardrails and packaged summaries; use kube_insight_sql for discovery, ranking candidates, topology expansion, and proof queries.

Use this order:
1. Call kube_insight_schema and read the backend notes before writing SQL; SQLite and ClickHouse-compatible backends use different table names and timestamp expressions.
2. Check collector coverage for %s. For ClickHouse-compatible backends, ingestion_offsets is append-only, so collapse current state with argMax(status, updated_at), argMax(error, updated_at), and max(updated_at) before judging health. kube_insight_health may be used as a summary.
3. List clusters with the cluster query that matches the returned schema. For SQLite use clusters; for ClickHouse-compatible backends use versions/facts/edges and the cluster_id string already stored in evidence rows.
4. Pick the relevant cluster id and keep cluster_id in follow-up SQL.
5. Query facts and changes for candidate resources. Prefer exact fact_key/fact_value, kind, severity, and object_id predicates before broad text search. For Service exposure issues, start with service.load_balancer.pending and service.load_balancer.ingress_ip facts before opening retained Service versions.
6. Query edges with src_id or dst_id around candidates to expand topology.
7. Query observations and versions for retained proof. Use kube_insight_history only for final candidate objects or when packaged diffs are clearer than raw SQL.

Do not claim absence unless collector coverage is healthy for the resource types involved.`, symptom, cluster), "Coverage-first kube-insight investigation", nil
	case "kube_insight_event_history":
		cluster := promptArg(args, "cluster", "the relevant cluster")
		reason := promptArg(args, "reason", "the Event reason")
		keyword := promptArg(args, "keyword", "the message keyword")
		return fmt.Sprintf(`Investigate retained Kubernetes Events in %s.

Use kube_insight_schema first, then use kube_insight_sql as the primary interface with SQL that matches the active backend:
1. Identify whether schema notes say SQLite or ClickHouse-compatible.
2. Check current collector coverage for Event and affected-resource types. For ClickHouse-compatible backends, collapse append-only ingestion_offsets with argMax(status, updated_at) before trusting current status.
3. Select a cluster_id using available schema rows or evidence rows.
4. Query Event facts directly. SQLite uses object_facts; ClickHouse-compatible backends use facts. Count Warning Events by k8s_event.reason, narrowing to %s when provided.
5. Search k8s_event.message_preview for %s only after the reason query is scoped by cluster_id.
6. Follow Event relationship edges. SQLite uses object_edges; ClickHouse-compatible backends use edges. Look for event_regarding_object, event_related_object, or event_involves_object to identify affected resources.
7. Query changes, observations, and versions for affected object_ids before making a claim.
8. Fetch kube_insight_history for the affected resource and the Event only when packaged proof or diffs are needed.

Compare retained Event history with current kubectl only as separate evidence; kubectl shows live apiserver state, not the retained window.`, cluster, reason, keyword), "Retained Event history investigation", nil
	case "kube_insight_object_history":
		target := promptObjectTarget(args)
		return fmt.Sprintf(`Inspect object history for %s.

Use kube_insight_schema first and treat kube_insight_sql as the primary investigation interface. Detect whether the active backend exposes SQLite tables such as object_facts/object_edges/object_observations/latest_index or ClickHouse-compatible tables such as facts/edges/changes/observations/versions.

Start with SQL:
1. Check coverage for the object's resource type; for ClickHouse-compatible backends, collapse append-only ingestion_offsets with argMax(status, updated_at).
2. Locate the object by the most specific identifier available, preferring uid when known.
3. Query facts and changes for the object_id to explain why it matters.
4. Query edges where the object is src_id or dst_id to find related causes and dependents.
5. Query observations and versions for proof timestamps, resource versions, doc_hash values, and retained documents when needed.

Use kube_insight_history after SQL has identified the object. Include diffs and keep maxVersions/maxObservations bounded at first.

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

func unmarshalToolArguments(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	return json.Unmarshal(raw, target)
}

type nopReadCloser struct {
	io.Reader
}

func (nopReadCloser) Close() error {
	return nil
}

type nopWriteCloser struct {
	io.Writer
}

func (nopWriteCloser) Close() error {
	return nil
}

func writeHTTPJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
