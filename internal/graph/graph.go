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
