package memory

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gregdhill/engram/internal/chunk"
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

// Ingestor orchestrates memory ingestion: chunk → metadata → vector.
type Ingestor struct {
	meta    postgres.MetaStore
	vec     qdrant.VectorStore
	chunker *chunk.Chunker
}

// NewIngestor constructs an Ingestor.
func NewIngestor(meta postgres.MetaStore, vec qdrant.VectorStore, chunker *chunk.Chunker) *Ingestor {
	return &Ingestor{meta: meta, vec: vec, chunker: chunker}
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
