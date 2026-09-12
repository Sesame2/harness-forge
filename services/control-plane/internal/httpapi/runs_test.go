package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"harness-forge.local/control-plane/internal/runs"
)

type runMemoryStore struct {
	mu           sync.Mutex
	run          runs.Run
	events       []runs.Event
	conversation uuid.UUID
	err          error
	afterList    func()
}

func (s *runMemoryStore) Read(ctx context.Context, id uuid.UUID) (runs.Run, error) {
	if s.err != nil {
		return runs.Run{}, s.err
	}
	if id != s.run.ID {
		return runs.Run{}, runs.ErrNotFound
	}
	return s.run, nil
}
func (s *runMemoryStore) ListByConversation(ctx context.Context, id uuid.UUID) ([]runs.Run, error) {
	s.conversation = id
	if id != s.run.ConversationID {
		return nil, runs.ErrNotFound
	}
	return []runs.Run{s.run}, nil
}
func (s *runMemoryStore) ListEvents(ctx context.Context, id uuid.UUID, after int64) ([]runs.Event, error) {
	s.mu.Lock()
	events := []runs.Event{}
	for _, event := range s.events {
		if event.Sequence > after {
			events = append(events, event)
		}
	}
	hook := s.afterList
	s.afterList = nil
	s.mu.Unlock()
	if hook != nil {
		hook()
	}
	return events, nil
}

type cancelFunc func(context.Context, uuid.UUID) (runs.Run, error)

func (f cancelFunc) Cancel(ctx context.Context, id uuid.UUID) (runs.Run, error) { return f(ctx, id) }

func TestRunRoutesListReadCancelAndHideProviderMetadata(t *testing.T) {
	ref, provider := "secret-runtime-ref", "fake"
	store := &runMemoryStore{run: runs.Run{ID: uuid.New(), ConversationID: uuid.New(), Status: runs.Queued, SandboxRef: &ref, SandboxProvider: &provider}}
	cancelCalls := 0
	router := NewRouter(Dependencies{Runs: store, Broker: runs.NewBroker(), Canceller: cancelFunc(func(ctx context.Context, id uuid.UUID) (runs.Run, error) { cancelCalls++; return store.run, nil })})
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/api/v1/conversations/" + store.run.ConversationID.String() + "/runs", 200},
		{"GET", "/api/v1/runs/" + store.run.ID.String(), 200},
		{"POST", "/api/v1/runs/" + store.run.ID.String() + "/cancel", 202},
		{"GET", "/api/v1/runs/" + uuid.NewString(), 404},
		{"GET", "/api/v1/conversations/" + uuid.NewString() + "/runs", 404},
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(tc.method, tc.path, nil))
		if response.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.path, response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), ref) || strings.Contains(response.Body.String(), "sandbox_") {
			t.Fatal("provider metadata exposed")
		}
	}
	if cancelCalls != 1 {
		t.Fatal(cancelCalls)
	}
}

func TestRunErrorEnvelopes(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{{runs.ErrNotFound, 404, "not_found"}, {runs.ErrConflict, 409, "conflict"}, {runs.ErrUnavailable, 503, "unavailable"}} {
		store := &runMemoryStore{run: runs.Run{ID: uuid.New()}, err: tc.err}
		response := httptest.NewRecorder()
		NewRouter(Dependencies{Runs: store}).ServeHTTP(response, httptest.NewRequest("GET", "/api/v1/runs/"+store.run.ID.String(), nil))
		var body map[string]any
		json.Unmarshal(response.Body.Bytes(), &body)
		if response.Code != tc.status || body["code"] != tc.code || body["request_id"] == nil {
			t.Fatalf("%d %s", response.Code, response.Body.String())
		}
	}
}

func eventFixture(id uuid.UUID, sequence int64) runs.Event {
	return runs.Event{RunID: id, Sequence: sequence, Type: "assistant.delta", Payload: json.RawMessage(`{"text":"hello"}`), OccurredAt: time.Now().UTC()}
}

func TestRunEventsListAndCursorValidation(t *testing.T) {
	id := uuid.New()
	store := &runMemoryStore{run: runs.Run{ID: id}, events: []runs.Event{eventFixture(id, 1), eventFixture(id, 2), eventFixture(id, 3)}}
	router := NewRouter(Dependencies{Runs: store, Broker: runs.NewBroker()})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", "/api/v1/runs/"+id.String()+"/events?after_sequence=1", nil))
	var events []runs.Event
	json.Unmarshal(response.Body.Bytes(), &events)
	if response.Code != 200 || len(events) != 2 || events[0].Sequence != 2 {
		t.Fatalf("%d %s", response.Code, response.Body.String())
	}
	for _, cursor := range []string{"-1", "abc", "18446744073709551616"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("GET", "/api/v1/runs/"+id.String()+"/events?after_sequence="+cursor, nil))
		if response.Code != 400 {
			t.Fatalf("cursor %s: %d", cursor, response.Code)
		}
	}
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", "/api/v1/runs/"+id.String()+"/events?after_sequence=18446744073709551615", nil))
	if response.Code != 200 || strings.TrimSpace(response.Body.String()) != "[]" {
		t.Fatalf("valid uint64 cursor: %d %s", response.Code, response.Body.String())
	}
}
