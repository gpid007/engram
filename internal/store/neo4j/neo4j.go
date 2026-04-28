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
