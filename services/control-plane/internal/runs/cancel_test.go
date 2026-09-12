package runs

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

type cancelMemoryStore struct {
	run         Run
	queuedCalls int
	err         error
}

func (s *cancelMemoryStore) Read(context.Context, uuid.UUID) (Run, error) { return s.run, s.err }
func (s *cancelMemoryStore) CancelQueued(ctx context.Context, id uuid.UUID) (Run, error) {
	s.queuedCalls++
	var err error
	s.run, err = Finish(s.run, Cancelled, Cleanup{}, time.Now())
	return s.run, err
}

func TestCancelQueuedUsesAtomicStoreOperation(t *testing.T) {
	store := &cancelMemoryStore{run: Run{ID: uuid.New(), Status: Queued}}
	canceller := NewCanceller(store, func(context.Context, Run) (Run, error) { t.Fatal("queued called runtime"); return Run{}, nil })
	got, err := canceller.Cancel(context.Background(), store.run.ID)
	if err != nil || got.Status != Cancelled || got.FinalizedAt == nil || store.queuedCalls != 1 {
		t.Fatalf("%#v %v calls=%d", got, err, store.queuedCalls)
	}
}

func TestCancelActiveDelegatesWithoutForgingFinalization(t *testing.T) {
	for _, phase := range []Phase{Preparing, Agent} {
		t.Run(string(phase), func(t *testing.T) {
			store := &cancelMemoryStore{run: Run{ID: uuid.New(), Status: Running, Phase: &phase}}
			var order []string
			// This is the Coordinator-owned seam: runtime cancel acknowledgement precedes finalization scheduling.
			runtimeCancel := func(context.Context, uuid.UUID) error { order = append(order, "runtime.cancel"); return nil }
			scheduleFinalize := func(context.Context, Run) error { order = append(order, "coordinator.finalize"); return nil }
			canceller := NewCanceller(store, func(ctx context.Context, run Run) (Run, error) {
				if err := runtimeCancel(ctx, run.ID); err != nil {
					return Run{}, err
				}
				return run, scheduleFinalize(ctx, run)
			})
			got, err := canceller.Cancel(context.Background(), store.run.ID)
			if err != nil || got.FinalizedAt != nil || got.Status != Running || store.queuedCalls != 0 || !reflect.DeepEqual(order, []string{"runtime.cancel", "coordinator.finalize"}) {
				t.Fatalf("%#v %v %v", got, err, order)
			}
		})
	}
}

func TestCancelPublishingAndUnavailableAndNotFound(t *testing.T) {
	phase := Publishing
	store := &cancelMemoryStore{run: Run{ID: uuid.New(), Status: Running, Phase: &phase}}
	canceller := NewCanceller(store, nil)
	if _, err := canceller.Cancel(context.Background(), store.run.ID); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	phase = Agent
	if _, err := canceller.Cancel(context.Background(), store.run.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	store.err = ErrNotFound
	if _, err := canceller.Cancel(context.Background(), store.run.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestRunBrokerCoalescesAndCleansSubscribers(t *testing.T) {
	broker := NewBroker()
	ctx, cancel := context.WithCancel(context.Background())
	id := uuid.New()
	notifications, unsubscribe := broker.Subscribe(ctx, id)
	for range 10000 {
		broker.Notify(id)
	}
	select {
	case <-notifications:
	default:
		t.Fatal("no notification")
	}
	select {
	case <-notifications:
		t.Fatal("notifications were not coalesced")
	default:
	}
	cancel()
	unsubscribe()
	unsubscribe()
	broker.mu.Lock()
	count := len(broker.subscribers)
	broker.mu.Unlock()
	if count != 0 {
		t.Fatalf("leaked subscriptions: %d", count)
	}
	broker.Notify(id)
}
