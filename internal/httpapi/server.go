// Package httpapi implements the HTTP transport.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gregdhill/engram/internal/memory"
	"github.com/gregdhill/engram/internal/store/postgres"
)

// Server is the HTTP transport for Engram.
type Server struct {
	ingestor   *memory.Ingestor
	retriever  *memory.Retriever
	meta       postgres.MetaStore
	checkVec   func(ctx context.Context) error
	checkEmbed func(ctx context.Context) error
	addr       string
	mux        *http.ServeMux
	metrics    *Metrics
}

// NewServer creates an HTTP server with the given dependencies.
func NewServer(
	addr string,
	ingestor *memory.Ingestor,
	retriever *memory.Retriever,
	meta postgres.MetaStore,
	checkVec func(ctx context.Context) error,
	checkEmbed func(ctx context.Context) error,
) *Server {
	s := &Server{
		ingestor:   ingestor,
		retriever:  retriever,
		meta:       meta,
		checkVec:   checkVec,
		checkEmbed: checkEmbed,
		addr:       addr,
		metrics:    NewMetrics(),
	}
	s.mux = s.routes()
	return s
}

func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("POST /v1/memories", s.wrap(http.HandlerFunc(s.handleStoreMemory)))
	mux.Handle("DELETE /v1/memories/{id}", s.wrap(http.HandlerFunc(s.handleDeleteMemory)))
	mux.Handle("POST /v1/retrieve", s.wrap(http.HandlerFunc(s.handleRetrieveContext)))
	mux.Handle("GET /v1/users/", s.wrap(http.HandlerFunc(s.handleGetUserState)))
	mux.Handle("GET /healthz", http.HandlerFunc(s.handleHealthz))
	mux.Handle("GET /readyz", http.HandlerFunc(s.handleReadyz))
	mux.Handle("GET /metrics", s.metrics.Handler())
	return mux
}

// ListenAndServe starts the HTTP server, shutting down gracefully when ctx is done.
func (s *Server) ListenAndServe(ctx context.Context) error {
	srv := &http.Server{Addr: s.addr, Handler: s.mux}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	case err := <-errCh:
		return err
	}
}

// ServeHTTP implements http.Handler so httptest.NewServer works directly.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// wrap applies logging + recover middleware.
func (s *Server) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic in handler", "recover", rec, "path", r.URL.Path)
				writeError(w, http.StatusInternalServerError, "internal_error", "internal server error", false)
			}
		}()
		next.ServeHTTP(rw, r)
		slog.Info("request", "method", r.Method, "path", r.URL.Path, "status", rw.code, "dur_ms", time.Since(start).Milliseconds())
	})
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.code = code
	sw.ResponseWriter.WriteHeader(code)
}

// --- Handlers ---

func (s *Server) handleStoreMemory(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Content  string         `json:"content"`
		UserID   string         `json:"user_id"`
		Source   string         `json:"source"`
		Metadata map[string]any `json:"metadata"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", "invalid JSON body", false)
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "content is required", false)
		return
	}
	result, err := s.ingestor.Store(r.Context(), memory.StoreInput{
		Content:  req.Content,
		UserID:   req.UserID,
		Source:   req.Source,
		Metadata: req.Metadata,
	})
	if err != nil {
		code, errCode := classifyError(err)
		writeError(w, code, errCode, err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleDeleteMemory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing memory id", http.StatusBadRequest)
		return
	}
	if err := s.ingestor.Delete(r.Context(), id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRetrieveContext(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Query  string `json:"query"`
		UserID string `json:"user_id"`
		K      int    `json:"k"`
		Rerank bool   `json:"rerank"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", "invalid JSON body", false)
		return
	}
	if strings.TrimSpace(req.Query) == "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "query is required", false)
		return
	}
	result, err := s.retriever.Retrieve(r.Context(), memory.RetrieveInput{
		Query:  req.Query,
		UserID: req.UserID,
		K:      req.K,
		Rerank: req.Rerank,
	})
	if err != nil {
		code, errCode := classifyError(err)
		writeError(w, code, errCode, err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleGetUserState(w http.ResponseWriter, r *http.Request) {
	// path: /v1/users/{id}/state
	p := strings.TrimPrefix(r.URL.Path, "/v1/users/")
	p = strings.TrimSuffix(p, "/state")
	userID := p
	if userID == "" {
		userID = "default"
	}
	state, err := s.meta.GetUserState(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "retrieval_failed", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var errs []string
	if s.checkVec != nil {
		if err := s.checkVec(ctx); err != nil {
			errs = append(errs, "vector: "+err.Error())
		}
	}
	if s.checkEmbed != nil {
		if err := s.checkEmbed(ctx); err != nil {
			errs = append(errs, "embed: "+err.Error())
		}
	}
	if len(errs) > 0 {
		writeError(w, http.StatusServiceUnavailable, "not_ready", strings.Join(errs, "; "), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// --- Helpers ---

type apiError struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		Retryable bool   `json:"retryable"`
	} `json:"error"`
}

func writeError(w http.ResponseWriter, status int, code, msg string, retryable bool) {
	var e apiError
	e.Error.Code = code
	e.Error.Message = msg
	e.Error.Retryable = retryable
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(e)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func classifyError(err error) (int, string) {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "embed"):
		return http.StatusInternalServerError, "embedding_failed"
	case strings.Contains(msg, "stor"):
		return http.StatusInternalServerError, "storage_failed"
	default:
		_ = errors.New("") // keep errors import
		return http.StatusInternalServerError, "retrieval_failed"
	}
}
