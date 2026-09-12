package sandbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"harness-forge.local/control-plane/internal/agentexec"
	"harness-forge.local/control-plane/internal/contracts"
)

func fakeRequest(id uuid.UUID, paths agentexec.Paths) agentexec.ExecuteRequest {
	return agentexec.ExecuteRequest{Version: "1", RunID: id, ProjectID: uuid.New(), ConversationID: uuid.New(), Prompt: "report", Profile: agentexec.Profile{ID: "geo-analysis", Version: "1", Digest: "test", Config: map[string]any{}}, Paths: paths, Limits: agentexec.Limits{MaxTurns: 8, MaxBudgetUSD: 2}}
}

func TestFakeProviderCopiesOutputsBeforeCompletedAndSyncBackDoesNothing(t *testing.T) {
	provider, err := NewFakeProvider(filepath.Join("..", "..", "..", "..", "tests", "fixtures", "fake-runtime"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, id := context.Background(), uuid.New()
	paths := localPaths(t.TempDir(), id)
	lease, err := provider.Acquire(ctx, AcquireRequest{RunID: id, Paths: paths})
	if err != nil {
		t.Fatal(err)
	}
	events, errs := lease.Runtime().Execute(ctx, fakeRequest(id, paths))
	var session agentexec.SessionID
	completed := false
	for event := range events {
		if event.RunID != id.String() {
			t.Fatal("fixture run ID not replaced")
		}
		if event.Type == "agent.completed" {
			completed = true
			session = agentexec.SessionID(event.Payload.(contracts.AgentCompletedPayload).CandidateSDKSessionID)
			if _, err := os.Stat(filepath.Join(paths.Outputs, "report", "index.html")); err != nil {
				t.Fatal(err)
			}
		}
	}
	for err := range errs {
		t.Fatal(err)
	}
	if !completed {
		t.Fatal("missing completed")
	}
	output := filepath.Join(paths.Outputs, "report", "index.html")
	if err := os.WriteFile(output, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := lease.SyncBack(ctx); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := os.ReadFile(output)
	if string(data) != "unchanged" {
		t.Fatal("SyncBack copied again")
	}
	_, errs = lease.Runtime().Execute(ctx, fakeRequest(id, paths))
	if err := <-errs; !errors.Is(err, agentexec.ErrConflict) {
		t.Fatalf("duplicate: %v", err)
	}
	executions, err := lease.Runtime().ListExecutions(ctx)
	if err != nil || len(executions) != 1 || executions[0].Lifecycle != agentexec.AwaitingFinalize {
		t.Fatalf("%#v %v", executions, err)
	}
	for range 2 {
		if err := lease.Runtime().Finalize(ctx, id, agentexec.Commit); err != nil {
			t.Fatal(err)
		}
	}
	if exists, err := lease.Runtime().SessionExists(ctx, session); err != nil || !exists {
		t.Fatalf("%v %v", exists, err)
	}
	if err := lease.Runtime().Finalize(ctx, id, agentexec.Abort); !errors.Is(err, agentexec.ErrConflict) {
		t.Fatalf("contradiction: %v", err)
	}
	_, errs = lease.Runtime().Execute(ctx, fakeRequest(id, paths))
	if err := <-errs; !errors.Is(err, agentexec.ErrFinalized) {
		t.Fatalf("finalized duplicate: %v", err)
	}
	for range 2 {
		if err := lease.Runtime().DeleteExecution(ctx, id); err != nil {
			t.Fatal(err)
		}
		if err := lease.Runtime().DeleteSession(ctx, session); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFakeProviderCopyFailureNeverEmitsCompleted(t *testing.T) {
	provider, err := NewFakeProvider(filepath.Join("..", "..", "..", "..", "tests", "fixtures", "fake-runtime"))
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	paths := localPaths(t.TempDir(), id)
	if err := os.MkdirAll(filepath.Dir(paths.Outputs), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Outputs, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	lease, err := provider.Acquire(context.Background(), AcquireRequest{RunID: id, Paths: paths})
	if err != nil {
		t.Fatal(err)
	}
	events, errs := lease.Runtime().Execute(context.Background(), fakeRequest(id, paths))
	for event := range events {
		if event.Type == "agent.completed" {
			t.Fatal("completed despite failed copy")
		}
	}
	if err := <-errs; err == nil {
		t.Fatal("missing copy error")
	}
}

func TestFakeProviderRecoverNeverCreates(t *testing.T) {
	provider, err := NewFakeProvider(filepath.Join("..", "..", "..", "..", "tests", "fixtures", "fake-runtime"))
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if _, err := provider.Recover(context.Background(), RecoverRequest{RunID: id, Ref: "fake:" + id.String(), Paths: localPaths(t.TempDir(), id)}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	listed, _ := provider.List(context.Background())
	if len(listed) != 0 {
		t.Fatal(listed)
	}
}

func TestFakeProviderCancelBlockedStreamThenAbort(t *testing.T) {
	provider, err := NewFakeProvider(filepath.Join("..", "..", "..", "..", "tests", "fixtures", "fake-runtime"))
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	paths := localPaths(t.TempDir(), id)
	ctx := context.Background()
	lease, err := provider.Acquire(ctx, AcquireRequest{RunID: id, Paths: paths})
	if err != nil {
		t.Fatal(err)
	}
	events, errs := lease.Runtime().Execute(ctx, fakeRequest(id, paths))
	<-events
	if err := lease.Runtime().Cancel(ctx, id); err != nil {
		t.Fatal(err)
	}
	for event := range events {
		if event.Type == "agent.completed" {
			t.Fatal("completed after cancelled")
		}
	}
	if err := <-errs; !errors.Is(err, agentexec.ErrOutcomeUnknown) {
		t.Fatalf("%v", err)
	}
	for range 2 {
		if err := lease.Runtime().Finalize(ctx, id, agentexec.Abort); err != nil {
			t.Fatal(err)
		}
	}
	if exists, err := lease.Runtime().SessionExists(ctx, agentexec.SessionID("fake-session:"+id.String())); exists || err != nil {
		t.Fatalf("%v %v", exists, err)
	}
}
