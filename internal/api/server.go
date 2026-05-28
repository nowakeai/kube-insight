package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"kube-insight/internal/agent"
	"kube-insight/internal/storage"
	"kube-insight/internal/storage/sqlite"
)

type ServerOptions struct {
	DBPath                   string
	OpenStore                StoreOpener
	KeepStoreOpen            bool
	Close                    func() error
	AgentStore               agent.Store
	AgentRunner              AgentRunner
	AgentRetentionInterval   time.Duration
	AgentRetentionRunOnStart bool
	ServerInfo               ServerInfo
}

type StoreOpener func(context.Context) (ReadStore, error)

type AgentRunner interface {
	Run(context.Context, agent.EinoRunInput) (agent.EinoRunResult, error)
}

type ReadStore interface {
	Close() error
}

type Server struct {
	dbPath               string
	openStore            StoreOpener
	closeStoreOnRequest  bool
	closeFunc            func() error
	agentStore           agent.Store
	agentRunner          AgentRunner
	agentRunMu           sync.Mutex
	agentRunCancels      map[string]context.CancelFunc
	agentRunWG           sync.WaitGroup
	agentRetentionMu     sync.Mutex
	agentRetentionCancel context.CancelFunc
	agentRetentionDone   chan struct{}
	serverInfo           ServerInfo
	mux                  *http.ServeMux
}

const agentRunShutdownWait = 5 * time.Second

type sqlRequest struct {
	SQL     string `json:"sql"`
	MaxRows int    `json:"maxRows"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func NewServer(opts ServerOptions) (*Server, error) {
	openStore := opts.OpenStore
	if openStore == nil {
		if opts.DBPath == "" {
			return nil, errors.New("api server requires a read store or sqlite database path")
		}
		openStore = sqliteStoreOpener(opts.DBPath)
	}
	agentStore := opts.AgentStore
	closeFunc := opts.Close
	if agentStore == nil && opts.DBPath != "" {
		persistentAgentStore, err := sqlite.Open(opts.DBPath)
		if err != nil {
			return nil, err
		}
		agentStore = persistentAgentStore
		closeFunc = joinClose(closeFunc, persistentAgentStore.Close)
	}
	if agentStore == nil {
		agentStore = agent.NewMemoryStore()
	}
	s := &Server{dbPath: opts.DBPath, openStore: openStore, closeStoreOnRequest: !opts.KeepStoreOpen, closeFunc: closeFunc, agentStore: agentStore, agentRunner: opts.AgentRunner, agentRunCancels: map[string]context.CancelFunc{}, serverInfo: normalizeServerInfo(opts.ServerInfo, opts.DBPath), mux: http.NewServeMux()}
	s.routes()
	s.recoverInterruptedAgentRuns(context.Background())
	s.startAgentRetentionLoop(opts.AgentRetentionInterval, opts.AgentRetentionRunOnStart)
	return s, nil
}

func sqliteStoreOpener(path string) StoreOpener {
	return func(context.Context) (ReadStore, error) {
		return sqlite.OpenReadOnly(path)
	}
}

func joinClose(first, second func() error) func() error {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	return func() error {
		return errors.Join(first(), second())
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) MountHTTP(mux *http.ServeMux) {
	mux.Handle("/api/", s)
}

func (s *Server) Close() error {
	s.cancelAgentRuns()
	s.waitAgentRuns(agentRunShutdownWait)
	s.stopAgentRetentionLoop()
	if s.closeFunc == nil {
		return nil
	}
	return s.closeFunc()
}

func (s *Server) closeReadStore(store ReadStore) {
	if s.closeStoreOnRequest && store != nil {
		_ = store.Close()
	}
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /api/v1/schema", s.handleSchema)
	s.mux.HandleFunc("GET /api/v1/storage/stats", s.handleStorageStats)
	s.mux.HandleFunc("GET /api/v1/server/info", s.handleServerInfo)
	s.mux.HandleFunc("POST /api/v1/sql", s.handleSQL)
	s.mux.HandleFunc("GET /api/v1/health", s.handleResourceHealth)
	s.mux.HandleFunc("GET /api/v1/history", s.handleHistory)
	s.mux.HandleFunc("GET /api/v1/search", s.handleSearch)
	s.mux.HandleFunc("GET /api/v1/services/{namespace}/{name}/investigation", s.handleServiceInvestigation)
	s.mux.HandleFunc("GET /api/v1/topology", s.handleTopology)
	s.mux.HandleFunc("POST /api/v1/agent/retention/compact", s.handleCompactAgentRetention)
	s.mux.HandleFunc("POST /api/v1/agent/sessions", s.handleCreateAgentSession)
	s.mux.HandleFunc("GET /api/v1/agent/sessions", s.handleListAgentSessions)
	s.mux.HandleFunc("GET /api/v1/agent/sessions/{session_id}", s.handleGetAgentSession)
	s.mux.HandleFunc("DELETE /api/v1/agent/sessions/{session_id}", s.handleDeleteAgentSession)
	s.mux.HandleFunc("POST /api/v1/agent/sessions/{session_id}/runs", s.handleCreateAgentRun)
	s.mux.HandleFunc("GET /api/v1/agent/runs", s.handleListAgentRuns)
	s.mux.HandleFunc("GET /api/v1/agent/runs/{run_id}/events", s.handleAgentRunEvents)
	s.mux.HandleFunc("POST /api/v1/agent/runs/{run_id}/cancel", s.handleCancelAgentRun)
	s.mux.HandleFunc("POST /api/v1/agent/runs/{run_id}/retry", s.handleRetryAgentRun)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleServerInfo(w http.ResponseWriter, r *http.Request) {
	info := s.serverInfo
	info.CheckedAt = time.Now().UTC()
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleSchema(w http.ResponseWriter, r *http.Request) {
	store, err := s.openStore(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer s.closeReadStore(store)
	queryStore, ok := store.(storage.SQLQueryStore)
	if !ok {
		writeUnsupported(w, "schema queries")
		return
	}
	schema, err := queryStore.QuerySchema(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, schema)
}

func (s *Server) handleSQL(w http.ResponseWriter, r *http.Request) {
	var input sqlRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid json body: %w", err))
		return
	}
	store, err := s.openStore(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer s.closeReadStore(store)
	queryStore, ok := store.(storage.SQLQueryStore)
	if !ok {
		writeUnsupported(w, "SQL queries")
		return
	}
	result, err := queryStore.QuerySQL(r.Context(), storage.SQLQueryOptions{
		SQL:     input.SQL,
		MaxRows: input.MaxRows,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleResourceHealth(w http.ResponseWriter, r *http.Request) {
	opts, err := parseResourceHealthOptions(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	store, err := s.openStore(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer s.closeReadStore(store)
	healthStore, ok := store.(storage.ResourceHealthStore)
	if !ok {
		writeUnsupported(w, "resource health")
		return
	}
	report, err := healthStore.ResourceHealth(r.Context(), opts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if resourceHealthDetail(r) == "full" {
		writeJSON(w, http.StatusOK, report)
		return
	}
	writeJSON(w, http.StatusOK, compactResourceHealthReport(report, compactResourceHealthLimit(r)))
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	target, opts, err := parseHistoryRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	store, err := s.openStore(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer s.closeReadStore(store)
	historyStore, ok := store.(storage.ObjectHistoryStore)
	if !ok {
		writeUnsupported(w, "object history")
		return
	}
	history, err := historyStore.ObjectHistory(r.Context(), target, opts)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, history)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	opts, err := parseSearchRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	store, err := s.openStore(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer s.closeReadStore(store)
	searchStore, ok := store.(storage.EvidenceSearchStore)
	if !ok {
		writeUnsupported(w, "evidence search")
		return
	}
	result, err := searchStore.SearchEvidence(r.Context(), opts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleServiceInvestigation(w http.ResponseWriter, r *http.Request) {
	target, opts, err := parseServiceInvestigationRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	store, err := s.openStore(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer s.closeReadStore(store)
	serviceStore, ok := store.(storage.ServiceInvestigationStore)
	if !ok {
		writeUnsupported(w, "service investigation")
		return
	}
	result, err := serviceStore.InvestigateServiceWithOptions(r.Context(), target, opts)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleTopology(w http.ResponseWriter, r *http.Request) {
	target := parseObjectTarget(r)
	store, err := s.openStore(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer s.closeReadStore(store)
	topologyStore, ok := store.(storage.TopologyStore)
	if !ok {
		writeUnsupported(w, "topology")
		return
	}
	graph, err := topologyStore.Topology(r.Context(), target)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, graph)
}

func ListenAndServe(ctx context.Context, listen string, opts ServerOptions) error {
	if listen == "" {
		listen = "127.0.0.1:8080"
	}
	handler, err := NewServer(opts)
	if err != nil {
		return err
	}
	defer handler.Close()
	server := &http.Server{
		Addr:              listen,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	done := make(chan error, 1)
	go func() {
		done <- server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
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

type compactResourceHealthResponse struct {
	CheckedAt        time.Time                      `json:"checkedAt"`
	Detail           string                         `json:"detail"`
	Summary          storage.ResourceHealthSummary  `json:"summary"`
	ByStatus         map[string]int                 `json:"byStatus"`
	Resources        []storage.ResourceHealthRecord `json:"resources"`
	ResourcesOmitted int                            `json:"resourcesOmitted,omitempty"`
}

func compactResourceHealthReport(report storage.ResourceHealthReport, limit int) compactResourceHealthResponse {
	if limit <= 0 {
		limit = 10
	}
	resources := make([]storage.ResourceHealthRecord, 0, limit)
	omitted := 0
	for _, record := range report.Resources {
		if !isProblemResourceHealthRecord(record) {
			continue
		}
		if len(resources) >= limit {
			omitted++
			continue
		}
		resources = append(resources, compactResourceHealthRecord(record))
	}
	return compactResourceHealthResponse{
		CheckedAt:        report.CheckedAt,
		Detail:           "compact",
		Summary:          report.Summary,
		ByStatus:         report.ByStatus,
		Resources:        resources,
		ResourcesOmitted: omitted,
	}
}

func resourceHealthDetail(r *http.Request) string {
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get("detail"))) {
	case "full":
		return "full"
	default:
		return "compact"
	}
}

func compactResourceHealthLimit(r *http.Request) int {
	value := strings.TrimSpace(r.URL.Query().Get("problemLimit"))
	if value == "" {
		value = strings.TrimSpace(r.URL.Query().Get("limit"))
	}
	if value == "" {
		return 10
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit <= 0 {
		return 10
	}
	if limit > 100 {
		return 100
	}
	return limit
}

func compactResourceHealthRecord(record storage.ResourceHealthRecord) storage.ResourceHealthRecord {
	record.Error = oneLineLimit(record.Error, 180)
	return record
}

func isProblemResourceHealthRecord(record storage.ResourceHealthRecord) bool {
	if record.Error != "" || record.Stale || record.Skipped {
		return true
	}
	switch record.Status {
	case "watching", "bookmark", "queued":
		return false
	default:
		return true
	}
}

func oneLineLimit(value string, maxRunes int) string {
	line := strings.Join(strings.Fields(value), " ")
	if maxRunes <= 0 {
		return line
	}
	runes := []rune(line)
	if len(runes) <= maxRunes {
		return line
	}
	if maxRunes <= 1 {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-1]) + "..."
}

func parseResourceHealthOptions(r *http.Request) (storage.ResourceHealthOptions, error) {
	query := r.URL.Query()
	opts := storage.ResourceHealthOptions{
		ClusterID: query.Get("cluster"),
		Status:    query.Get("status"),
	}
	if query.Get("errorsOnly") != "" {
		value, err := strconv.ParseBool(query.Get("errorsOnly"))
		if err != nil {
			return opts, fmt.Errorf("errorsOnly: %w", err)
		}
		opts.ErrorsOnly = value
	}
	if query.Get("includeSkipped") != "" {
		value, err := strconv.ParseBool(query.Get("includeSkipped"))
		if err != nil {
			return opts, fmt.Errorf("includeSkipped: %w", err)
		}
		opts.IncludeExcluded = value
	}
	for _, value := range query["exclude"] {
		opts.ExcludeResources = append(opts.ExcludeResources, splitCommaValues(value)...)
	}
	if query.Get("limit") != "" {
		value, err := strconv.Atoi(query.Get("limit"))
		if err != nil {
			return opts, fmt.Errorf("limit: %w", err)
		}
		opts.Limit = value
	}
	if query.Get("staleAfter") != "" {
		value, err := time.ParseDuration(query.Get("staleAfter"))
		if err != nil {
			return opts, fmt.Errorf("staleAfter: %w", err)
		}
		opts.StaleAfter = value
	}
	return opts, nil
}

func splitCommaValues(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func parseHistoryRequest(r *http.Request) (storage.ObjectTarget, storage.ObjectHistoryOptions, error) {
	query := r.URL.Query()
	target := parseObjectTarget(r)
	opts := storage.ObjectHistoryOptions{IncludeDiffs: true}
	var err error
	if query.Get("from") != "" {
		opts.From, err = parseHistoryTime(query.Get("from"))
		if err != nil {
			return target, opts, fmt.Errorf("from: %w", err)
		}
	}
	if query.Get("to") != "" {
		opts.To, err = parseHistoryTime(query.Get("to"))
		if err != nil {
			return target, opts, fmt.Errorf("to: %w", err)
		}
	}
	if !opts.From.IsZero() && !opts.To.IsZero() && opts.From.After(opts.To) {
		return target, opts, fmt.Errorf("from must be before to")
	}
	if query.Get("maxVersions") != "" {
		opts.MaxVersions, err = strconv.Atoi(query.Get("maxVersions"))
		if err != nil {
			return target, opts, fmt.Errorf("maxVersions: %w", err)
		}
	}
	if query.Get("maxObservations") != "" {
		opts.MaxObservations, err = strconv.Atoi(query.Get("maxObservations"))
		if err != nil {
			return target, opts, fmt.Errorf("maxObservations: %w", err)
		}
	}
	if query.Get("includeDocs") != "" {
		opts.IncludeDocs, err = strconv.ParseBool(query.Get("includeDocs"))
		if err != nil {
			return target, opts, fmt.Errorf("includeDocs: %w", err)
		}
	}
	if query.Get("diffs") != "" {
		opts.IncludeDiffs, err = strconv.ParseBool(query.Get("diffs"))
		if err != nil {
			return target, opts, fmt.Errorf("diffs: %w", err)
		}
	}
	return target, opts, nil
}

func parseSearchRequest(r *http.Request) (storage.EvidenceSearchOptions, error) {
	query := r.URL.Query()
	opts := storage.EvidenceSearchOptions{
		Query:         firstNonEmpty(query.Get("q"), query.Get("query")),
		ClusterID:     query.Get("cluster"),
		Kind:          query.Get("kind"),
		Namespace:     query.Get("namespace"),
		IncludeHealth: true,
	}
	var err error
	if query.Get("from") != "" {
		opts.From, err = parseHistoryTime(query.Get("from"))
		if err != nil {
			return opts, fmt.Errorf("from: %w", err)
		}
	}
	if query.Get("to") != "" {
		opts.To, err = parseHistoryTime(query.Get("to"))
		if err != nil {
			return opts, fmt.Errorf("to: %w", err)
		}
	}
	if !opts.From.IsZero() && !opts.To.IsZero() && opts.From.After(opts.To) {
		return opts, fmt.Errorf("from must be before to")
	}
	if query.Get("limit") != "" {
		opts.Limit, err = strconv.Atoi(query.Get("limit"))
		if err != nil {
			return opts, fmt.Errorf("limit: %w", err)
		}
	}
	if query.Get("maxVersionsPerObject") != "" {
		opts.MaxVersionsPerObject, err = strconv.Atoi(query.Get("maxVersionsPerObject"))
		if err != nil {
			return opts, fmt.Errorf("maxVersionsPerObject: %w", err)
		}
	}
	if query.Get("includeBundles") != "" {
		opts.IncludeBundles, err = strconv.ParseBool(query.Get("includeBundles"))
		if err != nil {
			return opts, fmt.Errorf("includeBundles: %w", err)
		}
	}
	if query.Get("includeHealth") != "" {
		opts.IncludeHealth, err = strconv.ParseBool(query.Get("includeHealth"))
		if err != nil {
			return opts, fmt.Errorf("includeHealth: %w", err)
		}
	}
	if query.Get("healthStaleAfter") != "" {
		opts.HealthStaleAfter, err = time.ParseDuration(query.Get("healthStaleAfter"))
		if err != nil {
			return opts, fmt.Errorf("healthStaleAfter: %w", err)
		}
	}
	return opts, nil
}

func parseServiceInvestigationRequest(r *http.Request) (storage.ObjectTarget, storage.InvestigationOptions, error) {
	query := r.URL.Query()
	target := storage.ObjectTarget{
		ClusterID: query.Get("cluster"),
		Kind:      "Service",
		Namespace: r.PathValue("namespace"),
		Name:      r.PathValue("name"),
	}
	opts, err := parseInvestigationOptions(query.Get("from"), query.Get("to"))
	if err != nil {
		return target, opts, err
	}
	if value := firstNonEmpty(query.Get("maxEvidenceObjects"), query.Get("limit")); value != "" {
		opts.MaxEvidenceObjects, err = strconv.Atoi(value)
		if err != nil {
			return target, opts, fmt.Errorf("maxEvidenceObjects: %w", err)
		}
	}
	if query.Get("maxVersionsPerObject") != "" {
		opts.MaxVersionsPerObject, err = strconv.Atoi(query.Get("maxVersionsPerObject"))
		if err != nil {
			return target, opts, fmt.Errorf("maxVersionsPerObject: %w", err)
		}
	}
	return target, opts, nil
}

func parseInvestigationOptions(from, to string) (storage.InvestigationOptions, error) {
	var opts storage.InvestigationOptions
	var err error
	if from != "" {
		opts.From, err = parseHistoryTime(from)
		if err != nil {
			return opts, fmt.Errorf("from: %w", err)
		}
	}
	if to != "" {
		opts.To, err = parseHistoryTime(to)
		if err != nil {
			return opts, fmt.Errorf("to: %w", err)
		}
	}
	if !opts.From.IsZero() && !opts.To.IsZero() && opts.From.After(opts.To) {
		return opts, fmt.Errorf("from must be before to")
	}
	return opts, nil
}

func parseObjectTarget(r *http.Request) storage.ObjectTarget {
	query := r.URL.Query()
	return storage.ObjectTarget{
		ClusterID: query.Get("cluster"),
		UID:       query.Get("uid"),
		Kind:      query.Get("kind"),
		Namespace: query.Get("namespace"),
		Name:      query.Get("name"),
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func parseHistoryTime(value string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02", value)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(value)
}

func writeUnsupported(w http.ResponseWriter, feature string) {
	writeError(w, http.StatusNotImplemented, fmt.Errorf("%s is not supported by this storage backend", feature))
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, errorResponse{Error: err.Error()})
}
