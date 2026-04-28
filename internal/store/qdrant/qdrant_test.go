package qdrant_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	qdrantstore "github.com/gregdhill/engram/internal/store/qdrant"
)

const (
	testCollection = "test_memories"
	testDim        = uint64(4)
)

// startQdrant spins up a real Qdrant container and returns its gRPC address.
func startQdrant(t *testing.T) string {
	t.Helper()
	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		Image:        "qdrant/qdrant:latest",
		ExposedPorts: []string{"6334/tcp"},
		WaitingFor:   wait.ForListeningPort("6334/tcp"),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start qdrant container: %v", err)
	}
	t.Cleanup(func() {
		if err := container.Terminate(ctx); err != nil {
			t.Logf("terminate container: %v", err)
		}
	})

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("get container host: %v", err)
	}
	port, err := container.MappedPort(ctx, "6334/tcp")
	if err != nil {
		t.Fatalf("get mapped port: %v", err)
	}

	return fmt.Sprintf("%s:%s", host, port.Port())
}

// newStore creates a Store connected to the given addr and closes it when done.
func newStore(t *testing.T, addr string) *qdrantstore.Store {
	t.Helper()
	ctx := context.Background()
	s, err := qdrantstore.New(ctx, addr, testCollection)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestEnsureCollection_Idempotent verifies calling EnsureCollection twice doesn't error.
func TestEnsureCollection_Idempotent(t *testing.T) {
	addr := startQdrant(t)
	s := newStore(t, addr)
	ctx := context.Background()

	if err := s.EnsureCollection(ctx, testDim); err != nil {
		t.Fatalf("first EnsureCollection: %v", err)
	}
	if err := s.EnsureCollection(ctx, testDim); err != nil {
		t.Fatalf("second EnsureCollection (idempotent): %v", err)
	}
}

// TestEnsureCollection_DimMismatch verifies that specifying a different dim
// on an existing collection returns an error.
func TestEnsureCollection_DimMismatch(t *testing.T) {
	addr := startQdrant(t)
	s := newStore(t, addr)
	ctx := context.Background()

	if err := s.EnsureCollection(ctx, testDim); err != nil {
		t.Fatalf("EnsureCollection: %v", err)
	}
	if err := s.EnsureCollection(ctx, testDim+1); err == nil {
		t.Fatal("expected error for dimension mismatch, got nil")
	}
}

// TestUpsertAndSearch verifies that upserting points and then searching
// returns the correct IDs.
func TestUpsertAndSearch(t *testing.T) {
	addr := startQdrant(t)
	s := newStore(t, addr)
	ctx := context.Background()

	if err := s.EnsureCollection(ctx, testDim); err != nil {
		t.Fatalf("EnsureCollection: %v", err)
	}

	points := []qdrantstore.Point{
		{
			ID:     "11111111-1111-1111-1111-111111111111",
			Vector: []float32{1, 0, 0, 0},
			Payload: map[string]any{
				"user_id":   "alice",
				"memory_id": "m1",
			},
		},
		{
			ID:     "22222222-2222-2222-2222-222222222222",
			Vector: []float32{0, 1, 0, 0},
			Payload: map[string]any{
				"user_id":   "alice",
				"memory_id": "m2",
			},
		},
	}

	if err := s.Upsert(ctx, points); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	results, err := s.Search(ctx, []float32{1, 0, 0, 0}, 10, "alice")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	// Top result should be the one closest to the query.
	if results[0].ID != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("expected top result ID %q, got %q",
			"11111111-1111-1111-1111-111111111111", results[0].ID)
	}
}

// TestSearch_UserIDFilter verifies that the user_id filter correctly scopes results.
func TestSearch_UserIDFilter(t *testing.T) {
	addr := startQdrant(t)
	s := newStore(t, addr)
	ctx := context.Background()

	if err := s.EnsureCollection(ctx, testDim); err != nil {
		t.Fatalf("EnsureCollection: %v", err)
	}

	points := []qdrantstore.Point{
		{
			ID:     "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
			Vector: []float32{1, 0, 0, 0},
			Payload: map[string]any{"user_id": "alice"},
		},
		{
			ID:     "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
			Vector: []float32{0.9, 0.1, 0, 0},
			Payload: map[string]any{"user_id": "alice"},
		},
		{
			ID:     "cccccccc-cccc-cccc-cccc-cccccccccccc",
			Vector: []float32{1, 0, 0, 0},
			Payload: map[string]any{"user_id": "bob"},
		},
	}

	if err := s.Upsert(ctx, points); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	results, err := s.Search(ctx, []float32{1, 0, 0, 0}, 10, "alice")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results for alice, got %d", len(results))
	}
	for _, r := range results {
		uid, _ := r.Payload["user_id"].(string)
		if uid != "alice" {
			t.Errorf("expected user_id=alice, got %q (point %s)", uid, r.ID)
		}
	}
}
