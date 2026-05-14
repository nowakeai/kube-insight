package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

type sqlArguments struct {
	SQL     string `json:"sql"`
	MaxRows int    `json:"maxRows,omitempty"`
}

type healthArguments struct {
	ClusterID  string `json:"cluster,omitempty"`
	Status     string `json:"status,omitempty"`
	ErrorsOnly bool   `json:"errorsOnly,omitempty"`
	StaleAfter string `json:"staleAfter,omitempty"`
	Limit      int    `json:"limit,omitempty"`
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

func (s *Server) ServeStdio(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	encoder := json.NewEncoder(out)
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
		response := s.handleLine(ctx, line)
		if response == nil {
			continue
		}
		if err := encoder.Encode(response); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
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
				"tools": map[string]any{},
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
		ClusterID:  args.ClusterID,
		Status:     args.Status,
		ErrorsOnly: args.ErrorsOnly,
		Limit:      args.Limit,
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
				},
			},
		},
	}
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

func rawID(raw json.RawMessage) any {
	var id any
	if err := json.Unmarshal(raw, &id); err != nil {
		return nil
	}
	return id
}
