//go:build integration

package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/gregdhill/engram/internal/graph"
	neo4jstore "github.com/gregdhill/engram/internal/store/neo4j"
)

func TestE2E_Neo4j_IngestThenExpand(t *testing.T) {
	uri := os.Getenv("NEO4J_TEST_URI")
	if uri == "" {
		t.Skip("NEO4J_TEST_URI not set")
	}
	pass := os.Getenv("NEO4J_TEST_PASS")
	if pass == "" {
		pass = "engrampass"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	store, err := neo4jstore.New(ctx, neo4jstore.Config{
		URI: uri, Username: "neo4j", Password: pass, Database: "neo4j",
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer store.Close(ctx)
	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatalf("schema: %v", err)
	}

	now := time.Now()
	chunks := []graph.ChunkNode{
		{ID: "ws6-a", MemoryID: "ws6-mem", UserID: "u1", Source: "test", Ord: 0, CreatedAt: now},
		{ID: "ws6-b", MemoryID: "ws6-mem", UserID: "u1", Source: "test", Ord: 1, CreatedAt: now},
	}
	for _, c := range chunks {
		if err := store.WriteChunkAndOf(ctx, c); err != nil {
			t.Fatalf("write chunk %s: %v", c.ID, err)
		}
	}
	if err := store.WriteSequentialEdges(ctx, []graph.SequentialEdge{
		{PrevID: "ws6-a", NextID: "ws6-b", UserID: "u1"},
	}); err != nil {
		t.Fatalf("write next: %v", err)
	}

	got, err := store.ExpandRelatedWithOptions(ctx, []graph.ChunkID{"ws6-a"}, 1, graph.ExpandOptions{
		UserID: "u1", MaxExpand: 10,
	})
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if len(got) != 1 || got[0] != "ws6-b" {
		t.Fatalf("expand: got %v, want [ws6-b]", got)
	}
}

func TestE2E_Neo4j_GracefulDegradation(t *testing.T) {
	// Connect to a deliberately unreachable URI; New must error fast.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := neo4jstore.New(ctx, neo4jstore.Config{
		URI: "bolt://127.0.0.1:1", Username: "neo4j", Password: "x", Timeout: 1 * time.Second,
	})
	if err == nil {
		t.Fatal("expected connect error")
	}
	// main.go path is tested implicitly: it must fall back to NopStore on error.
}
