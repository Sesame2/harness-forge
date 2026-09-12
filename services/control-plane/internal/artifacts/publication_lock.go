package artifacts

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AcquirePublicationLock serializes publishing and orphan scanning on one
// dedicated PostgreSQL session. A transaction lock would end before the
// caller's independent metadata transaction, and pool.Exec could unlock a
// different session. The returned release is idempotent and cancellation-safe.
func AcquirePublicationLock(ctx context.Context, pool *pgxpool.Pool) (func() error, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire artifact maintenance session: %w", err)
	}
	discard := func() error {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return conn.Hijack().Close(cleanup)
	}
	// ponytail: one global V0 maintenance lock; partition only when publication throughput requires it.
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended('harness-forge:artifact-maintenance',0))`); err != nil {
		return nil, errors.Join(fmt.Errorf("lock artifact maintenance: %w", err), discard())
	}
	var once sync.Once
	var releaseErr error
	return func() error {
		once.Do(func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var unlocked bool
			releaseErr = conn.QueryRow(cleanup, `SELECT pg_advisory_unlock(hashtextextended('harness-forge:artifact-maintenance',0))`).Scan(&unlocked)
			if releaseErr != nil || !unlocked {
				if releaseErr == nil {
					releaseErr = errors.New("artifact maintenance session did not own lock")
				}
				releaseErr = errors.Join(releaseErr, discard())
				return
			}
			conn.Release()
		})
		return releaseErr
	}, nil
}
