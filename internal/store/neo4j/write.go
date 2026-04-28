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
