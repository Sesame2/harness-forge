package runs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type cancelStore interface {
	Read(context.Context, uuid.UUID) (Run, error)
	CancelQueued(context.Context, uuid.UUID) (Run, error)
}

// ActiveCancel belongs to the Coordinator: cancel Runtime, then arrange abort/release finalization.
// The HTTP request must not own execution lifetime or synthesize a finalized timestamp.
type ActiveCancel func(context.Context, Run) (Run, error)
type Canceller struct {
	store  cancelStore
	active ActiveCancel
}

func NewCanceller(store cancelStore, active ActiveCancel) *Canceller {
	return &Canceller{store, active}
}
func (c *Canceller) Cancel(ctx context.Context, id uuid.UUID) (Run, error) {
	run, err := c.store.Read(ctx, id)
	if err != nil {
		return Run{}, err
	}
	if run.Status == Queued {
		cancelled, err := c.store.CancelQueued(ctx, id)
		if !errors.Is(err, ErrConflict) {
			return cancelled, err
		}
		// A scheduler may have claimed the row since Read; apply active cancellation to the current state.
		run, err = c.store.Read(ctx, id)
		if err != nil {
			return Run{}, err
		}
	}
	if run.Status == Cancelled {
		if run.FinalizedAt != nil {
			return run, nil
		}
		if c.active == nil {
			return Run{}, ErrUnavailable
		}
		return c.active(ctx, run)
	}
	if run.Status != Running || run.Phase == nil || *run.Phase == Publishing || run.FinalizedAt != nil {
		return Run{}, ErrConflict
	}
	if c.active == nil {
		return Run{}, ErrUnavailable
	}
	return c.active(ctx, run)
}

// CancelQueued serializes with ClaimNext and commits status, finalized_at and event together.
func (s *Store) CancelQueued(ctx context.Context, id uuid.UUID) (Run, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Run{}, fmt.Errorf("begin cancel: %w", err)
	}
	defer tx.Rollback(ctx)
	run, err := scanRun(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM runs WHERE id=$1 FOR UPDATE`, id))
	if err != nil {
		return Run{}, err
	}
	if run.Status == Cancelled && run.FinalizedAt != nil {
		return run, nil
	}
	if run.Status != Queued {
		return Run{}, ErrConflict
	}
	run, err = Finish(run, Cancelled, Cleanup{Lease: Absent, Runtime: Absent}, time.Now().UTC())
	if err != nil {
		return Run{}, err
	}
	run, err = scanRun(tx.QueryRow(ctx, `UPDATE runs SET status=$2,finalized_at=$3,updated_at=$4 WHERE id=$1 RETURNING `+runColumns, id, run.Status, run.FinalizedAt, run.UpdatedAt))
	if err != nil {
		return Run{}, err
	}
	key := "terminal:cancelled"
	if _, err := s.AppendEventTx(ctx, tx, Event{RunID: id, Type: "run.cancelled", Payload: []byte(`{}`), OccurredAt: run.UpdatedAt, DedupeKey: &key}); err != nil {
		return Run{}, err
	}
	err = tx.Commit(ctx)
	s.broker.Notify(id)
	if err != nil {
		return Run{}, fmt.Errorf("commit cancel: %w", err)
	}
	return run, nil
}
