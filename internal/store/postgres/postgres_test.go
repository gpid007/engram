package postgres_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gregdhill/engram/internal/store/postgres"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func setupStore(t *testing.T) (*postgres.Store, func()) {
	t.Helper()
	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		Image:        "postgres:16",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_USER":     "engram",
			"POSTGRES_PASSWORD": "engram",
			"POSTGRES_DB":       "engram",
		},
		WaitingFor: wait.ForListeningPort("5432/tcp"),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start container: %v", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("container host: %v", err)
	}
	port, err := container.MappedPort(ctx, "5432")
	if err != nil {
		t.Fatalf("container port: %v", err)
	}
	dsn := fmt.Sprintf("postgres://engram:engram@%s:%s/engram?sslmode=disable", host, port.Port())

	store, err := postgres.New(ctx, dsn)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	cleanup := func() {
		_ = store.Close()
		_ = container.Terminate(ctx)
	}
	return store, cleanup
}

func TestMigrationsIdempotent(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupStore(t)
	defer cleanup()

	// Second call to Migrate should be a no-op (all migrations already applied).
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
}

func TestSaveMemoryAndChunks(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupStore(t)
	defer cleanup()

	mem := &postgres.Memory{
		ID:          uuid.New().String(),
		UserID:      "user1",
		Source:      "test",
		Content:     "The quick brown fox jumps over the lazy dog",
		Metadata:    map[string]any{"k": "v"},
		Importance:  0.8,
		CreatedAt:   time.Now(),
		AccessedAt:  time.Now(),
		AccessCount: 0,
	}
	if err := store.SaveMemory(ctx, mem); err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}

	h := sha256.Sum256([]byte(mem.Content))
	chunk := &postgres.Chunk{
		ID:          uuid.New().String(),
		MemoryID:    mem.ID,
		UserID:      "user1",
		Ord:         0,
		Content:     mem.Content,
		ContentHash: h[:],
		CreatedAt:   time.Now(),
	}
	if err := store.SaveChunks(ctx, []*postgres.Chunk{chunk}); err != nil {
		t.Fatalf("SaveChunks: %v", err)
	}
}

func TestSaveChunksDedup(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupStore(t)
	defer cleanup()

	mem := &postgres.Memory{
		ID:         uuid.New().String(),
		UserID:     "user2",
		Content:    "Duplicate content here",
		Metadata:   map[string]any{},
		Importance: 0.5,
		CreatedAt:  time.Now(),
		AccessedAt: time.Now(),
	}
	if err := store.SaveMemory(ctx, mem); err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}

	h := sha256.Sum256([]byte(mem.Content))
	chunk := &postgres.Chunk{
		ID:          uuid.New().String(),
		MemoryID:    mem.ID,
		UserID:      "user2",
		Ord:         0,
		Content:     mem.Content,
		ContentHash: h[:],
		CreatedAt:   time.Now(),
	}
	// First insert: should succeed.
	if err := store.SaveChunks(ctx, []*postgres.Chunk{chunk}); err != nil {
		t.Fatalf("first SaveChunks: %v", err)
	}

	// Second insert with the same content_hash: should return ErrDuplicate.
	chunk2 := &postgres.Chunk{
		ID:          uuid.New().String(), // different ID
		MemoryID:    mem.ID,
		UserID:      "user2",
		Ord:         1,
		Content:     mem.Content,
		ContentHash: h[:], // same hash → duplicate
		CreatedAt:   time.Now(),
	}
	err := store.SaveChunks(ctx, []*postgres.Chunk{chunk2})
	if err == nil {
		t.Fatal("expected ErrDuplicate, got nil")
	}
	if err != postgres.ErrDuplicate {
		t.Fatalf("expected ErrDuplicate, got %v", err)
	}
}

func TestSearchBM25(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupStore(t)
	defer cleanup()

	mem := &postgres.Memory{
		ID:         uuid.New().String(),
		UserID:     "user3",
		Source:     "wiki",
		Content:    "Elephants are the largest land animals on Earth",
		Metadata:   map[string]any{},
		Importance: 0.9,
		CreatedAt:  time.Now(),
		AccessedAt: time.Now(),
	}
	if err := store.SaveMemory(ctx, mem); err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}

	h := sha256.Sum256([]byte(mem.Content))
	chunk := &postgres.Chunk{
		ID:          uuid.New().String(),
		MemoryID:    mem.ID,
		UserID:      "user3",
		Ord:         0,
		Content:     mem.Content,
		ContentHash: h[:],
		CreatedAt:   time.Now(),
	}
	if err := store.SaveChunks(ctx, []*postgres.Chunk{chunk}); err != nil {
		t.Fatalf("SaveChunks: %v", err)
	}

	results, err := store.SearchBM25(ctx, "user3", "elephants land animals", 10)
	if err != nil {
		t.Fatalf("SearchBM25: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one BM25 result")
	}
	if results[0].ChunkID != chunk.ID {
		t.Errorf("expected chunk ID %s, got %s", chunk.ID, results[0].ChunkID)
	}
}

func TestGetUserState(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupStore(t)
	defer cleanup()

	userID := "user4"
	for i := 0; i < 3; i++ {
		mem := &postgres.Memory{
			ID:         uuid.New().String(),
			UserID:     userID,
			Source:     fmt.Sprintf("src%d", i),
			Content:    fmt.Sprintf("Memory content number %d about something unique", i),
			Metadata:   map[string]any{},
			Importance: 0.5,
			CreatedAt:  time.Now(),
			AccessedAt: time.Now(),
		}
		if err := store.SaveMemory(ctx, mem); err != nil {
			t.Fatalf("SaveMemory %d: %v", i, err)
		}
		h := sha256.Sum256([]byte(mem.Content))
		chunk := &postgres.Chunk{
			ID:          uuid.New().String(),
			MemoryID:    mem.ID,
			UserID:      userID,
			Ord:         0,
			Content:     mem.Content,
			ContentHash: h[:],
			CreatedAt:   time.Now(),
		}
		if err := store.SaveChunks(ctx, []*postgres.Chunk{chunk}); err != nil {
			t.Fatalf("SaveChunks %d: %v", i, err)
		}
	}

	state, err := store.GetUserState(ctx, userID)
	if err != nil {
		t.Fatalf("GetUserState: %v", err)
	}
	if state.MemoryCount != 3 {
		t.Errorf("expected 3 memories, got %d", state.MemoryCount)
	}
	if state.ChunkCount != 3 {
		t.Errorf("expected 3 chunks, got %d", state.ChunkCount)
	}
	if state.FirstMemory == nil {
		t.Error("expected FirstMemory to be non-nil")
	}
	if state.LastMemory == nil {
		t.Error("expected LastMemory to be non-nil")
	}
	if len(state.TopSources) != 3 {
		t.Errorf("expected 3 top sources, got %d", len(state.TopSources))
	}
}

func TestPendingVectorsRoundtrip(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupStore(t)
	defer cleanup()

	// Need a real chunk in DB before we can add to pending_vectors (FK).
	mem := &postgres.Memory{
		ID:         uuid.New().String(),
		UserID:     "user5",
		Content:    "Pending vector test content",
		Metadata:   map[string]any{},
		Importance: 0.5,
		CreatedAt:  time.Now(),
		AccessedAt: time.Now(),
	}
	if err := store.SaveMemory(ctx, mem); err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}
	h := sha256.Sum256([]byte(mem.Content))
	chunk := &postgres.Chunk{
		ID:          uuid.New().String(),
		MemoryID:    mem.ID,
		UserID:      "user5",
		Ord:         0,
		Content:     mem.Content,
		ContentHash: h[:],
		CreatedAt:   time.Now(),
	}
	if err := store.SaveChunks(ctx, []*postgres.Chunk{chunk}); err != nil {
		t.Fatalf("SaveChunks: %v", err)
	}

	if err := store.EnqueuePending(ctx, chunk.ID); err != nil {
		t.Fatalf("EnqueuePending: %v", err)
	}

	pending, err := store.DrainPending(ctx, 10)
	if err != nil {
		t.Fatalf("DrainPending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending item, got %d", len(pending))
	}
	if pending[0].ChunkID != chunk.ID {
		t.Errorf("expected chunk ID %s, got %s", chunk.ID, pending[0].ChunkID)
	}

	if err := store.DeletePending(ctx, chunk.ID); err != nil {
		t.Fatalf("DeletePending: %v", err)
	}

	after, err := store.DrainPending(ctx, 10)
	if err != nil {
		t.Fatalf("DrainPending after delete: %v", err)
	}
	if len(after) != 0 {
		t.Errorf("expected 0 pending items after delete, got %d", len(after))
	}
}
