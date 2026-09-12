//go:build integration

package runs

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCommitLostAckStillNotifiesDurableEvents(t *testing.T) {
	for _, operation := range []string{"finalize", "append", "cancel queued"} {
		t.Run(operation, func(t *testing.T) {
			pool := integrationPool(t)
			ctx := context.Background()
			initial := NewStore(pool)
			run := enqueue(t, pool)
			if operation == "finalize" {
				if _, err := initial.ClaimNext(ctx); err != nil {
					t.Fatal(err)
				}
				if _, err := initial.RecordFailure(ctx, run.ID, Failed, failureDetail("original")); err != nil {
					t.Fatal(err)
				}
			}
			tracer := &lostClaimCommitAck{committed: make(chan error, 1)}
			tracer.armed.Store(true)
			config := pool.Config()
			config.ConnConfig.Tracer = tracer
			commitPool, err := pgxpool.NewWithConfig(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			defer commitPool.Close()
			broker := NewBroker()
			notifications, unsubscribe := broker.Subscribe(ctx, run.ID)
			defer unsubscribe()
			store := NewStore(commitPool, broker)
			wantType := "run.failed"
			switch operation {
			case "finalize":
				err = store.AcknowledgeFinalized(ctx, run.ID)
			case "append":
				wantType = "assistant.delta"
				_, err = store.AppendEvent(ctx, Event{RunID: run.ID, Type: wantType, Payload: []byte(`{"text":"committed"}`), OccurredAt: time.Now().UTC()})
			case "cancel queued":
				wantType = "run.cancelled"
				_, err = store.CancelQueued(ctx, run.ID)
			}
			if err == nil {
				t.Fatal("commit ACK loss was not injected")
			}
			if commitErr := <-tracer.committed; commitErr != nil {
				t.Fatal(commitErr)
			}
			events, err := initial.ListEvents(ctx, run.ID, 0)
			if err != nil || len(events) != 1 || events[0].Type != wantType {
				t.Fatal("event did not really commit", events, err)
			}
			if operation != "append" {
				got, err := initial.Read(ctx, run.ID)
				if err != nil || got.FinalizedAt == nil {
					t.Fatal("terminal event without finalized run", got, err)
				}
			}
			select {
			case <-notifications:
			default:
				t.Fatal("durable event committed but subscriber received no wakeup")
			}
		})
	}
}

func TestAppendEventTxDoesNotNotifyBeforeOwnerCommit(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	run := enqueue(t, pool)
	broker := NewBroker()
	notifications, unsubscribe := broker.Subscribe(ctx, run.ID)
	defer unsubscribe()
	store := NewStore(pool, broker)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := store.AppendEventTx(ctx, tx, Event{RunID: run.ID, Type: "assistant.delta", Payload: []byte(`{}`), OccurredAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-notifications:
		t.Fatal("caller-owned transaction emitted premature wakeup")
	default:
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	events, err := store.ListEvents(ctx, run.ID, 0)
	if err != nil || len(events) != 0 {
		t.Fatal(events, err)
	}
}
