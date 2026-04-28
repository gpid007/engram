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
			str, ok := v.(string)
			if !ok {
				continue
			}
			out = append(out, str)
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
