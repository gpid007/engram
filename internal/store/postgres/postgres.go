// Package postgres implements the MetaStore interface.
package postgres

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// ErrDuplicate is returned when all chunks in a batch already exist (dedup by content_hash).
var ErrDuplicate = errors.New("duplicate chunk")

// Memory represents a stored memory record.
type Memory struct {
	ID          string
	UserID      string
	Source      string
	Content     string
	Metadata    map[string]any
	Importance  float32
	CreatedAt   time.Time
	AccessedAt  time.Time
	AccessCount int
}

// Chunk represents a chunk of a memory for BM25/vector indexing.
type Chunk struct {
	ID          string
	MemoryID    string
	UserID      string
	Ord         int
	Content     string
	ContentHash []byte
	CreatedAt   time.Time
}

// BM25Result is a single result from a BM25 full-text search.
type BM25Result struct {
	ChunkID  string
	MemoryID string
	Content  string
	Rank     float64
}

// UserState holds aggregate statistics for a user.
type UserState struct {
	MemoryCount int
	ChunkCount  int
	FirstMemory *time.Time
	LastMemory  *time.Time
	TopSources  []string
}

// PendingVector represents a chunk awaiting vector embedding.
type PendingVector struct {
	ChunkID    string
	Attempts   int
	LastError  string
	EnqueuedAt time.Time
}

// MetaStore is the interface for persistent metadata operations.
type MetaStore interface {
	SaveMemory(ctx context.Context, m *Memory) error
	SaveChunks(ctx context.Context, chunks []*Chunk) error
	SearchBM25(ctx context.Context, userID, query string, k int) ([]BM25Result, error)
	GetUserState(ctx context.Context, userID string) (*UserState, error)
	EnqueuePending(ctx context.Context, chunkID string) error
	DrainPending(ctx context.Context, limit int) ([]*PendingVector, error)
	DeletePending(ctx context.Context, chunkID string) error
	Close() error
}

// Store implements MetaStore using a PostgreSQL connection pool.
type Store struct {
	pool       *pgxpool.Pool
	collection string // reserved for future use
}

// New creates a new Store, connects the pool, and runs migrations.
func New(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	s := &Store{pool: pool}
	if err := s.Migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

// Migrate applies any unapplied migration SQL files in lexicographic order.
func (s *Store) Migrate(ctx context.Context) error {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return err
	}

	// Collect and sort filenames.
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	for _, name := range files {
		tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			return err
		}

		// Ensure schema_migrations exists before querying it.
		// The first migration creates it; use CREATE TABLE IF NOT EXISTS
		// directly here as a bootstrap guard.
		if _, err := tx.Exec(ctx, `
			CREATE TABLE IF NOT EXISTS schema_migrations (
				version    TEXT PRIMARY KEY,
				applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
			)`); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}

		var count int
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM schema_migrations WHERE version=$1`, name,
		).Scan(&count); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if count > 0 {
			_ = tx.Rollback(ctx)
			continue
		}

		data, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if _, err := tx.Exec(ctx, string(data)); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO schema_migrations(version) VALUES($1)`, name,
		); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

// SaveMemory inserts a memory or updates accessed_at/access_count on conflict.
func (s *Store) SaveMemory(ctx context.Context, m *Memory) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO memories(id, user_id, source, content, metadata, importance, created_at, accessed_at, access_count)
		VALUES($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (id) DO UPDATE
			SET accessed_at   = now(),
			    access_count  = memories.access_count + 1`,
		m.ID, m.UserID, m.Source, m.Content, m.Metadata, m.Importance,
		m.CreatedAt, m.AccessedAt, m.AccessCount,
	)
	return err
}

// SaveChunks inserts chunks, skipping duplicates by (user_id, content_hash).
// Returns ErrDuplicate if every chunk was a duplicate (nothing inserted).
func (s *Store) SaveChunks(ctx context.Context, chunks []*Chunk) error {
	if len(chunks) == 0 {
		return nil
	}
	inserted := 0
	for _, c := range chunks {
		tag, err := s.pool.Exec(ctx, `
			INSERT INTO chunks(id, memory_id, user_id, ord, content, content_hash, created_at)
			VALUES($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (user_id, content_hash) DO NOTHING`,
			c.ID, c.MemoryID, c.UserID, c.Ord, c.Content, c.ContentHash, c.CreatedAt,
		)
		if err != nil {
			return err
		}
		inserted += int(tag.RowsAffected())
	}
	if inserted == 0 {
		return ErrDuplicate
	}
	return nil
}

// SearchBM25 performs a full-text search over chunks for the given user.
func (s *Store) SearchBM25(ctx context.Context, userID, query string, k int) ([]BM25Result, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, memory_id, content,
		       ts_rank(tsv, plainto_tsquery('english', $2)) AS rank
		FROM   chunks
		WHERE  user_id = $1
		  AND  tsv @@ plainto_tsquery('english', $2)
		ORDER BY rank DESC
		LIMIT $3`,
		userID, query, k,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []BM25Result
	for rows.Next() {
		var r BM25Result
		if err := rows.Scan(&r.ChunkID, &r.MemoryID, &r.Content, &r.Rank); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// GetUserState returns aggregate statistics for a user.
func (s *Store) GetUserState(ctx context.Context, userID string) (*UserState, error) {
	state := &UserState{}

	// Memory count, chunk count, first/last memory.
	err := s.pool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM memories WHERE user_id=$1),
			(SELECT COUNT(*) FROM chunks   WHERE user_id=$1),
			(SELECT MIN(created_at) FROM memories WHERE user_id=$1),
			(SELECT MAX(created_at) FROM memories WHERE user_id=$1)`,
		userID,
	).Scan(&state.MemoryCount, &state.ChunkCount, &state.FirstMemory, &state.LastMemory)
	if err != nil {
		return nil, err
	}

	// Top 5 sources (non-null) by count.
	rows, err := s.pool.Query(ctx, `
		SELECT source
		FROM   memories
		WHERE  user_id=$1 AND source IS NOT NULL
		GROUP BY source
		ORDER BY COUNT(*) DESC
		LIMIT 5`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var src string
		if err := rows.Scan(&src); err != nil {
			return nil, err
		}
		state.TopSources = append(state.TopSources, src)
	}
	return state, rows.Err()
}

// EnqueuePending inserts a chunk ID into the pending_vectors queue.
func (s *Store) EnqueuePending(ctx context.Context, chunkID string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO pending_vectors(chunk_id)
		VALUES($1)
		ON CONFLICT DO NOTHING`,
		chunkID,
	)
	return err
}

// DrainPending returns up to limit pending vectors, locking them for processing.
func (s *Store) DrainPending(ctx context.Context, limit int) ([]*PendingVector, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT chunk_id, attempts, COALESCE(last_error, ''), enqueued_at
		FROM   pending_vectors
		ORDER BY enqueued_at
		LIMIT  $1
		FOR UPDATE SKIP LOCKED`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*PendingVector
	for rows.Next() {
		pv := &PendingVector{}
		if err := rows.Scan(&pv.ChunkID, &pv.Attempts, &pv.LastError, &pv.EnqueuedAt); err != nil {
			return nil, err
		}
		result = append(result, pv)
	}
	return result, rows.Err()
}

// DeletePending removes a chunk from the pending_vectors queue.
func (s *Store) DeletePending(ctx context.Context, chunkID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM pending_vectors WHERE chunk_id=$1`, chunkID)
	return err
}

// Close closes the underlying connection pool.
func (s *Store) Close() error {
	s.pool.Close()
	return nil
}
