package memory

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gregdhill/engram/internal/store/postgres"
	"github.com/gregdhill/engram/internal/store/qdrant"
)

// --- mock MetaStore ---

type mockMetaStore struct {
	drainFn  func(ctx context.Context, limit int) ([]*postgres.PendingVector, error)
	deleteFn func(ctx context.Context, chunkID string) error
}

func (m *mockMetaStore) DrainPending(ctx context.Context, limit int) ([]*postgres.PendingVector, error) {
	if m.drainFn != nil {
		return m.drainFn(ctx, limit)
	}
	return nil, nil
}

func (m *mockMetaStore) DeletePending(ctx context.Context, chunkID string) error {
	if m.deleteFn != nil {
		return m.deleteFn(ctx, chunkID)
	}
	return nil
}

func (m *mockMetaStore) SaveMemory(ctx context.Context, mem *postgres.Memory) error { return nil }
func (m *mockMetaStore) SaveChunks(ctx context.Context, chunks []*postgres.Chunk) error {
	return nil
}
func (m *mockMetaStore) SearchBM25(ctx context.Context, userID, query string, k int) ([]postgres.BM25Result, error) {
	return nil, nil
}
func (m *mockMetaStore) GetUserState(ctx context.Context, userID string) (*postgres.UserState, error) {
	return nil, nil
}
func (m *mockMetaStore) EnqueuePending(ctx context.Context, chunkID string) error { return nil }
func (m *mockMetaStore) Close() error                                              { return nil }

// --- mock VectorStore ---

type mockVectorStore struct{}

func (m *mockVectorStore) EnsureCollection(ctx context.Context, dim uint64) error { return nil }
func (m *mockVectorStore) Upsert(ctx context.Context, points []qdrant.Point) error { return nil }
func (m *mockVectorStore) Search(ctx context.Context, vector []float32, k uint64, userID string) ([]qdrant.SearchResult, error) {
	return nil, nil
}
func (m *mockVectorStore) Close() error { return nil }

// --- helpers ---

func newTestReconciler(
	meta postgres.MetaStore,
	retryFn func(ctx context.Context, chunkID string) error,
	cfg ReconcilerConfig,
) *Reconciler {
	return NewReconciler(meta, &mockVectorStore{}, cfg, retryFn)
}

// tickOnce drives a single reconcile cycle without starting the goroutine.
func tickOnce(r *Reconciler) {
	r.tick(context.Background())
}

// --- tests ---

// TestReconciler_SuccessPath: one pending row, retryFn succeeds → DeletePending called once.
func TestReconciler_SuccessPath(t *testing.T) {
	const chunkID = "chunk-1"
	var deleteCalled int32

	meta := &mockMetaStore{
		drainFn: func(_ context.Context, _ int) ([]*postgres.PendingVector, error) {
			return []*postgres.PendingVector{
				{ChunkID: chunkID, Attempts: 0},
			}, nil
		},
		deleteFn: func(_ context.Context, id string) error {
			if id != chunkID {
				t.Errorf("DeletePending called with unexpected id %q", id)
			}
			atomic.AddInt32(&deleteCalled, 1)
			return nil
		},
	}

	var retryCalled int32
	retryFn := func(_ context.Context, id string) error {
		atomic.AddInt32(&retryCalled, 1)
		return nil
	}

	r := newTestReconciler(meta, retryFn, ReconcilerConfig{MaxAttempts: 5})
	tickOnce(r)

	if atomic.LoadInt32(&retryCalled) != 1 {
		t.Fatalf("retryFn called %d times, want 1", retryCalled)
	}
	if atomic.LoadInt32(&deleteCalled) != 1 {
		t.Fatalf("DeletePending called %d times, want 1", deleteCalled)
	}
}

// TestReconciler_RetryFailure: retryFn returns error → DeletePending NOT called.
func TestReconciler_RetryFailure(t *testing.T) {
	var deleteCalled int32

	meta := &mockMetaStore{
		drainFn: func(_ context.Context, _ int) ([]*postgres.PendingVector, error) {
			return []*postgres.PendingVector{
				{ChunkID: "chunk-2", Attempts: 1},
			}, nil
		},
		deleteFn: func(_ context.Context, _ string) error {
			atomic.AddInt32(&deleteCalled, 1)
			return nil
		},
	}

	retryFn := func(_ context.Context, _ string) error {
		return errors.New("upsert failed")
	}

	r := newTestReconciler(meta, retryFn, ReconcilerConfig{MaxAttempts: 5})
	tickOnce(r)

	if atomic.LoadInt32(&deleteCalled) != 0 {
		t.Fatalf("DeletePending called %d times, want 0", deleteCalled)
	}
}

// TestReconciler_MaxAttemptsExceeded: row.Attempts >= MaxAttempts → DeletePending called, retryFn NOT called.
func TestReconciler_MaxAttemptsExceeded(t *testing.T) {
	var deleteCalled int32
	var retryCalled int32

	meta := &mockMetaStore{
		drainFn: func(_ context.Context, _ int) ([]*postgres.PendingVector, error) {
			return []*postgres.PendingVector{
				{ChunkID: "chunk-3", Attempts: 5},
			}, nil
		},
		deleteFn: func(_ context.Context, _ string) error {
			atomic.AddInt32(&deleteCalled, 1)
			return nil
		},
	}

	retryFn := func(_ context.Context, _ string) error {
		atomic.AddInt32(&retryCalled, 1)
		return nil
	}

	r := newTestReconciler(meta, retryFn, ReconcilerConfig{MaxAttempts: 5})
	tickOnce(r)

	if atomic.LoadInt32(&retryCalled) != 0 {
		t.Fatalf("retryFn called %d times, want 0", retryCalled)
	}
	if atomic.LoadInt32(&deleteCalled) != 1 {
		t.Fatalf("DeletePending called %d times, want 1", deleteCalled)
	}
}

// TestReconciler_Stop: Start() then Stop() — goroutine exits cleanly.
func TestReconciler_Stop(t *testing.T) {
	meta := &mockMetaStore{
		drainFn: func(_ context.Context, _ int) ([]*postgres.PendingVector, error) {
			return nil, nil
		},
	}
	retryFn := func(_ context.Context, _ string) error { return nil }

	// Use a very long interval so the ticker never fires during the test.
	cfg := ReconcilerConfig{Interval: 10 * time.Minute, MaxAttempts: 5}
	r := newTestReconciler(meta, retryFn, cfg)

	ctx := context.Background()
	r.Start(ctx)

	done := make(chan struct{})
	go func() {
		r.Stop()
		close(done)
	}()

	select {
	case <-done:
		// goroutine exited cleanly
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() did not return within 2s — goroutine leak")
	}
}

// TestReconciler_EmptyDrain: DrainPending returns empty → no calls to retryFn or DeletePending.
func TestReconciler_EmptyDrain(t *testing.T) {
	var deleteCalled int32
	var retryCalled int32

	meta := &mockMetaStore{
		drainFn: func(_ context.Context, _ int) ([]*postgres.PendingVector, error) {
			return []*postgres.PendingVector{}, nil
		},
		deleteFn: func(_ context.Context, _ string) error {
			atomic.AddInt32(&deleteCalled, 1)
			return nil
		},
	}

	retryFn := func(_ context.Context, _ string) error {
		atomic.AddInt32(&retryCalled, 1)
		return nil
	}

	r := newTestReconciler(meta, retryFn, ReconcilerConfig{MaxAttempts: 5})
	tickOnce(r)

	if atomic.LoadInt32(&retryCalled) != 0 {
		t.Fatalf("retryFn called %d times, want 0", retryCalled)
	}
	if atomic.LoadInt32(&deleteCalled) != 0 {
		t.Fatalf("DeletePending called %d times, want 0", deleteCalled)
	}
}
