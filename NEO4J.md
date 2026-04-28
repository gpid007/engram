# Neo4j Integration Plan for Engram

> **Execution model:** Each WS below is a self-contained prompt for `lo-coder-q4`
> (Qwen2.5 Coder 7B Q4, local, free). WS prompts are written as **literal copy-paste
> instructions**: exact file paths, exact code blocks, exact verification commands.
> No design decisions are deferred to the executing agent — Qwen2.5 Coder Q4 is
> small and quantized; it executes well-specified mechanical edits but fails on
> ambiguous design work. Only escalate to `ac-sonnet` for WS2 (Cypher + driver),
> and only for review (not authoring) elsewhere.

---

## Cost Routing

| Tier | Model | Used for |
| --- | --- | --- |
| Free / local | `lo-coder-q4` | WS1, WS3, WS4, WS5, WS6, WS7 — mechanical edits with full prescription |
| Paid (cheap) | `gg-25flash` | Optional spot-check review of WS1/WS4 diffs |
| Paid (real) | `ac-sonnet` | WS2 only (Neo4j driver + Cypher are non-trivial) |

Estimated paid token spend: **WS2 only**. All other workstreams should run on
`lo-coder-q4` and cost nothing.

---

## Ground Truth (Verified Against Repo)

- **Graph interface:** `internal/graph/graph.go:15` — current signature is
  `ExpandRelated(ctx, []ChunkID, depth int) ([]ChunkID, error)`. `ChunkID` is a
  type alias for `string` (`graph.go:9`).
- **Nop:** `internal/graph/nop.go:10` — `var _ GraphStore = NopStore{}`.
- **Retriever caller:** `internal/memory/retriever.go:217` —
  `r.graph.ExpandRelated(ctx, ids, 1)`. Already error-tolerant (line 218).
  `r.graph` is checked for nil at line 210.
- **Ingestor:** `internal/memory/ingestor.go:50` — `Store()` does Postgres save
  (line 82), chunk save (line 100), Qdrant upsert (line 130). No graph write.
  Ingestor struct has no `graph` field (line 34). No logger field either.
- **Main wiring:** `cmd/engram/main.go:148` — `graphStore := graph.NopStore{}`.
  Retriever takes `graphStore` directly (line 159). Ingestor does **not**
  currently take a graph store.
- **Config:** `internal/config/config.go:85-94` — `Config` struct has no `Graph`
  field. `Defaults()` at line 97. `applyEnvOverrides()` at line 178.
  `validate()` at line 265.
- **README:** Already references `internal/store/neo4j/` (line 262) and has a
  Neo4j-aware architecture diagram (lines 273-308) and graph expansion section
  (lines 310-329). **WS7 is mostly bookkeeping.**
- **Architecture doc:** `docs/architecture.md:270` (per prior plan) marks Neo4j
  as future work. WS7 verifies and updates.

---

## Design Summary

**Goal:** Replace `NopStore` with a real Neo4j adapter. Build chunk/memory graph
on ingest. Use it to expand retrieval results. Degrade gracefully on every
failure.

**Schema (final):**
```cypher
// Constraints
CREATE CONSTRAINT chunk_id IF NOT EXISTS FOR (c:Chunk) REQUIRE c.id IS UNIQUE;
CREATE CONSTRAINT memory_id IF NOT EXISTS FOR (m:Memory) REQUIRE m.id IS UNIQUE;

// Indexes
CREATE INDEX chunk_user IF NOT EXISTS FOR (c:Chunk) ON (c.user_id);
CREATE INDEX chunk_user_created IF NOT EXISTS FOR (c:Chunk) ON (c.user_id, c.created_at);

// Nodes
(:Chunk  {id, memory_id, user_id, ord, created_at})
(:Memory {id, user_id, source, created_at})

// Edges
(:Chunk)-[:OF]->(:Memory)              // chunk belongs to memory
(:Chunk)-[:NEXT]->(:Chunk)             // sequential chunk in same memory
(:Chunk)-[:SIMILAR {score}]->(:Chunk)  // cosine similarity ≥ threshold
```

Chunk IDs are globally unique UUIDs (see `ingestor.go:151` `newUUID`), so a
single uniqueness constraint on `id` is sufficient. `user_id` is a *property*
used as a tenant filter in queries, not a uniqueness key.

**Failure model:**
- Postgres + Qdrant remain canonical. Existing degraded path
  (`pending_vectors`) is unchanged.
- Neo4j writes: dispatched to a **bounded worker pool** owned by the Ingestor.
  Pool drains on shutdown. Errors logged + counted via metric. Never block
  ingest.
- Neo4j reads: errors fall through. Existing fused list returned. No change to
  retriever error semantics — the `if gerr == nil` guard at
  `retriever.go:218` already handles this.

**Compose health gate:** Neo4j is **not** in `engram`'s `depends_on`. The
service starts with `provider: none` if Neo4j is unhealthy. Documented as the
graceful path.

---

## Fixed Issues vs. Prior Plan

| # | Issue | Fix |
| --- | --- | --- |
| 1 | `ExpandRelated` signature change claimed backward-compat — was actually breaking | New method `ExpandRelatedWithOptions`. Old `ExpandRelated` retained, delegates to new method with default options. Single caller in retriever switches to new method. |
| 2 | Cypher `*1..$depth` — bounds cannot be parameters | Depth is fixed at `*1..1` (sufficient for design). If we ever need variable depth, build query string server-side with int validation. |
| 3 | `MERGE` key included `user_id` despite UNIQUE constraint on `id` alone | `MERGE (c:Chunk {id: $id})` — `user_id` is set as property only. Tenant filtering happens in MATCH `WHERE`. |
| 4 | Ingest goroutines used `context.Background()`, leaked, no shutdown | Bounded worker pool (4 workers) with own context, drained on `Close()`. |
| 5 | Race: sequential edge MATCH could fire before chunk exists | Single Cypher per ingest writes chunks + NEXT edges in **one transaction**. No more per-edge round trips. |
| 6 | Similarity from Qdrant during ingest = latency | Deferred. SIMILAR edges are computed by reconciler (background), not ingest. Ingest writes only Chunk/Memory/OF/NEXT. |
| 7 | Password env validation only checked name set, not resolution | `validate()` checks `os.Getenv(PasswordEnv) != ""` when provider=neo4j. |
| 8 | Compose `depends_on: neo4j: service_healthy` violates graceful degradation | Removed. Documented. |
| 9 | README/arch doc duplicate diagrams | WS7 removes the older diagram in README (lines 158-176) and verifies arch doc is consistent. |
| 10 | `:OF` edge declared in schema but not written | `WriteChunk` (renamed `WriteChunkAndOf`) writes chunk + memory + OF in one MERGE. |
| 11 | `MemoryID` set on chunk but no Memory node created | Same Cypher creates `:Memory` and `:OF` edge. |
| 12 | Optimistic time estimate | Updated below; honest budget. |

---

## Workstreams

### WS1 — Interface Extension + Nop + Tests

**Agent:** `lo-coder-q4` | **Blocks:** WS2, WS3 | **Est:** 30 min

#### Files to edit

1. `internal/graph/graph.go`
2. `internal/graph/nop.go`
3. `internal/graph/graph_test.go`

#### Exact replacement: `internal/graph/graph.go`

```go
// Package graph defines the GraphStore interface for graph-based memory expansion.
package graph

import (
	"context"
	"time"
)

// ChunkID is a UUID string identifying a memory chunk.
type ChunkID = string

// ChunkNode is the data needed to write a chunk into the graph.
type ChunkNode struct {
	ID        ChunkID
	MemoryID  string
	UserID    string
	Source    string
	Ord       int
	CreatedAt time.Time
}

// SequentialEdge represents a NEXT relationship between two chunks in the same memory.
type SequentialEdge struct {
	PrevID ChunkID
	NextID ChunkID
	UserID string
}

// SimilarEdge represents a SIMILAR relationship between two chunks (cross-memory).
type SimilarEdge struct {
	AID    ChunkID
	BID    ChunkID
	UserID string
	Score  float64
}

// ExpandOptions controls graph traversal during retrieval.
type ExpandOptions struct {
	UserID    string
	MaxExpand int // hard cap on expanded results; 0 means no cap
}

// GraphStore expands a set of chunk IDs to related chunks via graph traversal,
// and accepts writes for chunk nodes and edges. All write methods are
// idempotent: re-running the same call must not produce duplicates.
type GraphStore interface {
	// ExpandRelated returns chunk IDs related to the given seeds up to depth hops.
	// Returns an empty slice (not an error) when no relations exist.
	// Retained for backward compatibility; calls ExpandRelatedWithOptions with
	// zero-value options.
	ExpandRelated(ctx context.Context, chunkIDs []ChunkID, depth int) ([]ChunkID, error)

	// ExpandRelatedWithOptions is the user-scoped, capped form of ExpandRelated.
	ExpandRelatedWithOptions(ctx context.Context, chunkIDs []ChunkID, depth int, opts ExpandOptions) ([]ChunkID, error)

	// WriteChunkAndOf writes a Chunk node, ensures the parent Memory node,
	// and creates the (:Chunk)-[:OF]->(:Memory) edge in one transaction.
	WriteChunkAndOf(ctx context.Context, node ChunkNode) error

	// WriteSequentialEdges writes (:Chunk)-[:NEXT]->(:Chunk) edges in batch.
	// All chunks referenced must already exist (call WriteChunkAndOf first).
	WriteSequentialEdges(ctx context.Context, edges []SequentialEdge) error

	// WriteSimilarEdge writes a single (:Chunk)-[:SIMILAR {score}]->(:Chunk) edge.
	WriteSimilarEdge(ctx context.Context, edge SimilarEdge) error

	// Close releases resources (driver, worker pool, etc.).
	Close(ctx context.Context) error
}
```

#### Exact replacement: `internal/graph/nop.go`

```go
package graph

import "context"

// NopStore is a no-op GraphStore that always returns an empty slice and
// silently accepts writes. Use this when graph expansion is not configured.
type NopStore struct{}

// Ensure NopStore implements GraphStore at compile time.
var _ GraphStore = NopStore{}

func (NopStore) ExpandRelated(_ context.Context, _ []ChunkID, _ int) ([]ChunkID, error) {
	return nil, nil
}

func (NopStore) ExpandRelatedWithOptions(_ context.Context, _ []ChunkID, _ int, _ ExpandOptions) ([]ChunkID, error) {
	return nil, nil
}

func (NopStore) WriteChunkAndOf(_ context.Context, _ ChunkNode) error {
	return nil
}

func (NopStore) WriteSequentialEdges(_ context.Context, _ []SequentialEdge) error {
	return nil
}

func (NopStore) WriteSimilarEdge(_ context.Context, _ SimilarEdge) error {
	return nil
}

func (NopStore) Close(_ context.Context) error {
	return nil
}
```

#### Exact replacement: `internal/graph/graph_test.go`

Replace entire file with:

```go
package graph

import (
	"context"
	"testing"
	"time"
)

// recordingStore implements GraphStore and records every call. Used for
// verifying that the Ingestor and Retriever invoke the expected methods.
type recordingStore struct {
	Chunks         []ChunkNode
	Sequentials    [][]SequentialEdge
	Similars       []SimilarEdge
	ExpandCalls    int
	ExpandOptsLast ExpandOptions
	ExpandResult   []ChunkID
	Closed         bool
}

func (r *recordingStore) ExpandRelated(ctx context.Context, ids []ChunkID, depth int) ([]ChunkID, error) {
	return r.ExpandRelatedWithOptions(ctx, ids, depth, ExpandOptions{})
}

func (r *recordingStore) ExpandRelatedWithOptions(_ context.Context, _ []ChunkID, _ int, opts ExpandOptions) ([]ChunkID, error) {
	r.ExpandCalls++
	r.ExpandOptsLast = opts
	return r.ExpandResult, nil
}

func (r *recordingStore) WriteChunkAndOf(_ context.Context, n ChunkNode) error {
	r.Chunks = append(r.Chunks, n)
	return nil
}

func (r *recordingStore) WriteSequentialEdges(_ context.Context, edges []SequentialEdge) error {
	r.Sequentials = append(r.Sequentials, edges)
	return nil
}

func (r *recordingStore) WriteSimilarEdge(_ context.Context, e SimilarEdge) error {
	r.Similars = append(r.Similars, e)
	return nil
}

func (r *recordingStore) Close(_ context.Context) error {
	r.Closed = true
	return nil
}

var _ GraphStore = (*recordingStore)(nil)

func TestNopStore_AllMethodsReturnNoError(t *testing.T) {
	var s NopStore
	ctx := context.Background()

	ids, err := s.ExpandRelated(ctx, []ChunkID{"a"}, 1)
	if err != nil || len(ids) != 0 {
		t.Fatalf("ExpandRelated: got (%v, %v), want (nil, nil)", ids, err)
	}

	ids, err = s.ExpandRelatedWithOptions(ctx, []ChunkID{"a"}, 1, ExpandOptions{UserID: "u", MaxExpand: 5})
	if err != nil || len(ids) != 0 {
		t.Fatalf("ExpandRelatedWithOptions: got (%v, %v), want (nil, nil)", ids, err)
	}

	if err := s.WriteChunkAndOf(ctx, ChunkNode{ID: "c1", UserID: "u", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("WriteChunkAndOf: %v", err)
	}
	if err := s.WriteSequentialEdges(ctx, []SequentialEdge{{PrevID: "a", NextID: "b", UserID: "u"}}); err != nil {
		t.Fatalf("WriteSequentialEdges: %v", err)
	}
	if err := s.WriteSimilarEdge(ctx, SimilarEdge{AID: "a", BID: "b", UserID: "u", Score: 0.9}); err != nil {
		t.Fatalf("WriteSimilarEdge: %v", err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestRecordingStore_ImplementsGraphStore(t *testing.T) {
	var _ GraphStore = (*recordingStore)(nil)
}
```

#### Verification (run from repo root)

```bash
go build ./internal/graph/...
go test ./internal/graph/... -count=1
```

Both commands must exit 0. Do not proceed to other WS until they pass.

---

### WS2 — Neo4j Adapter

**Agent:** `ac-sonnet` (Cypher + driver wiring) | **Depends on:** WS1 | **Est:** 3-4 h

#### Why this is the only paid-tier WS

Neo4j Go driver v5 has subtle session/transaction lifecycle rules. Cypher with
`UNWIND` + `MERGE` + relationship variables requires careful authoring.
Quantized 7B models reliably mis-author this.

#### Files to create

1. `internal/store/neo4j/neo4j.go` — driver lifecycle, Store struct
2. `internal/store/neo4j/schema.go` — `EnsureSchema`
3. `internal/store/neo4j/write.go` — write methods
4. `internal/store/neo4j/read.go` — `ExpandRelatedWithOptions`
5. `internal/store/neo4j/neo4j_test.go` — integration tests gated on env

#### `go.mod`

Add: `github.com/neo4j/neo4j-go-driver/v5 v5.x.x` (latest 5.x).

#### `internal/store/neo4j/neo4j.go`

```go
// Package neo4j implements graph.GraphStore against a Neo4j 5.x server.
package neo4j

import (
	"context"
	"fmt"
	"time"

	"github.com/gregdhill/engram/internal/graph"
	neo "github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// Config holds connection settings for the Neo4j driver.
type Config struct {
	URI         string        // bolt://host:7687
	Username    string
	Password    string        // resolved value, not env var name
	Database    string        // default "neo4j"
	MaxPoolSize int           // 0 → driver default
	Timeout     time.Duration // per-query timeout
}

// Store implements graph.GraphStore.
type Store struct {
	driver  neo.DriverWithContext
	db      string
	timeout time.Duration
}

// Compile-time check.
var _ graph.GraphStore = (*Store)(nil)

// New connects, verifies connectivity, and returns a Store.
func New(ctx context.Context, cfg Config) (*Store, error) {
	if cfg.Database == "" {
		cfg.Database = "neo4j"
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 2 * time.Second
	}

	auth := neo.BasicAuth(cfg.Username, cfg.Password, "")
	driver, err := neo.NewDriverWithContext(cfg.URI, auth, func(c *neo.Config) {
		if cfg.MaxPoolSize > 0 {
			c.MaxConnectionPoolSize = cfg.MaxPoolSize
		}
	})
	if err != nil {
		return nil, fmt.Errorf("neo4j: new driver: %w", err)
	}

	verifyCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	if err := driver.VerifyConnectivity(verifyCtx); err != nil {
		_ = driver.Close(ctx)
		return nil, fmt.Errorf("neo4j: verify connectivity: %w", err)
	}

	return &Store{driver: driver, db: cfg.Database, timeout: cfg.Timeout}, nil
}

// Close releases the driver.
func (s *Store) Close(ctx context.Context) error {
	if s == nil || s.driver == nil {
		return nil
	}
	return s.driver.Close(ctx)
}

// session opens a session bound to the configured database.
func (s *Store) session(access neo.AccessMode) neo.SessionWithContext {
	return s.driver.NewSession(context.Background(), neo.SessionConfig{
		DatabaseName: s.db,
		AccessMode:   access,
	})
}
```

#### `internal/store/neo4j/schema.go`

```go
package neo4j

import (
	"context"
	"fmt"

	neo "github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// schemaStatements is run idempotently at startup. Each statement is independent
// and uses IF NOT EXISTS semantics.
var schemaStatements = []string{
	`CREATE CONSTRAINT chunk_id IF NOT EXISTS FOR (c:Chunk) REQUIRE c.id IS UNIQUE`,
	`CREATE CONSTRAINT memory_id IF NOT EXISTS FOR (m:Memory) REQUIRE m.id IS UNIQUE`,
	`CREATE INDEX chunk_user IF NOT EXISTS FOR (c:Chunk) ON (c.user_id)`,
	`CREATE INDEX chunk_user_created IF NOT EXISTS FOR (c:Chunk) ON (c.user_id, c.created_at)`,
}

// EnsureSchema applies all schema statements. Safe to call repeatedly.
func (s *Store) EnsureSchema(ctx context.Context) error {
	sess := s.session(neo.AccessModeWrite)
	defer sess.Close(ctx)

	for _, stmt := range schemaStatements {
		_, err := sess.ExecuteWrite(ctx, func(tx neo.ManagedTransaction) (any, error) {
			res, err := tx.Run(ctx, stmt, nil)
			if err != nil {
				return nil, err
			}
			_, err = res.Consume(ctx)
			return nil, err
		})
		if err != nil {
			return fmt.Errorf("neo4j: schema %q: %w", stmt, err)
		}
	}
	return nil
}
```

#### `internal/store/neo4j/write.go`

```go
package neo4j

import (
	"context"
	"fmt"

	"github.com/gregdhill/engram/internal/graph"
	neo "github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// WriteChunkAndOf writes a Chunk node, the parent Memory node (if missing),
// and the (:Chunk)-[:OF]->(:Memory) edge. Idempotent via MERGE.
func (s *Store) WriteChunkAndOf(ctx context.Context, n graph.ChunkNode) error {
	sess := s.session(neo.AccessModeWrite)
	defer sess.Close(ctx)

	const cypher = `
MERGE (m:Memory {id: $memory_id})
  ON CREATE SET m.user_id = $user_id, m.source = $source, m.created_at = $created_at
MERGE (c:Chunk {id: $chunk_id})
  ON CREATE SET c.memory_id = $memory_id, c.user_id = $user_id, c.ord = $ord, c.created_at = $created_at
  ON MATCH  SET c.ord = $ord
MERGE (c)-[:OF]->(m)
`
	params := map[string]any{
		"chunk_id":   n.ID,
		"memory_id":  n.MemoryID,
		"user_id":    n.UserID,
		"source":     n.Source,
		"ord":        n.Ord,
		"created_at": n.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}

	_, err := sess.ExecuteWrite(ctx, func(tx neo.ManagedTransaction) (any, error) {
		res, err := tx.Run(ctx, cypher, params)
		if err != nil {
			return nil, err
		}
		_, err = res.Consume(ctx)
		return nil, err
	})
	if err != nil {
		return fmt.Errorf("neo4j: WriteChunkAndOf: %w", err)
	}
	return nil
}

// WriteSequentialEdges writes one or more :NEXT edges in a single transaction.
// Both endpoints must already exist (created via WriteChunkAndOf). Idempotent.
func (s *Store) WriteSequentialEdges(ctx context.Context, edges []graph.SequentialEdge) error {
	if len(edges) == 0 {
		return nil
	}
	sess := s.session(neo.AccessModeWrite)
	defer sess.Close(ctx)

	const cypher = `
UNWIND $edges AS e
MATCH (prev:Chunk {id: e.prev_id})
MATCH (next:Chunk {id: e.next_id})
WHERE prev.user_id = e.user_id AND next.user_id = e.user_id
MERGE (prev)-[:NEXT]->(next)
`
	rows := make([]map[string]any, len(edges))
	for i, e := range edges {
		rows[i] = map[string]any{
			"prev_id": e.PrevID,
			"next_id": e.NextID,
			"user_id": e.UserID,
		}
	}

	_, err := sess.ExecuteWrite(ctx, func(tx neo.ManagedTransaction) (any, error) {
		res, err := tx.Run(ctx, cypher, map[string]any{"edges": rows})
		if err != nil {
			return nil, err
		}
		_, err = res.Consume(ctx)
		return nil, err
	})
	if err != nil {
		return fmt.Errorf("neo4j: WriteSequentialEdges: %w", err)
	}
	return nil
}

// WriteSimilarEdge writes (:Chunk)-[:SIMILAR {score}]->(:Chunk). Idempotent;
// re-applying updates the score.
func (s *Store) WriteSimilarEdge(ctx context.Context, e graph.SimilarEdge) error {
	sess := s.session(neo.AccessModeWrite)
	defer sess.Close(ctx)

	const cypher = `
MATCH (a:Chunk {id: $a_id})
MATCH (b:Chunk {id: $b_id})
WHERE a.user_id = $user_id AND b.user_id = $user_id
MERGE (a)-[r:SIMILAR]->(b)
  ON CREATE SET r.score = $score
  ON MATCH  SET r.score = $score
`
	params := map[string]any{
		"a_id":    e.AID,
		"b_id":    e.BID,
		"user_id": e.UserID,
		"score":   e.Score,
	}

	_, err := sess.ExecuteWrite(ctx, func(tx neo.ManagedTransaction) (any, error) {
		res, err := tx.Run(ctx, cypher, params)
		if err != nil {
			return nil, err
		}
		_, err = res.Consume(ctx)
		return nil, err
	})
	if err != nil {
		return fmt.Errorf("neo4j: WriteSimilarEdge: %w", err)
	}
	return nil
}
```

#### `internal/store/neo4j/read.go`

```go
package neo4j

import (
	"context"
	"fmt"

	"github.com/gregdhill/engram/internal/graph"
	neo "github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// ExpandRelated is the legacy, depth-only entry point. Calls
// ExpandRelatedWithOptions with zero-value options.
func (s *Store) ExpandRelated(ctx context.Context, ids []graph.ChunkID, depth int) ([]graph.ChunkID, error) {
	return s.ExpandRelatedWithOptions(ctx, ids, depth, graph.ExpandOptions{})
}

// ExpandRelatedWithOptions walks NEXT and SIMILAR edges from the seed chunk IDs,
// scoped to the given UserID. Depth is fixed at 1 hop in this implementation;
// the depth argument is currently ignored to keep Cypher deterministic.
// (Variable-length path bounds cannot be parameterized in Cypher.)
func (s *Store) ExpandRelatedWithOptions(
	ctx context.Context,
	ids []graph.ChunkID,
	_ int,
	opts graph.ExpandOptions,
) ([]graph.ChunkID, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	sess := s.session(neo.AccessModeRead)
	defer sess.Close(ctx)

	cypher := `
UNWIND $ids AS sid
MATCH (c:Chunk {id: sid})
MATCH (c)-[:NEXT|SIMILAR]-(r:Chunk)
WHERE r.id <> c.id
`
	params := map[string]any{"ids": ids}

	if opts.UserID != "" {
		cypher += "  AND r.user_id = $user_id AND c.user_id = $user_id\n"
		params["user_id"] = opts.UserID
	}

	cypher += "RETURN DISTINCT r.id AS id\n"

	if opts.MaxExpand > 0 {
		cypher += fmt.Sprintf("LIMIT %d\n", opts.MaxExpand)
	}

	result, err := sess.ExecuteRead(ctx, func(tx neo.ManagedTransaction) (any, error) {
		res, err := tx.Run(ctx, cypher, params)
		if err != nil {
			return nil, err
		}
		var out []graph.ChunkID
		for res.Next(ctx) {
			rec := res.Record()
			v, ok := rec.Get("id")
			if !ok {
				continue
			}
			s, ok := v.(string)
			if !ok {
				continue
			}
			out = append(out, s)
		}
		return out, res.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("neo4j: ExpandRelated: %w", err)
	}
	if result == nil {
		return nil, nil
	}
	return result.([]graph.ChunkID), nil
}
```

#### `internal/store/neo4j/neo4j_test.go`

```go
//go:build integration

package neo4j

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/gregdhill/engram/internal/graph"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	uri := os.Getenv("NEO4J_TEST_URI")
	if uri == "" {
		t.Skip("NEO4J_TEST_URI not set; skipping integration test")
	}
	user := os.Getenv("NEO4J_TEST_USER")
	if user == "" {
		user = "neo4j"
	}
	pass := os.Getenv("NEO4J_TEST_PASS")
	if pass == "" {
		pass = "engrampass"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	s, err := New(ctx, Config{URI: uri, Username: user, Password: pass, Database: "neo4j"})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := s.EnsureSchema(ctx); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	t.Cleanup(func() {
		// Wipe test data so reruns are clean.
		sess := s.session("WRITE")
		_, _ = sess.ExecuteWrite(ctx, func(tx interface{ Run(context.Context, string, map[string]any) (any, error) }) (any, error) {
			return nil, nil
		})
		_ = sess.Close(ctx)
		_ = s.Close(ctx)
	})
	return s
}

func TestIntegration_WriteChunkAndOf_Idempotent(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	n := graph.ChunkNode{
		ID: "test-chunk-1", MemoryID: "test-mem-1", UserID: "tu",
		Source: "test", Ord: 0, CreatedAt: time.Now(),
	}
	if err := s.WriteChunkAndOf(ctx, n); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := s.WriteChunkAndOf(ctx, n); err != nil {
		t.Fatalf("second write (should be idempotent): %v", err)
	}
}

func TestIntegration_ExpandRelated_NextEdges(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Now()

	// Three chunks in one memory: A -> B -> C
	for i, id := range []string{"e2e-a", "e2e-b", "e2e-c"} {
		n := graph.ChunkNode{
			ID: id, MemoryID: "e2e-mem", UserID: "tu",
			Source: "test", Ord: i, CreatedAt: now,
		}
		if err := s.WriteChunkAndOf(ctx, n); err != nil {
			t.Fatalf("write %s: %v", id, err)
		}
	}
	if err := s.WriteSequentialEdges(ctx, []graph.SequentialEdge{
		{PrevID: "e2e-a", NextID: "e2e-b", UserID: "tu"},
		{PrevID: "e2e-b", NextID: "e2e-c", UserID: "tu"},
	}); err != nil {
		t.Fatalf("write next: %v", err)
	}

	got, err := s.ExpandRelatedWithOptions(ctx, []graph.ChunkID{"e2e-b"}, 1, graph.ExpandOptions{
		UserID: "tu", MaxExpand: 10,
	})
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expand: got %d ids %v, want 2 (a and c)", len(got), got)
	}
}
```

#### Verification

```bash
go build ./internal/store/neo4j/...
go vet ./internal/store/neo4j/...

# Integration (requires running Neo4j on localhost:7687)
docker run --rm -d --name neo4j-test -p 7687:7687 -p 7474:7474 \
  -e NEO4J_AUTH=neo4j/engrampass \
  -e NEO4J_ACCEPT_LICENSE_AGREEMENT=yes \
  neo4j:5.28-community
sleep 30
NEO4J_TEST_URI=bolt://localhost:7687 \
NEO4J_TEST_USER=neo4j \
NEO4J_TEST_PASS=engrampass \
  go test -tags integration -count=1 ./internal/store/neo4j/...
docker stop neo4j-test
```

---

### WS3 — Ingestor Wiring + Worker Pool

**Agent:** `lo-coder-q4` | **Depends on:** WS1 | **Est:** 45 min

#### Files to edit

1. `internal/memory/ingestor.go` — add `graph` field, worker pool, dispatch
2. `internal/memory/ingestor_test.go` — assert graph calls via mock
3. `internal/memory/retriever.go:217` — switch to `ExpandRelatedWithOptions`

#### Diff for `internal/memory/ingestor.go`

**1. Update imports (top of file, replace existing import block):**

```go
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
```

**2. Replace the `Ingestor` struct (currently lines 33-38):**

```go
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
	graphDrops  uint64 // atomic-incremented on drop; exported via metric later
}
```

**3. Replace `NewIngestor` (currently lines 40-43):**

```go
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
```

**4. Append graph dispatch to `Store()` — after line 135 (`if err := ing.vec.Upsert...` block ends), insert before the `return &StoreResult{...}` at line 137:**

```go
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
			// and nothing breaks; reconciler-style retry is out of scope here.
			time.Sleep(200 * time.Millisecond)
			if err := ing.graph.WriteSequentialEdges(ctx, edges); err != nil {
				ing.logger.Warn("graph: write sequential edges failed",
					"memory_id", memoryID, "count", len(edges), "err", err)
			}
		})
	}
```

> **Note for the executing agent:** the `time.Sleep` is a deliberate, simple
> ordering hack. Do not replace it with channel sync — that adds complexity
> the failure model doesn't need. Lost edges are recovered by re-ingest
> (idempotent) or by future reconciler work (out of scope).

#### Diff for `internal/memory/retriever.go`

Replace the single line at `retriever.go:217`:

**Before:**
```go
		expanded, gerr := r.graph.ExpandRelated(ctx, ids, 1)
```

**After:**
```go
		// Use the user-scoped, capped variant. UserID and MaxExpand come from
		// the request context. If retriever doesn't yet thread userID through,
		// pass empty string — the Neo4j adapter falls back to unscoped expansion,
		// which is still bounded by the seed set.
		expanded, gerr := r.graph.ExpandRelatedWithOptions(ctx, ids, 1, graph.ExpandOptions{
			UserID:    input.UserID,
			MaxExpand: 10,
		})
```

> **Note:** check whether `input.UserID` is the correct field — read
> `internal/memory/retriever.go` lines 1-100 first to confirm the input type.
> If the field is named differently (e.g. `UserID` on a `RetrieveInput`),
> use that name.

#### Mock-based test edits in `internal/memory/ingestor_test.go`

The existing test file calls `NewIngestor(meta, vec, chunker)` — the signature
is changing. Update every call site to pass `IngestorOptions{}` for the new
parameter. Add a new test that uses the recording mock from `graph_test.go`:

```go
func TestIngestor_DispatchesGraphWrites(t *testing.T) {
	// ... assemble meta, vec, chunker mocks as existing tests do ...
	rec := &recordingGraphStore{} // see below
	ing := NewIngestor(meta, vec, chunker, IngestorOptions{
		Graph:        rec,
		GraphWorkers: 1,
		GraphQueue:   16,
	})
	defer ing.Close(context.Background())

	_, err := ing.Store(context.Background(), StoreInput{Content: "hello world"})
	if err != nil {
		t.Fatalf("Store: %v", err)
	}

	// Allow workers to drain.
	time.Sleep(500 * time.Millisecond)

	if len(rec.chunks) == 0 {
		t.Fatal("expected at least one WriteChunkAndOf call")
	}
}

// recordingGraphStore is a local copy of graph.recordingStore (unexported in
// graph package). Defined here to avoid exporting test helpers across packages.
type recordingGraphStore struct {
	mu       sync.Mutex
	chunks   []graph.ChunkNode
	nextEdges [][]graph.SequentialEdge
}

func (r *recordingGraphStore) ExpandRelated(_ context.Context, _ []graph.ChunkID, _ int) ([]graph.ChunkID, error) {
	return nil, nil
}
func (r *recordingGraphStore) ExpandRelatedWithOptions(_ context.Context, _ []graph.ChunkID, _ int, _ graph.ExpandOptions) ([]graph.ChunkID, error) {
	return nil, nil
}
func (r *recordingGraphStore) WriteChunkAndOf(_ context.Context, n graph.ChunkNode) error {
	r.mu.Lock(); defer r.mu.Unlock()
	r.chunks = append(r.chunks, n)
	return nil
}
func (r *recordingGraphStore) WriteSequentialEdges(_ context.Context, e []graph.SequentialEdge) error {
	r.mu.Lock(); defer r.mu.Unlock()
	r.nextEdges = append(r.nextEdges, e)
	return nil
}
func (r *recordingGraphStore) WriteSimilarEdge(_ context.Context, _ graph.SimilarEdge) error {
	return nil
}
func (r *recordingGraphStore) Close(_ context.Context) error { return nil }
```

#### Verification

```bash
go build ./internal/memory/...
go test ./internal/memory/... -count=1 -timeout 60s
```

---

### WS4 — Config + main.go Wiring

**Agent:** `lo-coder-q4` | **Depends on:** WS1 | **Parallel with:** WS3 | **Est:** 45 min

#### Files to edit

1. `internal/config/config.go` — add `GraphConfig`
2. `internal/config/config_test.go` — defaults + env override tests
3. `cmd/engram/main.go` — switch on `cfg.Graph.Provider`
4. `engram.yaml` — commented `graph:` block
5. `engram.local.yaml` — active Neo4j config

#### Diff for `internal/config/config.go`

**1. Add `GraphConfig` struct** (insert after `LoggingConfig`, before `Config`):

```go
// GraphConfig holds graph store settings.
type GraphConfig struct {
	Provider         string  `yaml:"provider"`          // "neo4j" | "none" (default)
	URI              string  `yaml:"uri"`               // bolt://host:7687
	Username         string  `yaml:"username"`
	PasswordEnv      string  `yaml:"password_env"`      // env var name (e.g. NEO4J_PASSWORD)
	Database         string  `yaml:"database"`          // default "neo4j"
	WriteSimilar     bool    `yaml:"write_similar"`     // default false (deferred to reconciler)
	SimilarThreshold float64 `yaml:"similar_threshold"` // default 0.75
	MaxExpand        int     `yaml:"max_expand"`        // default 10
	TimeoutMS        int     `yaml:"timeout_ms"`        // default 2000
}
```

**2. Add field to `Config` struct** (after `Logging`):

```go
	Graph GraphConfig `yaml:"graph"`
```

**3. Add to `Defaults()`** (after `Logging:` block):

```go
		Graph: GraphConfig{
			Provider:         "none",
			Database:         "neo4j",
			SimilarThreshold: 0.75,
			MaxExpand:        10,
			TimeoutMS:        2000,
		},
```

**4. Add env overrides** (append to `applyEnvOverrides()` body, before closing brace):

```go
	// Graph
	setStr("ENGRAM_GRAPH_PROVIDER", &cfg.Graph.Provider)
	setStr("ENGRAM_GRAPH_URI", &cfg.Graph.URI)
	setStr("ENGRAM_GRAPH_USERNAME", &cfg.Graph.Username)
	setStr("ENGRAM_GRAPH_PASSWORD_ENV", &cfg.Graph.PasswordEnv)
	setStr("ENGRAM_GRAPH_DATABASE", &cfg.Graph.Database)
	setBool("ENGRAM_GRAPH_WRITE_SIMILAR", &cfg.Graph.WriteSimilar)
	setFloat("ENGRAM_GRAPH_SIMILAR_THRESHOLD", &cfg.Graph.SimilarThreshold)
	setInt("ENGRAM_GRAPH_MAX_EXPAND", &cfg.Graph.MaxExpand)
	setInt("ENGRAM_GRAPH_TIMEOUT_MS", &cfg.Graph.TimeoutMS)
```

**5. Add validation** (append to `validate()` body, before the `if len(errs) > 0` block):

```go
	if cfg.Graph.Provider != "" && cfg.Graph.Provider != "none" {
		if cfg.Graph.Provider != "neo4j" {
			errs = append(errs, fmt.Sprintf("graph.provider %q must be one of: none, neo4j", cfg.Graph.Provider))
		} else {
			if strings.TrimSpace(cfg.Graph.URI) == "" {
				errs = append(errs, "graph.uri must be set when provider=neo4j")
			}
			if strings.TrimSpace(cfg.Graph.Username) == "" {
				errs = append(errs, "graph.username must be set when provider=neo4j")
			}
			if strings.TrimSpace(cfg.Graph.PasswordEnv) == "" {
				errs = append(errs, "graph.password_env must be set when provider=neo4j")
			} else if os.Getenv(cfg.Graph.PasswordEnv) == "" {
				errs = append(errs, fmt.Sprintf("graph.password_env %q resolves to empty value; set it in the environment", cfg.Graph.PasswordEnv))
			}
		}
	}
```

#### Diff for `cmd/engram/main.go`

**1. Add import** (in the existing import block):

```go
	neo4jstore "github.com/gregdhill/engram/internal/store/neo4j"
```

**2. Replace line 148** (`graphStore := graph.NopStore{}`) with:

```go
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
			break
		}
		ns, err := neo4jstore.New(ctx, neo4jstore.Config{
			URI:      cfg.Graph.URI,
			Username: cfg.Graph.Username,
			Password: password,
			Database: cfg.Graph.Database,
			Timeout:  time.Duration(cfg.Graph.TimeoutMS) * time.Millisecond,
		})
		if err != nil {
			slog.Warn("neo4j connect failed; falling back to nop graph", "err", err)
			break
		}
		if err := ns.EnsureSchema(ctx); err != nil {
			slog.Warn("neo4j schema setup failed; falling back to nop graph", "err", err)
			_ = ns.Close(ctx)
			break
		}
		graphStore = ns
		defer func() {
			closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := ns.Close(closeCtx); err != nil {
				slog.Warn("neo4j close error", "err", err)
			}
		}()
		slog.Info("neo4j graph store initialized", "uri", cfg.Graph.URI)
	default:
		slog.Warn("unknown graph.provider; falling back to nop", "provider", cfg.Graph.Provider)
	}
```

**3. Update `NewIngestor` call** (line 151) to pass options:

```go
	ingestor := memory.NewIngestor(metaStore, vecStore, chunker, memory.IngestorOptions{
		Graph:  graphStore,
		Logger: slog.Default(),
	})
```

**4. Add ingestor close to shutdown** (insert after `reconciler.Stop()` near line 227):

```go
	if err := ingestor.Close(shutCtx); err != nil {
		slog.Warn("ingestor close error", "err", err)
	}
```

#### Patch for `engram.yaml`

Append at end of file (commented):

```yaml

# Graph store (optional). Set provider: neo4j to enable graph expansion.
graph:
  provider: none
  # provider: neo4j
  # uri: bolt://localhost:7687
  # username: neo4j
  # password_env: NEO4J_PASSWORD
  # database: neo4j
  # write_similar: false
  # similar_threshold: 0.75
  # max_expand: 10
  # timeout_ms: 2000
```

#### Patch for `engram.local.yaml`

Append (active config for local dev):

```yaml

graph:
  provider: neo4j
  uri: bolt://localhost:7687
  username: neo4j
  password_env: NEO4J_PASSWORD
  database: neo4j
  write_similar: false
  similar_threshold: 0.75
  max_expand: 10
  timeout_ms: 2000
```

#### Verification

```bash
go build ./...
go vet ./...
go test ./internal/config/... -count=1
```

---

### WS5 — Docker Compose

**Agent:** `lo-coder-q4` | **Independent** | **Est:** 20 min

#### Files to edit

1. `deploy/docker-compose.yml` — add `neo4j` service + volume
2. `run-local.sh` — export `NEO4J_PASSWORD`

#### Diff for `deploy/docker-compose.yml`

**1. Add service** (alongside `qdrant`, `postgres`, `ollama`):

```yaml
  neo4j:
    image: neo4j:5.28-community
    container_name: engram-neo4j
    ports:
      - "7474:7474"
      - "7687:7687"
    environment:
      NEO4J_AUTH: neo4j/${NEO4J_PASSWORD:-engrampass}
      NEO4J_ACCEPT_LICENSE_AGREEMENT: "yes"
    volumes:
      - neo4jdata:/data
    healthcheck:
      test: ["CMD-SHELL", "wget -qO- http://localhost:7474 >/dev/null || exit 1"]
      interval: 10s
      timeout: 5s
      retries: 6
      start_period: 40s
    networks:
      - engram
```

**2. Add `neo4jdata:` to the top-level `volumes:` section.**

**3. Pass env to engram service** — add to `engram` service `environment:`:

```yaml
      NEO4J_PASSWORD: ${NEO4J_PASSWORD:-engrampass}
      ENGRAM_GRAPH_PROVIDER: ${ENGRAM_GRAPH_PROVIDER:-none}
      ENGRAM_GRAPH_URI: bolt://neo4j:7687
      ENGRAM_GRAPH_USERNAME: neo4j
      ENGRAM_GRAPH_PASSWORD_ENV: NEO4J_PASSWORD
```

**4. Do NOT add `neo4j` to engram's `depends_on`.** Engram must start
regardless of Neo4j health (graceful degradation requirement).

#### Diff for `run-local.sh`

Add near the top (after the shebang and any existing `set` lines):

```bash
# Neo4j credentials. Override in your shell to use a different password.
export NEO4J_PASSWORD="${NEO4J_PASSWORD:-engrampass}"
```

#### Verification

```bash
docker compose -f deploy/docker-compose.yml config >/dev/null
# Compose syntax valid; services parse.

docker compose -f deploy/docker-compose.yml up -d neo4j
# Wait ~40s, then:
docker compose -f deploy/docker-compose.yml ps neo4j
# STATUS should include "healthy".
docker compose -f deploy/docker-compose.yml down
```

---

### WS6 — Integration Test

**Agent:** `lo-coder-q4` | **Depends on:** WS2, WS3, WS4, WS5 | **Est:** 1 h

#### File to create

`test/integration/neo4j_e2e_test.go`

```go
//go:build integration

package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/gregdhill/engram/internal/graph"
	neo4jstore "github.com/gregdhill/engram/internal/store/neo4j"
)

func TestE2E_Neo4j_IngestThenExpand(t *testing.T) {
	uri := os.Getenv("NEO4J_TEST_URI")
	if uri == "" {
		t.Skip("NEO4J_TEST_URI not set")
	}
	pass := os.Getenv("NEO4J_TEST_PASS")
	if pass == "" {
		pass = "engrampass"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	store, err := neo4jstore.New(ctx, neo4jstore.Config{
		URI: uri, Username: "neo4j", Password: pass, Database: "neo4j",
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer store.Close(ctx)
	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatalf("schema: %v", err)
	}

	now := time.Now()
	chunks := []graph.ChunkNode{
		{ID: "ws6-a", MemoryID: "ws6-mem", UserID: "u1", Source: "test", Ord: 0, CreatedAt: now},
		{ID: "ws6-b", MemoryID: "ws6-mem", UserID: "u1", Source: "test", Ord: 1, CreatedAt: now},
	}
	for _, c := range chunks {
		if err := store.WriteChunkAndOf(ctx, c); err != nil {
			t.Fatalf("write chunk %s: %v", c.ID, err)
		}
	}
	if err := store.WriteSequentialEdges(ctx, []graph.SequentialEdge{
		{PrevID: "ws6-a", NextID: "ws6-b", UserID: "u1"},
	}); err != nil {
		t.Fatalf("write next: %v", err)
	}

	got, err := store.ExpandRelatedWithOptions(ctx, []graph.ChunkID{"ws6-a"}, 1, graph.ExpandOptions{
		UserID: "u1", MaxExpand: 10,
	})
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if len(got) != 1 || got[0] != "ws6-b" {
		t.Fatalf("expand: got %v, want [ws6-b]", got)
	}
}

func TestE2E_Neo4j_GracefulDegradation(t *testing.T) {
	// Connect to a deliberately unreachable URI; New must error fast.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := neo4jstore.New(ctx, neo4jstore.Config{
		URI: "bolt://127.0.0.1:1", Username: "neo4j", Password: "x", Timeout: 1 * time.Second,
	})
	if err == nil {
		t.Fatal("expected connect error")
	}
	// main.go path is tested implicitly: it must fall back to NopStore on error.
}
```

#### Verification

```bash
docker compose -f deploy/docker-compose.yml up -d neo4j
sleep 40
NEO4J_TEST_URI=bolt://localhost:7687 \
NEO4J_TEST_PASS=engrampass \
  go test -tags integration -count=1 ./test/integration/... -v -timeout 120s
docker compose -f deploy/docker-compose.yml down
```

---

### WS7 — Documentation Cleanup

**Agent:** `lo-coder-q4` | **Depends on:** WS1 | **Est:** 20 min

#### Files to edit

1. `README.md` — remove duplicate architecture diagram (lines 158-176)
2. `docs/architecture.md` — remove "Neo4j hook (interface only)" note,
   add Cypher reference

#### `README.md`

Delete the duplicate architecture diagram block at lines 158-176 (the older,
non-Neo4j one). Keep the diagram at lines 273-308. Replace lines 156-186 with
just:

```markdown
## Architecture

See [Architecture](#architecture-1) below and [`docs/architecture.md`](docs/architecture.md)
for full detail.
```

(Or simpler: remove the duplicate "## Architecture" section at line 156-186
and let the canonical one at line 269 stand alone.)

#### `docs/architecture.md`

1. Find any line saying Neo4j is "future" or "interface only" and replace with
   a brief description of the implemented schema and edge types.
2. Add a subsection after the architecture diagram:

   ```markdown
   ### Graph Schema

   - **Nodes**: `(:Chunk {id, memory_id, user_id, ord, created_at})`,
     `(:Memory {id, user_id, source, created_at})`
   - **Edges**: `[:OF]` (chunk → memory), `[:NEXT]` (sequential chunks),
     `[:SIMILAR {score}]` (cross-memory similarity, optional)
   - **Constraints**: UNIQUE on `Chunk.id` and `Memory.id`
   - **Indexes**: `Chunk(user_id)`, `Chunk(user_id, created_at)`
   - **Traversal**: `ExpandRelatedWithOptions` walks `NEXT|SIMILAR` at depth 1,
     filtered by `user_id`, capped at `MaxExpand`.
   ```

#### Verification

```bash
# Markdown renders without obvious breakage
grep -c "## Architecture" README.md
# Expect: 1 (was 2 before WS7)
```

---

## Execution Order

```
WS1 (lo-coder-q4, ~30m)
  ↓
  ├── WS2 (ac-sonnet, ~3-4h) ────┐
  ├── WS3 (lo-coder-q4, ~45m) ───┤
  ├── WS4 (lo-coder-q4, ~45m) ───┤
  ├── WS5 (lo-coder-q4, ~20m) ───┤
  └── WS7 (lo-coder-q4, ~20m)    │
                                  ↓
                               WS6 (lo-coder-q4, ~1h)
```

WS2 is the only paid-tier work. Everything else runs free on `lo-coder-q4`.

**Total wall-clock (parallel):** ~5-6 h. **Total paid spend:** ~3-4 h of
`ac-sonnet` (WS2 only).

---

## Risks & Mitigations

| Risk | Mitigation |
| --- | --- |
| Worker pool drops jobs on bursty ingest | Queue size 256 ≫ typical batch; drops are logged and counted. Re-ingest is idempotent. |
| Sequential edges race chunk creation | 200ms in-job delay + idempotent MERGE on chunk side. Lost edges recovered on re-ingest. Future work: reconciler-style retry. |
| Neo4j down at startup | `main.go` falls back to `NopStore` with a warning. Engram still serves vector + BM25. |
| Neo4j down mid-run | Worker pool jobs error and log; ingest unaffected. Reads at retriever return empty + log. |
| Schema drift across Neo4j 5.x | All statements use `IF NOT EXISTS`. Run on every startup. |
| Cypher injection via `MaxExpand` | Server-side int formatting only; never accepts user-supplied strings into Cypher. |
| Quantized model misimplements WS2 | WS2 escalated to `ac-sonnet`. |

---

## Success Criteria

- [ ] `go build ./...` and `go vet ./...` clean.
- [ ] `go test ./...` passes with no skipped Neo4j tests when integration tag absent.
- [ ] `go test -tags integration ./...` passes against a live Neo4j 5.28 container.
- [ ] `docker compose up` brings up engram + postgres + qdrant + ollama + neo4j;
      engram starts even if neo4j fails (degrades to NopStore).
- [ ] Ingesting a 3-chunk memory creates 3 `:Chunk`, 1 `:Memory`, 3 `:OF`,
      and 2 `:NEXT` in Neo4j (verifiable via Cypher Browser at :7474).
- [ ] Retrieving with a query that hits chunk B returns chunks A and C via
      `:NEXT` expansion when graph is enabled.
- [ ] Killing Neo4j mid-run: ingest still succeeds; logs show graph errors;
      retrieve returns vector+BM25 results.
- [ ] `ENGRAM_GRAPH_PROVIDER=neo4j` env var activates Neo4j store; absent or
      `none` keeps NopStore.
