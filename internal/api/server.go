package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"kube-insight/internal/storage/sqlite"
)

type ServerOptions struct {
	DBPath string
}

type Server struct {
	dbPath string
	mux    *http.ServeMux
}

type sqlRequest struct {
	SQL     string `json:"sql"`
	MaxRows int    `json:"maxRows"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func NewServer(opts ServerOptions) (*Server, error) {
	if opts.DBPath == "" {
		return nil, errors.New("api server requires a sqlite database path")
	}
	s := &Server{dbPath: opts.DBPath, mux: http.NewServeMux()}
	s.routes()
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /api/v1/schema", s.handleSchema)
	s.mux.HandleFunc("POST /api/v1/sql", s.handleSQL)
	s.mux.HandleFunc("GET /api/v1/health", s.handleResourceHealth)
	s.mux.HandleFunc("GET /api/v1/history", s.handleHistory)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleSchema(w http.ResponseWriter, r *http.Request) {
	store, err := sqlite.OpenReadOnly(s.dbPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer store.Close()
	schema, err := store.QuerySchema(r.Context())
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
	store, err := sqlite.OpenReadOnly(s.dbPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer store.Close()
	result, err := store.QuerySQL(r.Context(), sqlite.SQLQueryOptions{
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
	store, err := sqlite.OpenReadOnly(s.dbPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer store.Close()
	report, err := store.ResourceHealth(r.Context(), opts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	target, opts, err := parseHistoryRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	store, err := sqlite.OpenReadOnly(s.dbPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer store.Close()
	history, err := store.ObjectHistory(r.Context(), target, opts)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, history)
}

func ListenAndServe(ctx context.Context, listen string, opts ServerOptions) error {
	if listen == "" {
		listen = "127.0.0.1:8080"
	}
	handler, err := NewServer(opts)
	if err != nil {
		return err
	}
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

func parseResourceHealthOptions(r *http.Request) (sqlite.ResourceHealthOptions, error) {
	query := r.URL.Query()
	opts := sqlite.ResourceHealthOptions{
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

func parseHistoryRequest(r *http.Request) (sqlite.ObjectTarget, sqlite.ObjectHistoryOptions, error) {
	query := r.URL.Query()
	target := sqlite.ObjectTarget{
		ClusterID: query.Get("cluster"),
		UID:       query.Get("uid"),
		Kind:      query.Get("kind"),
		Namespace: query.Get("namespace"),
		Name:      query.Get("name"),
	}
	opts := sqlite.ObjectHistoryOptions{IncludeDiffs: true}
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

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, errorResponse{Error: err.Error()})
}
