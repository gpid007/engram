package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	mcpmcp "github.com/mark3labs/mcp-go/mcp"

	"github.com/gregdhill/engram/internal/memory"
)

// --------------------------------------------------------------------------
// Uniform error envelope
// --------------------------------------------------------------------------

type mcpError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

type mcpErrorEnvelope struct {
	Error mcpError `json:"error"`
}

func errorResult(code string, msg string, retryable bool) *mcpmcp.CallToolResult {
	env := mcpErrorEnvelope{Error: mcpError{Code: code, Message: msg, Retryable: retryable}}
	b, _ := json.Marshal(env)
	return mcpmcp.NewToolResultError(string(b))
}

// --------------------------------------------------------------------------
// Tool registration
// --------------------------------------------------------------------------

func registerTools(s *Server) {
	// store_memory
	storeMemoryTool := mcpmcp.NewTool("store_memory",
		mcpmcp.WithDescription("Ingest content into Engram memory with optional user, source, and metadata."),
		mcpmcp.WithString("content",
			mcpmcp.Required(),
			mcpmcp.Description("The content to store."),
		),
		mcpmcp.WithString("user_id",
			mcpmcp.Description("User namespace (default: \"default\")."),
		),
		mcpmcp.WithString("source",
			mcpmcp.Description("Origin label (e.g. filename, URL)."),
		),
		mcpmcp.WithObject("metadata",
			mcpmcp.Description("Arbitrary JSON metadata object."),
		),
	)
	s.srv.AddTool(storeMemoryTool, s.handleStoreMemory)

	// retrieve_context
	retrieveContextTool := mcpmcp.NewTool("retrieve_context",
		mcpmcp.WithDescription("Retrieve relevant memory chunks via hybrid search (vector + BM25 + optional rerank)."),
		mcpmcp.WithString("query",
			mcpmcp.Required(),
			mcpmcp.Description("The search query."),
		),
		mcpmcp.WithString("user_id",
			mcpmcp.Description("User namespace (default: \"default\")."),
		),
		mcpmcp.WithNumber("k",
			mcpmcp.Description("Number of results to return (default: 5)."),
		),
		mcpmcp.WithBoolean("rerank",
			mcpmcp.Description("Whether to apply reranking (default: false)."),
		),
	)
	s.srv.AddTool(retrieveContextTool, s.handleRetrieveContext)

	// get_user_state
	getUserStateTool := mcpmcp.NewTool("get_user_state",
		mcpmcp.WithDescription("Return aggregate memory statistics for a user."),
		mcpmcp.WithString("user_id",
			mcpmcp.Description("User namespace (default: \"default\")."),
		),
	)
	s.srv.AddTool(getUserStateTool, s.handleGetUserState)
}

// --------------------------------------------------------------------------
// store_memory handler
// --------------------------------------------------------------------------

func (s *Server) handleStoreMemory(ctx context.Context, req mcpmcp.CallToolRequest) (*mcpmcp.CallToolResult, error) {
	content, err := req.RequireString("content")
	if err != nil {
		return errorResult("invalid_input", err.Error(), false), nil
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return errorResult("invalid_input", "content must not be empty", false), nil
	}

	userID := req.GetString("user_id", "")
	source := req.GetString("source", "")

	// metadata is an optional object; extract it from raw arguments.
	var metadata map[string]any
	if args := req.GetArguments(); args != nil {
		if raw, ok := args["metadata"]; ok && raw != nil {
			switch v := raw.(type) {
			case map[string]any:
				metadata = v
			default:
				return errorResult("invalid_input", "metadata must be a JSON object", false), nil
			}
		}
	}

	result, err := s.ingestor.Store(ctx, memory.StoreInput{
		Content:  content,
		UserID:   userID,
		Source:   source,
		Metadata: metadata,
	})
	if err != nil {
		// Distinguish embedding errors from storage errors by message heuristic.
		code := "storage_failed"
		if isEmbedError(err) {
			code = "embedding_failed"
		}
		return errorResult(code, err.Error(), true), nil
	}

	type storeOut struct {
		MemoryID      string `json:"memory_id"`
		ChunksStored  int    `json:"chunks_stored"`
		ChunksDeduped int    `json:"chunks_deduped"`
		Stored        bool   `json:"stored"`
	}
	out := storeOut{
		MemoryID:      result.MemoryID,
		ChunksStored:  result.ChunksStored,
		ChunksDeduped: result.ChunksDeduped,
		Stored:        result.Stored,
	}
	b, _ := json.Marshal(out)
	return mcpmcp.NewToolResultText(string(b)), nil
}

// --------------------------------------------------------------------------
// retrieve_context handler
// --------------------------------------------------------------------------

func (s *Server) handleRetrieveContext(ctx context.Context, req mcpmcp.CallToolRequest) (*mcpmcp.CallToolResult, error) {
	query, err := req.RequireString("query")
	if err != nil {
		return errorResult("invalid_input", err.Error(), false), nil
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return errorResult("invalid_input", "query must not be empty", false), nil
	}

	userID := req.GetString("user_id", "")
	k := req.GetInt("k", 0)
	rerank := getBool(req, "rerank", false)

	resp, err := s.retriever.Retrieve(ctx, memory.RetrieveInput{
		Query:  query,
		UserID: userID,
		K:      k,
		Rerank: rerank,
	})
	if err != nil {
		code := "retrieval_failed"
		if isEmbedError(err) {
			code = "embedding_failed"
		}
		return errorResult(code, err.Error(), true), nil
	}

	type resultItem struct {
		MemoryID  string  `json:"memory_id"`
		ChunkID   string  `json:"chunk_id"`
		Content   string  `json:"content"`
		Score     float64 `json:"score"`
		Source    string  `json:"source"`
		CreatedAt string  `json:"created_at"`
	}
	type statsOut struct {
		VecMs         int64 `json:"vec_ms"`
		BM25Ms        int64 `json:"bm25_ms"`
		FusionMs      int64 `json:"fusion_ms"`
		RerankMs      int64 `json:"rerank_ms"`
		TotalMs       int64 `json:"total_ms"`
		RerankSkipped bool  `json:"rerank_skipped"`
		Degraded      bool  `json:"degraded"`
	}
	type retrieveOut struct {
		Results []resultItem `json:"results"`
		Stats   statsOut     `json:"stats"`
	}

	items := make([]resultItem, len(resp.Results))
	for i, r := range resp.Results {
		items[i] = resultItem{
			MemoryID:  r.MemoryID,
			ChunkID:   r.ChunkID,
			Content:   r.Content,
			Score:     r.Score,
			Source:    r.Source,
			CreatedAt: r.CreatedAt,
		}
	}
	out := retrieveOut{
		Results: items,
		Stats: statsOut{
			VecMs:         resp.Stats.VecMs,
			BM25Ms:        resp.Stats.BM25Ms,
			FusionMs:      resp.Stats.FusionMs,
			RerankMs:      resp.Stats.RerankMs,
			TotalMs:       resp.Stats.TotalMs,
			RerankSkipped: resp.Stats.RerankSkipped,
			Degraded:      resp.Stats.Degraded,
		},
	}
	b, _ := json.Marshal(out)
	return mcpmcp.NewToolResultText(string(b)), nil
}

// --------------------------------------------------------------------------
// get_user_state handler
// --------------------------------------------------------------------------

func (s *Server) handleGetUserState(ctx context.Context, req mcpmcp.CallToolRequest) (*mcpmcp.CallToolResult, error) {
	userID := req.GetString("user_id", "default")
	if strings.TrimSpace(userID) == "" {
		userID = "default"
	}

	state, err := s.meta.GetUserState(ctx, userID)
	if err != nil {
		return errorResult("retrieval_failed", fmt.Sprintf("get user state: %s", err), true), nil
	}

	type userStateOut struct {
		MemoryCount int      `json:"memory_count"`
		ChunkCount  int      `json:"chunk_count"`
		FirstMemory *string  `json:"first_memory,omitempty"`
		LastMemory  *string  `json:"last_memory,omitempty"`
		TopSources  []string `json:"top_sources"`
	}

	out := userStateOut{
		MemoryCount: state.MemoryCount,
		ChunkCount:  state.ChunkCount,
		TopSources:  state.TopSources,
	}
	if state.FirstMemory != nil {
		s := state.FirstMemory.Format("2006-01-02T15:04:05Z07:00")
		out.FirstMemory = &s
	}
	if state.LastMemory != nil {
		s := state.LastMemory.Format("2006-01-02T15:04:05Z07:00")
		out.LastMemory = &s
	}
	if out.TopSources == nil {
		out.TopSources = []string{}
	}

	b, _ := json.Marshal(out)
	return mcpmcp.NewToolResultText(string(b)), nil
}

// --------------------------------------------------------------------------
// Helpers
// --------------------------------------------------------------------------

// isEmbedError returns true when the error message suggests an embedding failure.
// The chunker/embedder wraps errors with "embed" in the message.
func isEmbedError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "embed") || strings.Contains(msg, "Embed")
}

// getBool returns a bool argument by key from a CallToolRequest,
// or the default value if not found. It handles JSON float64 (0/1) as well as
// native bool.
func getBool(req mcpmcp.CallToolRequest, key string, defaultValue bool) bool {
	args := req.GetArguments()
	if args == nil {
		return defaultValue
	}
	val, ok := args[key]
	if !ok || val == nil {
		return defaultValue
	}
	switch v := val.(type) {
	case bool:
		return v
	case float64:
		return v != 0
	}
	return defaultValue
}


