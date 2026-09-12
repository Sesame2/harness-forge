package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"harness-forge.local/control-plane/internal/agentexec"
)

func TestDockerProviderListMapsRuntimeRecordsAndFailuresAreDefinite(t *testing.T) {
	id := uuid.New()
	healthy := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !healthy {
			w.WriteHeader(503)
			return
		}
		if r.URL.Path == "/v1/executions" {
			json.NewEncoder(w).Encode([]agentexec.Execution{{RunID: id, Lifecycle: agentexec.Starting}})
		}
	}))
	defer server.Close()
	root := t.TempDir()
	provider, err := NewDockerProvider(server.URL, root, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	listed, err := provider.List(context.Background())
	if err != nil || len(listed) != 1 || listed[0].RunID != id || listed[0].Ref != "docker:agent-runtime" {
		t.Fatalf("%#v %v", listed, err)
	}
	_, err = provider.Acquire(context.Background(), AcquireRequest{RunID: id, Paths: localPaths(t.TempDir(), id)})
	if !errors.Is(err, ErrConflict) || errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("mapping: %v", err)
	}
	healthy = false
	_, err = provider.Acquire(context.Background(), AcquireRequest{RunID: id, Paths: localPaths(root, id)})
	if !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("health: %v", err)
	}
	var typed *Error
	if !errors.As(err, &typed) || typed.Operation != "acquire" || typed.Provider != Docker || typed.RunID != id {
		t.Fatalf("correlation: %#v", typed)
	}
}
