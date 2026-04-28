package memory

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/gregdhill/engram/internal/chunk"
	"github.com/gregdhill/engram/internal/graph"
	"github.com/gregdhill/engram/internal/store/postgres"
	"github.com/gregdhill/engram/internal/store/qdrant"
)

// StoreInput is the input to the Ingestor.
type StoreInput struct {
	Content  string
	UserID   string // defaults to "default" if empty
	Source   string
	Metadata map[string]any
}

// StoreResult is returned from a successful Store call.
type StoreResult struct {
	MemoryID      string
	ChunksStored  int
	ChunksDeduped int
	Stored        bool // false if ALL chunks were duplicates
}

// Ingestor orchestrates memory ingestion: chunk → metadata → vector → graph.
type Ingestor struct {
	meta    postgres.MetaStore
	vec     qdrant.VectorStore
	chunker *chunk.Chunker
	graph   graph.GraphStore // may be nil or NopStore
	logger  *slog.Logger

	// Worker pool for async graph writes. Dispatch is non-blocking; if the
	// queue is full, the job is dropped and counted (graphDrops).
	graphJobs   chan func(context.Context)
	graphWG     sync.WaitGroup
	graphCancel context.CancelFunc
	graphDrops  uint64 // incremented on drop; exported via metric later
}

// IngestorOptions configures optional ingestor wiring.
type IngestorOptions struct {
	Graph        graph.GraphStore // nil → graph.NopStore{}
	Logger       *slog.Logger     // nil → slog.Default()
	GraphWorkers int              // 0 → 4
	GraphQueue   int              // 0 → 256
}

// NewIngestor constructs an Ingestor. Graph writes are dispatched to a bounded
// worker pool; failures are logged and never block ingest.
func NewIngestor(meta postgres.MetaStore, vec qdrant.VectorStore, chunker *chunk.Chunker, opts IngestorOptions) *Ingestor {
	if opts.Graph == nil {
		opts.Graph = graph.NopStore{}
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.GraphWorkers <= 0 {
		opts.GraphWorkers = 4
	}
	if opts.GraphQueue <= 0 {
		opts.GraphQueue = 256
	}

	poolCtx, cancel := context.WithCancel(context.Background())
	ing := &Ingestor{
		meta:        meta,
		vec:         vec,
		chunker:     chunker,
		graph:       opts.Graph,
		logger:      opts.Logger,
		graphJobs:   make(chan func(context.Context), opts.GraphQueue),
		graphCancel: cancel,
	}

	// Spin up workers.
	for i := 0; i < opts.GraphWorkers; i++ {
		ing.graphWG.Add(1)
		go func() {
			defer ing.graphWG.Done()
			for {
				select {
				case <-poolCtx.Done():
					return
				case job, ok := <-ing.graphJobs:
					if !ok {
						return
					}
					// Each job gets its own 2s timeout but uses poolCtx so
					// shutdown cancels in-flight work.
					jctx, jcancel := context.WithTimeout(poolCtx, 2*time.Second)
					job(jctx)
					jcancel()
				}
			}
		}()
	}

	return ing
}

// Close drains the graph worker pool. Call before process exit.
// Blocks until either all queued jobs finish or ctx expires (whichever first).
func (ing *Ingestor) Close(ctx context.Context) error {
	close(ing.graphJobs)
	done := make(chan struct{})
	go func() {
		ing.graphWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		ing.graphCancel()
		return nil
	case <-ctx.Done():
		ing.graphCancel()
		return ctx.Err()
	}
}

// dispatchGraph enqueues a graph write. Drops + logs if queue is full.
func (ing *Ingestor) dispatchGraph(name string, fn func(context.Context)) {
	select {
	case ing.graphJobs <- fn:
	default:
		ing.graphDrops++
		ing.logger.Warn("graph: dispatch dropped (queue full)", "op", name)
	}
}

// Store ingests content: chunks it, persists metadata, and upserts vectors.
//
// Dedup semantics: postgres.MetaStore.SaveChunks returns ErrDuplicate only when
// every chunk in the batch is a duplicate. Partial dedup is not exposed by the
// interface, so this implementation treats the batch as all-or-nothing.
func (ing *Ingestor) Store(ctx context.Context, input StoreInput) (*StoreResult, error) {
	content := strings.TrimSpace(input.Content)
	userID := input.UserID
	if userID == "" {
		userID = "default"
	}

	chunks, err := ing.chunker.Chunk(ctx, content)
	if err != nil {
		return nil, fmt.Errorf("chunk: %w", err)
	}
	if len(chunks) == 0 {
		return &StoreResult{}, nil
	}

	now := time.Now().UTC()
	memoryID := newUUID()
	importance := float32(0.3) + float32(len(chunks))*0.1
	if importance > 1.0 {
		importance = 1.0
	}

	mem := &postgres.Memory{
		ID:         memoryID,
		UserID:     userID,
		Source:     input.Source,
		Content:    content,
		Metadata:   input.Metadata,
		Importance: importance,
		CreatedAt:  now,
		AccessedAt: now,
	}
	if err := ing.meta.SaveMemory(ctx, mem); err != nil {
		return nil, fmt.Errorf("save memory: %w", err)
	}

	pgChunks := make([]*postgres.Chunk, len(chunks))
	for i, c := range chunks {
		sum := sha256.Sum256([]byte(c.Content))
		pgChunks[i] = &postgres.Chunk{
			ID:          newUUID(),
			MemoryID:    memoryID,
			UserID:      userID,
			Ord:         i,
			Content:     c.Content,
			ContentHash: sum[:],
			CreatedAt:   now,
		}
	}

	saveErr := ing.meta.SaveChunks(ctx, pgChunks)
	if errors.Is(saveErr, postgres.ErrDuplicate) {
		return &StoreResult{
			MemoryID:      memoryID,
			ChunksStored:  0,
			ChunksDeduped: len(pgChunks),
			Stored:        false,
		}, nil
	}
	if saveErr != nil {
		return nil, fmt.Errorf("save chunks: %w", saveErr)
	}

	points := make([]qdrant.Point, len(pgChunks))
	for i, pc := range pgChunks {
		points[i] = qdrant.Point{
			ID:     pc.ID,
			Vector: chunks[i].EmbVec,
			Payload: map[string]any{
				"memory_id":  memoryID,
				"chunk_id":   pc.ID,
				"user_id":    userID,
				"ord":        pc.Ord,
				"source":     input.Source,
				"importance": importance,
				"created_at": now.Format(time.RFC3339),
			},
		}
	}

	if err := ing.vec.Upsert(ctx, points); err != nil {
		// Degraded path: enqueue chunks for later vector indexing.
		for _, pc := range pgChunks {
			_ = ing.meta.EnqueuePending(ctx, pc.ID)
		}
	}

	// Best-effort: write chunks + Memory + OF edges to the graph.
	// Fire-and-forget per memory; failures logged, never block ingest.
	memNode := input.Source
	for i, pc := range pgChunks {
		node := graph.ChunkNode{
			ID:        pc.ID,
			MemoryID:  memoryID,
			UserID:    userID,
			Source:    memNode,
			Ord:       i,
			CreatedAt: now,
		}
		ing.dispatchGraph("write_chunk", func(ctx context.Context) {
			if err := ing.graph.WriteChunkAndOf(ctx, node); err != nil {
				ing.logger.Warn("graph: write chunk failed", "chunk_id", node.ID, "err", err)
			}
		})
	}
	if len(pgChunks) > 1 {
		edges := make([]graph.SequentialEdge, 0, len(pgChunks)-1)
		for i := 0; i < len(pgChunks)-1; i++ {
			edges = append(edges, graph.SequentialEdge{
				PrevID: pgChunks[i].ID,
				NextID: pgChunks[i+1].ID,
				UserID: userID,
			})
		}
		ing.dispatchGraph("write_next_edges", func(ctx context.Context) {
			// Brief delay so the chunk-write jobs above have time to land.
			// The MERGE statements in WriteChunkAndOf are idempotent, so this
			// is just a happy-path optimization. If the chunks aren't there
			// yet, the MATCH in WriteSequentialEdges will return zero rows
			// and nothing breaks.
			time.Sleep(200 * time.Millisecond)
			if err := ing.graph.WriteSequentialEdges(ctx, edges); err != nil {
				ing.logger.Warn("graph: write sequential edges failed",
					"memory_id", memoryID, "count", len(edges), "err", err)
			}
		})
	}

	return &StoreResult{
		MemoryID:      memoryID,
		ChunksStored:  len(pgChunks),
		ChunksDeduped: 0,
		Stored:        true,
	}, nil
}

// newUUID generates an RFC 4122 v4 UUID string without external dependencies.
func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
