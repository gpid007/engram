// Package main is the entry point for the engram binary.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gregdhill/engram/internal/chunk"
	"github.com/gregdhill/engram/internal/config"
	"github.com/gregdhill/engram/internal/embed"
	"github.com/gregdhill/engram/internal/graph"
	"github.com/gregdhill/engram/internal/httpapi"
	"github.com/gregdhill/engram/internal/mcp"
	"github.com/gregdhill/engram/internal/memory"
	"github.com/gregdhill/engram/internal/rerank"
	"github.com/gregdhill/engram/internal/store/postgres"
	"github.com/gregdhill/engram/internal/store/qdrant"
)

func main() {
	// 1. Parse flags.
	var (
		configPath string
		mcpMode    bool
	)
	flag.StringVar(&configPath, "config", "", "path to YAML config file (default: built-in defaults)")
	flag.BoolVar(&mcpMode, "mcp", false, "run as MCP stdio server only (no HTTP)")
	flag.Parse()

	// Resolve config path: if not given, use "engram.yaml" if it exists.
	if configPath == "" {
		if _, err := os.Stat("engram.yaml"); err == nil {
			configPath = "engram.yaml"
		}
	}

	// 2. Load config.
	cfg, err := config.LoadOrDefaults(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "engram: load config: %v\n", err)
		os.Exit(1)
	}

	// 3. Set up slog logger.
	level := slog.LevelInfo
	switch cfg.Logging.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	var handler slog.Handler
	opts := &slog.HandlerOptions{Level: level}
	if cfg.Logging.Format == "json" {
		handler = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		handler = slog.NewTextHandler(os.Stderr, opts)
	}
	slog.SetDefault(slog.New(handler))

	// Signal context for graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	// 4. Connect Postgres.
	slog.Info("connecting to postgres", "dsn_prefix", cfg.Meta.DSN[:min(len(cfg.Meta.DSN), 30)])
	metaStore, err := postgres.New(ctx, cfg.Meta.DSN)
	if err != nil {
		slog.Error("postgres connect failed", "err", err)
		os.Exit(1)
	}

	// 5. Connect Qdrant.
	slog.Info("connecting to qdrant", "addr", cfg.Vector.Addr)
	vecStore, err := qdrant.New(ctx, cfg.Vector.Addr, cfg.Vector.Collection)
	if err != nil {
		slog.Error("qdrant connect failed", "err", err)
		os.Exit(1)
	}

	// 6. Create Ollama embedder.
	embedder := embed.NewOllamaEmbedder(embed.Config{
		BaseURL: cfg.Embedding.BaseURL,
		Model:   cfg.Embedding.Model,
		Dim:     cfg.Embedding.Dim,
		Batch:   cfg.Embedding.Batch,
		Timeout: time.Duration(cfg.Embedding.TimeoutMS) * time.Millisecond,
		Retries: cfg.Embedding.Retries,
	})

	// 7. Ensure Qdrant collection exists with correct dimension.
	slog.Info("ensuring qdrant collection", "collection", cfg.Vector.Collection, "dim", cfg.Embedding.Dim)
	if err := vecStore.EnsureCollection(ctx, uint64(cfg.Embedding.Dim)); err != nil {
		slog.Error("qdrant EnsureCollection failed", "err", err)
		os.Exit(1)
	}

	// 8. Build reranker.
	timeout := time.Duration(cfg.Rerank.TimeoutMS) * time.Millisecond
	var reranker rerank.Reranker
	switch cfg.Rerank.Provider {
	case "llm":
		reranker = rerank.NewLLMReranker(rerank.LLMConfig{
			BaseURL: cfg.Embedding.BaseURL,
			Model:   cfg.Rerank.LLMModel,
			Timeout: timeout,
		})
	case "remote":
		apiKey := ""
		if cfg.Rerank.Remote.APIKeyEnv != "" {
			apiKey = os.Getenv(cfg.Rerank.Remote.APIKeyEnv)
		}
		reranker = rerank.NewRemoteReranker(rerank.RemoteConfig{
			BaseURL: cfg.Rerank.Remote.BaseURL,
			APIKey:  apiKey,
			Model:   cfg.Rerank.Remote.Model,
			Timeout: timeout,
		})
	case "crossenc":
		r, cerr := rerank.NewCrossEncoderReranker(cfg.Rerank.CrossEncModelPath, timeout)
		if cerr != nil {
			slog.Warn("crossenc reranker unavailable, falling back to nil", "err", cerr)
			reranker = nil
		} else {
			reranker = r
		}
	default:
		// "none" or anything else.
		reranker = nil
	}

	// 9. Build chunker.
	chunker := chunk.NewChunker(embedder, chunk.Config{
		MaxTokens:           cfg.Chunking.MaxTokens,
		MinTokens:           cfg.Chunking.MinTokens,
		SimilarityThreshold: cfg.Chunking.SimilarityThreshold,
	})

	// 10. Build graph store (nop for now).
	graphStore := graph.NopStore{}

	// 11. Build ingestor.
	ingestor := memory.NewIngestor(metaStore, vecStore, chunker)

	// 12. Build retriever.
	retriever := memory.NewRetriever(
		metaStore,
		vecStore,
		embedder,
		reranker,
		graphStore,
		memory.RetrieverConfig{
			VectorK:     cfg.Retrieval.VectorK,
			BM25K:       cfg.Retrieval.BM25K,
			RerankK:     cfg.Retrieval.RerankK,
			FinalK:      cfg.Retrieval.FinalK,
			RRFK:        float64(cfg.Retrieval.RRFK),
			VectorFloor: float32(cfg.Retrieval.VectorFloor),
		},
	)

	// 13. Build reconciler with placeholder retryFn.
	retryFn := func(ctx context.Context, chunkID string) error {
		slog.Warn("pending vector retry not implemented", "chunk_id", chunkID)
		return nil
	}
	reconciler := memory.NewReconciler(metaStore, vecStore, memory.ReconcilerConfig{}, retryFn)

	// 14. Start reconciler.
	reconciler.Start(ctx)

	// --- Transport startup ---

	if mcpMode {
		// MCP-only mode: block on stdio server.
		slog.Info("starting MCP stdio server")
		mcpServer := mcp.NewServer(ingestor, retriever, metaStore)
		if err := mcpServer.ServeStdio(ctx); err != nil {
			slog.Error("MCP server error", "err", err)
		}
	} else {
		// HTTP mode.
		checkVec := func(ctx context.Context) error { return nil }
		checkEmbed := func(ctx context.Context) error { return nil }

		httpServer := httpapi.NewServer(
			cfg.Server.HTTPAddr,
			ingestor,
			retriever,
			metaStore,
			checkVec,
			checkEmbed,
		)

		slog.Info("starting HTTP server", "addr", cfg.Server.HTTPAddr)
		go func() {
			if err := httpServer.ListenAndServe(ctx); err != nil {
				slog.Error("HTTP server error", "err", err)
			}
		}()

		if cfg.Server.MCPStdio {
			slog.Info("starting MCP stdio server alongside HTTP")
			mcpServer := mcp.NewServer(ingestor, retriever, metaStore)
			go func() {
				if err := mcpServer.ServeStdio(ctx); err != nil {
					slog.Error("MCP server error", "err", err)
				}
			}()
		}

		// Block until signal.
		<-ctx.Done()
	}

	// --- Graceful shutdown ---
	slog.Info("shutting down")

	reconciler.Stop()

	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := vecStore.Close(); err != nil {
		slog.Warn("qdrant close error", "err", err)
	}
	if err := metaStore.Close(); err != nil {
		slog.Warn("postgres close error", "err", err)
	}
	_ = shutCtx // timeout context available for future blocking shutdown steps
}

// min returns the smaller of a and b.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
