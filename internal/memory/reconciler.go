package memory

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/gregdhill/engram/internal/store/postgres"
	"github.com/gregdhill/engram/internal/store/qdrant"
)

// ReconcilerConfig holds tuning knobs for the Reconciler.
type ReconcilerConfig struct {
	// Interval is how long to wait between drain cycles. Default: 30s.
	Interval time.Duration
	// BatchSize is the maximum number of pending rows to drain per cycle. Default: 10.
	BatchSize int
	// MaxAttempts is the number of times a row may appear in DrainPending before
	// it is permanently deleted without retrying. Default: 5.
	//
	// NOTE: The current MetaStore interface does not expose an IncrementAttempts
	// method, so the attempts counter in postgres is never incremented by the
	// reconciler. MaxAttempts therefore guards against rows whose attempt count
	// was incremented externally (e.g. by the ingestor on initial failure). If
	// the ingestor never increments attempts, a row will be retried indefinitely
	// until retryFn succeeds. A future extension should add
	// IncrementPendingAttempts to MetaStore and call it on retryFn failure.
	MaxAttempts int
}

func (c *ReconcilerConfig) withDefaults() ReconcilerConfig {
	out := *c
	if out.Interval <= 0 {
		out.Interval = 30 * time.Second
	}
	if out.BatchSize <= 0 {
		out.BatchSize = 10
	}
	if out.MaxAttempts <= 0 {
		out.MaxAttempts = 5
	}
	return out
}

// Reconciler is a background goroutine that periodically retries Qdrant upserts
// for chunks that failed during ingestion.
//
// Design note on attempt tracking: DrainPending uses SKIP LOCKED, so rows that
// are currently being processed by another worker are skipped. Because we do not
// call IncrementPendingAttempts (the method does not exist in MetaStore), the
// attempts column in postgres is only set by the original enqueuer. The
// MaxAttempts guard therefore relies on the enqueuer having stored a non-zero
// attempt count. This is a known limitation; see ReconcilerConfig.MaxAttempts.
type Reconciler struct {
	meta    postgres.MetaStore
	vec     qdrant.VectorStore
	cfg     ReconcilerConfig
	retryFn func(ctx context.Context, chunkID string) error
	stopCh  chan struct{}
	wg      sync.WaitGroup
}

// NewReconciler creates a Reconciler. retryFn is called for each pending chunk
// that has not yet exceeded MaxAttempts; on success the row is deleted from the
// queue. vec is accepted for future use (e.g. direct upsert) but is not used by
// the default retry path, which delegates entirely to retryFn.
func NewReconciler(
	meta postgres.MetaStore,
	vec qdrant.VectorStore,
	cfg ReconcilerConfig,
	retryFn func(ctx context.Context, chunkID string) error,
) *Reconciler {
	return &Reconciler{
		meta:    meta,
		vec:     vec,
		cfg:     cfg.withDefaults(),
		retryFn: retryFn,
		stopCh:  make(chan struct{}),
	}
}

// Start launches the background reconcile goroutine. It returns immediately.
// Call Stop to shut it down.
func (r *Reconciler) Start(ctx context.Context) {
	r.wg.Add(1)
	go r.run(ctx)
}

// Stop signals the reconciler to stop and blocks until the goroutine exits.
func (r *Reconciler) Stop() {
	close(r.stopCh)
	r.wg.Wait()
}

func (r *Reconciler) run(ctx context.Context) {
	defer r.wg.Done()

	ticker := time.NewTicker(r.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-r.stopCh:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.tick(ctx)
		}
	}
}

func (r *Reconciler) tick(ctx context.Context) {
	rows, err := r.meta.DrainPending(ctx, r.cfg.BatchSize)
	if err != nil {
		slog.Warn("reconciler: DrainPending failed", "err", err)
		return
	}

	for _, row := range rows {
		if row.Attempts >= r.cfg.MaxAttempts {
			slog.Warn("reconciler: chunk exceeded max attempts, dropping",
				"chunk_id", row.ChunkID,
				"attempts", row.Attempts,
				"last_error", row.LastError,
			)
			if delErr := r.meta.DeletePending(ctx, row.ChunkID); delErr != nil {
				slog.Warn("reconciler: DeletePending failed", "chunk_id", row.ChunkID, "err", delErr)
			}
			continue
		}

		if err := r.retryFn(ctx, row.ChunkID); err != nil {
			// Leave the row in the queue; it will be picked up on the next tick.
			// Attempt count is NOT incremented here because MetaStore has no
			// IncrementPendingAttempts method. See ReconcilerConfig.MaxAttempts.
			slog.Warn("reconciler: retryFn failed", "chunk_id", row.ChunkID, "err", err)
			continue
		}

		if delErr := r.meta.DeletePending(ctx, row.ChunkID); delErr != nil {
			slog.Warn("reconciler: DeletePending after success failed",
				"chunk_id", row.ChunkID, "err", delErr)
		}
	}
}
