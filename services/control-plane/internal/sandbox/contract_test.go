package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"harness-forge.local/control-plane/internal/agentexec"
)

func localPaths(root string, id uuid.UUID) agentexec.Paths {
	return agentexec.Paths{Inputs: filepath.Join(root, id.String(), "inputs"), Workspace: filepath.Join(root, id.String(), "workspace"), Outputs: filepath.Join(root, id.String(), "outputs")}
}

func TestProviderLeaseContract(t *testing.T) {
	for _, kind := range []ProviderID{Docker, Fake} {
		t.Run(string(kind), func(t *testing.T) {
			ctx, id, root := context.Background(), uuid.New(), t.TempDir()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/executions" {
					json.NewEncoder(w).Encode([]agentexec.Execution{})
				}
			}))
			defer server.Close()
			var provider Provider
			var err error
			if kind == Docker {
				provider, err = NewDockerProvider(server.URL, root, server.Client())
			} else {
				provider, err = NewFakeProvider(filepath.Join("..", "..", "..", "..", "tests", "fixtures", "fake-runtime"))
			}
			if err != nil {
				t.Fatal(err)
			}
			request := AcquireRequest{RunID: id, Paths: localPaths(root, id)}
			first, err := provider.Acquire(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			second, err := provider.Acquire(ctx, request)
			if err != nil || second.Ref() != first.Ref() {
				t.Fatalf("duplicate acquire: %v", err)
			}
			expectedRef := "docker:agent-runtime"
			expectedPaths := localPaths("/workspaces", id)
			if kind == Fake {
				expectedRef = "fake:" + id.String()
				expectedPaths = request.Paths
			}
			if first.Ref() != expectedRef || first.Paths() != expectedPaths || !first.Paths().Valid() {
				t.Fatalf("lease: %s %#v", first.Ref(), first.Paths())
			}
			if _, err := provider.Recover(ctx, RecoverRequest{RunID: id, Ref: "wrong", Paths: request.Paths}); !errors.Is(err, ErrNotFound) {
				t.Fatalf("wrong ref: %v", err)
			}
			recovered, err := provider.Recover(ctx, RecoverRequest{RunID: id, Ref: first.Ref(), Paths: request.Paths})
			if err != nil || recovered.Ref() != first.Ref() {
				t.Fatalf("recover: %v", err)
			}
			listed, err := provider.List(ctx)
			if err != nil {
				t.Fatal(err)
			}
			expectedCount := 0
			if kind == Fake {
				expectedCount = 1
			}
			if len(listed) != expectedCount {
				t.Fatalf("list: %#v", listed)
			}
			for range 2 {
				if err := first.SyncBack(ctx); err != nil {
					t.Fatal(err)
				}
				if err := first.Release(ctx); err != nil {
					t.Fatal(err)
				}
			}
			listed, err = provider.List(ctx)
			if err != nil || len(listed) != 0 {
				t.Fatalf("released acquisition remains discoverable: %#v %v", listed, err)
			}
			if _, err := provider.Recover(ctx, RecoverRequest{RunID: id, Ref: first.Ref(), Paths: request.Paths}); err != nil {
				t.Fatalf("released logical lease lost idempotent recovery: %v", err)
			}
		})
	}
}

// Models the remote creation/acknowledgement boundary without creating containers.
// The same contract that Fake exercises before Execute must hold for future remote providers.
func TestProviderContractDiscoversAcquireWithLostAcknowledgement(t *testing.T) {
	fake, err := NewFakeProvider(filepath.Join("..", "..", "..", "..", "tests", "fixtures", "fake-runtime"))
	if err != nil {
		t.Fatal(err)
	}
	provider := lostAcknowledgementProvider{Provider: fake}
	id := uuid.New()
	request := AcquireRequest{RunID: id, Paths: localPaths(t.TempDir(), id)}
	lease, err := provider.Acquire(context.Background(), request)
	if lease != nil || !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("%v %v", lease, err)
	}
	listed, err := provider.List(context.Background())
	if err != nil || len(listed) != 1 || listed[0].RunID != id {
		t.Fatalf("list %#v %v", listed, err)
	}
	if _, err := provider.Recover(context.Background(), RecoverRequest{RunID: id, Ref: listed[0].Ref, Paths: request.Paths}); err != nil {
		t.Fatal(err)
	}
}

type lostAcknowledgementProvider struct{ Provider }

func (p lostAcknowledgementProvider) Acquire(ctx context.Context, r AcquireRequest) (Lease, error) {
	if _, err := p.Provider.Acquire(ctx, r); err != nil {
		return nil, err
	}
	return nil, &Error{Operation: "acquire", Provider: Fake, RunID: r.RunID, Kind: ErrOutcomeUnknown}
}
