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
