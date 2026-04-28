//go:build integration

package neo4j

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/gregdhill/engram/internal/graph"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	uri := os.Getenv("NEO4J_TEST_URI")
	if uri == "" {
		t.Skip("NEO4J_TEST_URI not set; skipping integration test")
	}
	user := os.Getenv("NEO4J_TEST_USER")
	if user == "" {
		user = "neo4j"
	}
	pass := os.Getenv("NEO4J_TEST_PASS")
	if pass == "" {
		pass = "engrampass"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	s, err := New(ctx, Config{URI: uri, Username: user, Password: pass, Database: "neo4j"})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := s.EnsureSchema(ctx); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	t.Cleanup(func() {
		_ = s.Close(context.Background())
	})
	return s
}

func TestIntegration_WriteChunkAndOf_Idempotent(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	n := graph.ChunkNode{
		ID: "test-chunk-1", MemoryID: "test-mem-1", UserID: "tu",
		Source: "test", Ord: 0, CreatedAt: time.Now(),
	}
	if err := s.WriteChunkAndOf(ctx, n); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := s.WriteChunkAndOf(ctx, n); err != nil {
		t.Fatalf("second write (should be idempotent): %v", err)
	}
}

func TestIntegration_ExpandRelated_NextEdges(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Now()

	// Three chunks in one memory: A -> B -> C
	for i, id := range []string{"e2e-a", "e2e-b", "e2e-c"} {
		n := graph.ChunkNode{
			ID: id, MemoryID: "e2e-mem", UserID: "tu",
			Source: "test", Ord: i, CreatedAt: now,
		}
		if err := s.WriteChunkAndOf(ctx, n); err != nil {
			t.Fatalf("write %s: %v", id, err)
		}
	}
	if err := s.WriteSequentialEdges(ctx, []graph.SequentialEdge{
		{PrevID: "e2e-a", NextID: "e2e-b", UserID: "tu"},
		{PrevID: "e2e-b", NextID: "e2e-c", UserID: "tu"},
	}); err != nil {
		t.Fatalf("write next: %v", err)
	}

	got, err := s.ExpandRelatedWithOptions(ctx, []graph.ChunkID{"e2e-b"}, 1, graph.ExpandOptions{
		UserID: "tu", MaxExpand: 10,
	})
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expand: got %d ids %v, want 2 (a and c)", len(got), got)
	}
}
