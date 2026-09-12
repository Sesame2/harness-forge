package runs

import (
	"context"
	"errors"
	"log"
	"sync/atomic"
	"time"
)

type Scheduler struct {
	store       *Store
	coordinator *Coordinator
	reconciler  *Reconciler
	ready       atomic.Bool
	poll, retry time.Duration
}

func NewScheduler(store *Store, coordinator *Coordinator, reconciler *Reconciler) *Scheduler {
	return &Scheduler{store: store, coordinator: coordinator, reconciler: reconciler, poll: time.Second, retry: 5 * time.Second}
}
func (s *Scheduler) Ready() bool { return s.ready.Load() }

// One scheduler owns the V0 slot. Execute is synchronous here, so periodic
// repair cannot mistake this process's live execution for a crash residue.
func (s *Scheduler) Run(ctx context.Context) {
	startup := true
	backoff := s.retry
	defer s.ready.Store(false)
	for ctx.Err() == nil {
		if err := s.reconciler.Reconcile(ctx, !startup); err != nil {
			s.ready.Store(false)
			log.Printf("run scheduling paused pending reconciliation: %v", err)
			if errors.Is(err, ErrConsistency) {
				<-ctx.Done()
				return
			}
			if !schedulerWait(ctx, backoff) {
				return
			}
			backoff = min(backoff*2, time.Minute)
			continue
		}
		startup = false
		backoff = s.retry
		s.ready.Store(true)
		run, err := s.store.ClaimNext(ctx)
		if err != nil {
			s.ready.Store(false)
			log.Printf("claim run: %v", err)
			if !schedulerWait(ctx, backoff) {
				return
			}
			continue
		}
		if run != nil {
			if err := s.coordinator.Execute(ctx, *run); err != nil {
				// Execute has returned, so it no longer owns a live local run.
				// A failed DB outcome write can leave running crash-like residue.
				startup = true
				log.Printf("run %s requires outcome/cleanup inspection: %v", run.ID, err)
				if errors.Is(err, ErrConsistency) {
					s.ready.Store(false)
					<-ctx.Done()
					return
				}
			}
			continue
		}
		if !schedulerWait(ctx, s.poll) {
			return
		}
	}
}
func schedulerWait(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
