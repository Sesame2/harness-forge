//go:build integration

package runs

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type cancelAckTracer struct {
	*lostClaimCommitAck
	cancelRequest       context.CancelFunc
	refuseConfirmation  bool
	confirmationBounded bool
}

func (p *cancelAckTracer) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if data.SQL == `SELECT `+runColumns+` FROM runs WHERE id=$1` && !p.armed.Load() {
		deadline, ok := ctx.Deadline()
		p.confirmationBounded = ok && ctx.Err() == nil && time.Until(deadline) <= 30*time.Second
		if p.refuseConfirmation {
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			return cancelled
		}
	}
	traced := p.lostClaimCommitAck.TraceQueryStart(ctx, conn, data)
	if data.SQL == "commit" && traced.Err() != nil && p.cancelRequest != nil {
		p.cancelRequest()
	}
	return traced
}

func TestCancelCommitLostAckConfirmsBeforeStoppingActiveExecution(t *testing.T) {
	for _, requestCancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "request alive", true: "request cancelled"}[requestCancelled], func(t *testing.T) {
			pool := integrationPool(t)
			initial := NewStore(pool)
			enqueue(t, pool)
			run, err := initial.ClaimNext(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			requestCtx, cancelRequest := context.WithCancel(context.Background())
			defer cancelRequest()
			tracer := &cancelAckTracer{lostClaimCommitAck: &lostClaimCommitAck{committed: make(chan error, 1)}}
			tracer.armed.Store(true)
			if requestCancelled {
				tracer.cancelRequest = cancelRequest
			}
			config := pool.Config()
			config.ConnConfig.Tracer = tracer
			cancelPool, err := pgxpool.NewWithConfig(context.Background(), config)
			if err != nil {
				t.Fatal(err)
			}
			defer cancelPool.Close()
			executionCtx, stop := context.WithCancel(context.Background())
			defer stop()
			coordinator := &Coordinator{store: NewStore(cancelPool), active: map[uuid.UUID]context.CancelFunc{run.ID: stop}}
			got, err := coordinator.Cancel(requestCtx, *run)
			if commitErr := <-tracer.committed; commitErr != nil {
				t.Fatal(commitErr)
			}
			persisted, readErr := initial.Read(context.Background(), run.ID)
			if readErr != nil || persisted.Status != Cancelled || persisted.FinalizedAt != nil {
				t.Fatal("cancellation not committed", persisted, readErr)
			}
			if err != nil || got.Status != Cancelled || executionCtx.Err() == nil || !tracer.confirmationBounded {
				t.Fatalf("durable cancelled worker not stopped after bounded confirmation: got=%#v err=%v stopped=%v bounded=%v", got, err, executionCtx.Err(), tracer.confirmationBounded)
			}
			if requestCancelled && requestCtx.Err() == nil {
				t.Fatal("request cancellation was not injected")
			}
		})
	}
}

func TestCancelUnknownConfirmationDoesNotStopWorkerAndRetryCompletesStop(t *testing.T) {
	pool := integrationPool(t)
	initial := NewStore(pool)
	enqueue(t, pool)
	run, err := initial.ClaimNext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	tracer := &cancelAckTracer{lostClaimCommitAck: &lostClaimCommitAck{committed: make(chan error, 1)}, refuseConfirmation: true}
	tracer.armed.Store(true)
	config := pool.Config()
	config.ConnConfig.Tracer = tracer
	cancelPool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer cancelPool.Close()
	executionCtx, stop := context.WithCancel(context.Background())
	defer stop()
	coordinator := &Coordinator{store: NewStore(cancelPool), active: map[uuid.UUID]context.CancelFunc{run.ID: stop}}
	if _, err := coordinator.Cancel(context.Background(), *run); err == nil {
		t.Fatal("unknown confirmation reported success")
	}
	if commitErr := <-tracer.committed; commitErr != nil {
		t.Fatal(commitErr)
	}
	if executionCtx.Err() != nil {
		t.Fatal("worker stopped without authoritative confirmation")
	}
	// This is the same Canceller entry point used by HTTP POST /runs/{id}/cancel.
	// A later healthy request must re-enter Coordinator despite persisted cancelled.
	coordinator.store = initial
	got, err := NewCanceller(initial, coordinator.Cancel).Cancel(context.Background(), run.ID)
	if err != nil || got.Status != Cancelled || got.FinalizedAt != nil || executionCtx.Err() == nil {
		t.Fatalf("cancel retry did not repair stop: %#v err=%v stopped=%v", got, err, executionCtx.Err())
	}
}

func TestCancelledRequestWithoutCommitDoesNotStopWorker(t *testing.T) {
	pool := integrationPool(t)
	store := NewStore(pool)
	enqueue(t, pool)
	run, err := store.ClaimNext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	executionCtx, stop := context.WithCancel(context.Background())
	defer stop()
	coordinator := &Coordinator{store: store, active: map[uuid.UUID]context.CancelFunc{run.ID: stop}}
	requestCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := coordinator.Cancel(requestCtx, *run); err == nil {
		t.Fatal("uncommitted cancelled request reported success")
	}
	got, err := store.Read(context.Background(), run.ID)
	if err != nil || got.Status != Running || executionCtx.Err() != nil {
		t.Fatal("invented cancellation without commit", got, err, executionCtx.Err())
	}
}
