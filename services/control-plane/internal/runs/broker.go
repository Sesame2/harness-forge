package runs

import (
	"context"
	"github.com/google/uuid"
	"sync"
)

// Broker coalesces wakeups only. Durable events and cursors always come from PostgreSQL.
type Broker struct {
	mu          sync.Mutex
	subscribers map[uuid.UUID]map[chan struct{}]struct{}
}

func NewBroker() *Broker { return &Broker{subscribers: map[uuid.UUID]map[chan struct{}]struct{}{}} }
func (b *Broker) Subscribe(ctx context.Context, id uuid.UUID) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	b.mu.Lock()
	if b.subscribers[id] == nil {
		b.subscribers[id] = map[chan struct{}]struct{}{}
	}
	b.subscribers[id][ch] = struct{}{}
	b.mu.Unlock()
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			delete(b.subscribers[id], ch)
			if len(b.subscribers[id]) == 0 {
				delete(b.subscribers, id)
			}
		})
	}
	stop := context.AfterFunc(ctx, cleanup)
	return ch, func() { stop(); cleanup() }
}
func (b *Broker) Notify(id uuid.UUID) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subscribers[id] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
