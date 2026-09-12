package httpapi

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"harness-forge.local/control-plane/internal/runs"
)

func TestSSEReplayDurableIDsAndDisconnectNeverCancels(t *testing.T) {
	id := uuid.New()
	broker := runs.NewBroker()
	store := &runMemoryStore{run: runs.Run{ID: id}, events: []runs.Event{eventFixture(id, 1), eventFixture(id, 2), eventFixture(id, 3)}}
	var cancelled atomic.Int32
	server := httptest.NewServer(NewRouter(Dependencies{Runs: store, Broker: broker, Canceller: cancelFunc(func(context.Context, uuid.UUID) (runs.Run, error) { cancelled.Add(1); return store.run, nil })}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/v1/runs/"+id.String()+"/events/stream?after_sequence=999", nil)
	request.Header.Set("Last-Event-ID", "1")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("%d %s", response.StatusCode, response.Header)
	}
	got := readSSEIDs(t, bufio.NewScanner(response.Body), 2)
	response.Body.Close()
	cancel()
	if !reflect.DeepEqual(got, []int64{2, 3}) || cancelled.Load() != 0 {
		t.Fatalf("ids=%v cancelled=%d", got, cancelled.Load())
	}
}

func TestSSESubscribeBeforeReplayClosesReplayLiveRace(t *testing.T) {
	id := uuid.New()
	broker := runs.NewBroker()
	store := &runMemoryStore{run: runs.Run{ID: id}, events: []runs.Event{eventFixture(id, 1), eventFixture(id, 2)}}
	store.afterList = func() {
		store.mu.Lock()
		store.events = append(store.events, eventFixture(id, 3))
		store.mu.Unlock()
		broker.Notify(id)
	}
	server := httptest.NewServer(NewRouter(Dependencies{Runs: store, Broker: broker}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/v1/runs/"+id.String()+"/events/stream", nil)
	request.Header.Set("Last-Event-ID", "1")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	got := readSSEIDs(t, scanner, 2)
	store.mu.Lock()
	store.events = append(store.events, eventFixture(id, 4))
	store.mu.Unlock()
	broker.Notify(id)
	broker.Notify(id)
	got = append(got, readSSEIDs(t, scanner, 1)...)
	if !reflect.DeepEqual(got, []int64{2, 3, 4}) {
		t.Fatalf("ids=%v", got)
	}
}

func readSSEIDs(t *testing.T, scanner *bufio.Scanner, count int) []int64 {
	t.Helper()
	var ids []int64
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "id: ") {
			id, err := strconv.ParseInt(strings.TrimPrefix(scanner.Text(), "id: "), 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
			if len(ids) == count {
				return ids
			}
		}
	}
	t.Fatalf("stream ended after %v: %v", ids, scanner.Err())
	return nil
}

func TestSSEUntrustedEventTypeCannotInjectDurableID(t *testing.T) {
	id := uuid.New()
	event := eventFixture(id, 1)
	event.Type = "future.progress\nid: 999"
	store := &runMemoryStore{run: runs.Run{ID: id}, events: []runs.Event{event, eventFixture(id, 2)}}
	server := httptest.NewServer(NewRouter(Dependencies{Runs: store, Broker: runs.NewBroker()}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/v1/runs/"+id.String()+"/events/stream", nil)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	got := readSSEIDs(t, bufio.NewScanner(response.Body), 2)
	if !reflect.DeepEqual(got, []int64{1, 2}) {
		t.Fatalf("injected IDs: %v", got)
	}
}

func TestSSEPollsDurableEventsAfterEarlyOrMissingWakeupWithoutDuplicates(t *testing.T) {
	id := uuid.New()
	broker := runs.NewBroker()
	store := &runMemoryStore{run: runs.Run{ID: id}, events: []runs.Event{eventFixture(id, 1)}}
	server := httptest.NewServer(NewRouter(Dependencies{Runs: store, Broker: broker}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 14*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/v1/runs/"+id.String()+"/events/stream", nil)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	got := readSSEIDs(t, scanner, 1)
	// Model a COMMIT-error notification arriving while the server-side commit
	// is still in flight: its immediate database read legitimately sees nothing.
	earlyRead := make(chan struct{})
	store.mu.Lock()
	store.afterList = func() { close(earlyRead) }
	store.mu.Unlock()
	broker.Notify(id)
	select {
	case <-earlyRead:
	case <-ctx.Done():
		t.Fatal("early wakeup was not read")
	}
	store.mu.Lock()
	store.events = append(store.events, eventFixture(id, 2))
	store.mu.Unlock()
	got = append(got, readSSEIDs(t, scanner, 1)...)
	// A second silent append verifies that the periodic reread keeps its
	// durable cursor, rather than replaying the preceding event again.
	terminal := eventFixture(id, 3)
	terminal.Type = "run.failed"
	terminal.Payload = []byte(`{}`)
	store.mu.Lock()
	store.events = append(store.events, terminal)
	store.mu.Unlock()
	got = append(got, readSSEIDs(t, scanner, 1)...)
	if !reflect.DeepEqual(got, []int64{1, 2, 3}) {
		t.Fatalf("silent publication replayed or skipped durable IDs: %v", got)
	}
}
