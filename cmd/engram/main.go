// Package main is the entry point for the engram binary.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gregdhill/engram/internal/chunk"
	"github.com/gregdhill/engram/internal/cli"
	"github.com/gregdhill/engram/internal/config"
	"github.com/gregdhill/engram/internal/embed"
	"github.com/gregdhill/engram/internal/graph"
	"github.com/gregdhill/engram/internal/httpapi"
	"github.com/gregdhill/engram/internal/mcp"
	"github.com/gregdhill/engram/internal/memory"
	"github.com/gregdhill/engram/internal/rerank"
	neo4jstore "github.com/gregdhill/engram/internal/store/neo4j"
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

	// Resolve config path: flag > ENGRAM_CONFIG env > engram.yaml if exists.
	if configPath == "" {
		if v := os.Getenv("ENGRAM_CONFIG"); v != "" {
			configPath = v
		} else if _, err := os.Stat("engram.yaml"); err == nil {
			configPath = "engram.yaml"
		}
	}

	// 2. Load config.
	cfg, err := config.LoadOrDefaults(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "engram: load config: %v\n", err)
		os.Exit(1)
	}

	// 3. Subcommand dispatch — runs before any server infrastructure is started.
	// Usage: engram [flags] <put|get|status> [args...]
	if args := flag.Args(); len(args) > 0 {
		runCLI(cfg, args)
		return
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

	// 6. Create embedder (Ollama default, ONNX if configured).
	ollamaEmbedder := embed.NewOllamaEmbedder(embed.Config{
		BaseURL: cfg.Embedding.BaseURL,
		Model:   cfg.Embedding.Model,
		Dim:     cfg.Embedding.Dim,
		Batch:   cfg.Embedding.Batch,
		Timeout: time.Duration(cfg.Embedding.TimeoutMS) * time.Millisecond,
		Retries: cfg.Embedding.Retries,
	})

	var embedder embed.Embedder = ollamaEmbedder
	if cfg.Embedding.Provider == "onnx" {
		onnxEmb, onnxErr := embed.NewONNXEmbedder(embed.ONNXConfig{
			ModelDir:  cfg.Embedding.ModelDir,
			LibPath:   cfg.Embedding.LibPath,
			MaxSeqLen: cfg.Embedding.MaxSeqLen,
			Dim:       cfg.Embedding.Dim,
			BatchSize: cfg.Embedding.Batch,
			Timeout:   time.Duration(cfg.Embedding.TimeoutMS) * time.Millisecond,
		})
		if onnxErr != nil {
			slog.Warn("onnx embedder failed; falling back to ollama",
				"err", onnxErr,
				"model_dir", cfg.Embedding.ModelDir,
			)
		} else {
			embedder = onnxEmb
			slog.Info("onnx embedder initialized", "model_dir", cfg.Embedding.ModelDir)
		}
	}

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

	// 10. Build graph store.
	var graphStore graph.GraphStore = graph.NopStore{}
	switch cfg.Graph.Provider {
	case "", "none":
		// keep nop
	case "neo4j":
		password := os.Getenv(cfg.Graph.PasswordEnv)
		if password == "" {
			slog.Warn("neo4j password env not set; falling back to nop graph",
				"env", cfg.Graph.PasswordEnv)
		} else {
			ns, nerr := neo4jstore.New(ctx, neo4jstore.Config{
				URI:      cfg.Graph.URI,
				Username: cfg.Graph.Username,
				Password: password,
				Database: cfg.Graph.Database,
				Timeout:  time.Duration(cfg.Graph.TimeoutMS) * time.Millisecond,
			})
			if nerr != nil {
				slog.Warn("neo4j connect failed; falling back to nop graph", "err", nerr)
			} else if serr := ns.EnsureSchema(ctx); serr != nil {
				slog.Warn("neo4j schema setup failed; falling back to nop graph", "err", serr)
				_ = ns.Close(ctx)
			} else {
				graphStore = ns
				defer func() {
					closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					if err := ns.Close(closeCtx); err != nil {
						slog.Warn("neo4j close error", "err", err)
					}
				}()
				slog.Info("neo4j graph store initialized", "uri", cfg.Graph.URI)
			}
		}
	default:
		slog.Warn("unknown graph.provider; falling back to nop", "provider", cfg.Graph.Provider)
	}

	// 11. Build ingestor.
	ingestor := memory.NewIngestor(metaStore, vecStore, chunker, memory.IngestorOptions{
		Graph:  graphStore,
		Logger: slog.Default(),
	})

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

	if err := ingestor.Close(shutCtx); err != nil {
		slog.Warn("ingestor close error", "err", err)
	}

	if err := vecStore.Close(); err != nil {
		slog.Warn("qdrant close error", "err", err)
	}
	if err := metaStore.Close(); err != nil {
		slog.Warn("postgres close error", "err", err)
	}
	_ = shutCtx // timeout context available for future blocking shutdown steps
}

// runCLI dispatches CLI subcommands against the running Engram HTTP server.
// It does not start Postgres, Qdrant, or any server infrastructure.
func runCLI(cfg *config.Config, args []string) {
	if len(args) == 0 {
		printUsage()
		os.Exit(1)
	}

	parseTimeout := time.Duration(cfg.CLI.ParseTimeoutMS) * time.Millisecond
	client := cli.NewClient(cfg.CLI.BaseURL, cfg.CLI.UserID, 15*time.Second)
	parser := cli.NewOllamaParser(cfg.CLI.ParseBaseURL, cfg.CLI.ParseModel, cfg.CLI.UserID, parseTimeout)
	rc := cli.RunConfig{Client: client, Parser: parser, UserID: cfg.CLI.UserID}
	ctx := context.Background()

	cmd := args[0]
	rest := args[1:]

	switch cmd {
	case "add":
		fs := flag.NewFlagSet("add", flag.ContinueOnError)
		fileFlag := fs.String("f", "", "file to ingest (.txt or .md)")
		fs.StringVar(fileFlag, "file", "", "file to ingest (.txt or .md)")
		dirFlag := fs.String("d", "", "directory to ingest (walks .txt and .md recursively)")
		fs.StringVar(dirFlag, "dir", "", "directory to ingest")
		dryRun := fs.Bool("dry-run", false, "print what would be stored without storing")
		if err := fs.Parse(rest); err != nil {
			os.Exit(1)
		}
		raw := strings.Join(fs.Args(), " ")
		if err := cli.RunAdd(ctx, rc, raw, *fileFlag, *dirFlag, *dryRun); err != nil {
			fmt.Fprintf(os.Stderr, "add: %v\n", err)
			os.Exit(1)
		}

	case "find":
		fs := flag.NewFlagSet("find", flag.ContinueOnError)
		k := fs.Int("k", 5, "number of results to return")
		if err := fs.Parse(rest); err != nil {
			os.Exit(1)
		}
		query := strings.Join(fs.Args(), " ")
		if query == "" {
			fmt.Fprintln(os.Stderr, "usage: engram find [--k N] <query>")
			os.Exit(1)
		}
		if err := cli.RunFind(ctx, rc, query, *k); err != nil {
			fmt.Fprintf(os.Stderr, "find: %v\n", err)
			os.Exit(1)
		}

	case "rm", "remove":
		fs := flag.NewFlagSet("rm", flag.ContinueOnError)
		queryFlag := fs.String("q", "", "semantic search query to find memories to delete")
		fs.StringVar(queryFlag, "query", "", "semantic search query to find memories to delete")
		force := fs.Bool("force", false, "skip confirmation prompt")
		dryRun := fs.Bool("dry-run", false, "print what would be deleted without deleting")
		if err := fs.Parse(rest); err != nil {
			os.Exit(1)
		}
		ids := fs.Args()
		if len(ids) == 0 && *queryFlag == "" {
			fmt.Fprintln(os.Stderr, "usage: engram rm [--query <q>] [--force] [--dry-run] [<id>...]")
			os.Exit(1)
		}
		if err := cli.RunRemove(ctx, rc, ids, *queryFlag, *force, *dryRun); err != nil {
			fmt.Fprintf(os.Stderr, "rm: %v\n", err)
			os.Exit(1)
		}

	case "status":
		if err := cli.RunStatus(ctx, rc); err != nil {
			fmt.Fprintf(os.Stderr, "status: %v\n", err)
			os.Exit(1)
		}

	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "usage: engram [flags] <command> [args]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "commands:")
	fmt.Fprintln(os.Stderr, "  add [-f file] [-d dir] [--dry-run] [text]        store memories")
	fmt.Fprintln(os.Stderr, "  find [--k N] <query>                              retrieve memories")
	fmt.Fprintln(os.Stderr, "  rm|remove [-q query] [--force] [--dry-run] [ids]  delete memories")
	fmt.Fprintln(os.Stderr, "  status                                            server stats")
}

// min returns the smaller of a and b.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
