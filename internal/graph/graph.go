// Package graph defines the GraphStore interface for graph-based memory expansion.
// The interface is designed so that adding a Neo4j implementation later
// requires zero changes to the Retriever.
package graph

import "context"

// ChunkID is a UUID string identifying a memory chunk.
type ChunkID = string

// GraphStore expands a set of chunk IDs to related chunks via graph traversal.
type GraphStore interface {
	// ExpandRelated returns chunk IDs related to the given seeds up to depth hops.
	// Returns an empty slice (not an error) when no relations exist.
	ExpandRelated(ctx context.Context, chunkIDs []ChunkID, depth int) ([]ChunkID, error)
}
