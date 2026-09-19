package sandbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"harness-forge.local/control-plane/internal/agentexec"
	"harness-forge.local/control-plane/internal/contracts"
)

func fakeRequest(id uuid.UUID, paths agentexec.Paths) agentexec.ExecuteRequest {
	return agentexec.ExecuteRequest{Version: "1", RunID: id, ProjectID: uuid.New(), ConversationID: uuid.New(), Prompt: "report", Profile: agentexec.Profile{ID: "geo-analysis", Version: "1", Digest: "test", Config: map[string]any{}}, Paths: paths, Limits: agentexec.Limits{MaxTurns: 8, MaxBudgetUSD: 2}}
}

func TestFakeScenarioSelectionAndOutputs(t *testing.T) {
	for _, tc := range []struct {
		prompt, terminal, heading string
		invalid                   bool
	}{
		{"ordinary report", "agent.completed", "Geographic report", false},
		{"[fixture:geo-report] report", "agent.completed", "Geographic report", false},
		{"not a prefix [fixture:agent-failure]", "agent.completed", "Geographic report", false},
		{"[fixture:success-v2] continue", "agent.completed", "Geographic report v2", false},
		{"[fixture:agent-failure] fail", "agent.failed", "", false},
		{"[fixture:invalid-manifest] publish", "agent.completed", "Invalid manifest report", true},
	} {
		t.Run(tc.prompt, func(t *testing.T) {
			p, err := NewFakeProvider(filepath.Join("..", "..", "..", "..", "tests", "fixtures", "fake-runtime"))
			if err != nil {
				t.Fatal(err)
			}
			id := uuid.New()
			paths := localPaths(t.TempDir(), id)
			lease, err := p.Acquire(context.Background(), AcquireRequest{RunID: id, Paths: paths})
			if err != nil {
				t.Fatal(err)
			}
			r := fakeRequest(id, paths)
			r.Prompt = tc.prompt
			events, errs := lease.Runtime().Execute(context.Background(), r)
			var got []agentexec.Event
			for event := range events {
				got = append(got, event)
			}
			for err := range errs {
				t.Fatal(err)
			}
			if err := contracts.ValidateRuntimeEventSequence(got); err != nil {
				t.Fatal(err)
			}
			if got[len(got)-1].Type != tc.terminal {
				t.Fatalf("terminal = %s, want %s", got[len(got)-1].Type, tc.terminal)
			}
			if tc.heading == "" {
				if _, err := os.Stat(paths.Outputs); !os.IsNotExist(err) {
					t.Fatalf("failure copied outputs: %v", err)
				}
				return
			}
			html, err := os.ReadFile(filepath.Join(paths.Outputs, "report", "index.html"))
			if err != nil || !strings.Contains(string(html), tc.heading) {
				t.Fatalf("HTML = %s, error %v", html, err)
			}
			manifest, err := os.ReadFile(filepath.Join(paths.Outputs, "artifact-manifest.json"))
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := contracts.ParseArtifactManifest(manifest)
			if (err != nil) != tc.invalid {
				t.Fatalf("manifest error %v, invalid %v", err, tc.invalid)
			}
			if !tc.invalid && !reflect.DeepEqual(parsed.Artifacts, got[len(got)-1].Payload.(contracts.AgentCompletedPayload).Artifacts) {
				t.Fatal("completed artifacts differ from manifest")
			}
		})
	}
}

func TestFakeScenarioRejectsUnknownSelector(t *testing.T) {
	for _, prompt := range []string{"[fixture:unknown]", "[fixture:../geo-report]", "[fixture:geo-report"} {
		p, err := NewFakeProvider(filepath.Join("..", "..", "..", "..", "tests", "fixtures", "fake-runtime"))
		if err != nil {
			t.Fatal(err)
		}
		id := uuid.New()
		paths := localPaths(t.TempDir(), id)
		lease, _ := p.Acquire(context.Background(), AcquireRequest{RunID: id, Paths: paths})
		r := fakeRequest(id, paths)
		r.Prompt = prompt
		events, errs := lease.Runtime().Execute(context.Background(), r)
		for event := range events {
			t.Errorf("unknown selector emitted %s", event.Type)
		}
		if err := <-errs; !errors.Is(err, agentexec.ErrInvalid) {
			t.Errorf("%s error = %v", prompt, err)
		}
	}
}

func TestFakeScenarioDelayedAndBlockingCancellation(t *testing.T) {
	for _, name := range []string{"delayed-success", "blocking"} {
		t.Run(name, func(t *testing.T) {
			p, err := NewFakeProvider(filepath.Join("..", "..", "..", "..", "tests", "fixtures", "fake-runtime"))
			if err != nil {
				t.Fatal(err)
			}
			waits := make(chan time.Duration)
			p.wait = func(ctx context.Context, delay time.Duration) error {
				select {
				case waits <- delay:
					return ctx.Err()
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			id := uuid.New()
			paths := localPaths(t.TempDir(), id)
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			lease, _ := p.Acquire(ctx, AcquireRequest{RunID: id, Paths: paths})
			r := fakeRequest(id, paths)
			r.Prompt = "[fixture:" + name + "]"
			events, errs := lease.Runtime().Execute(ctx, r)
			if name == "delayed-success" {
				<-events
				if delay := <-waits; delay != time.Second {
					t.Fatalf("delay = %v", delay)
				}
				<-events
			} else {
				for event := range events {
					if contracts.IsTerminalEvent(event) {
						t.Fatal("blocking emitted terminal without cancellation")
					}
					if delay := <-waits; delay != 50*time.Millisecond {
						t.Fatalf("delay = %v", delay)
					}
					if event.Type == "artifact.candidate" {
						break
					}
				}
				select {
				case event, open := <-events:
					t.Fatalf("blocking released before cancellation: event=%v open=%v", event, open)
				case <-time.After(50 * time.Millisecond):
				}
			}
			cancel()
			for event := range events {
				if contracts.IsTerminalEvent(event) {
					t.Fatal("terminal after cancellation")
				}
			}
			if err := <-errs; !errors.Is(err, agentexec.ErrOutcomeUnknown) {
				t.Fatal(err)
			}
			if err := lease.Runtime().Finalize(context.Background(), id, agentexec.Abort); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFakeScenarioDelayedCompletesWithExactInterEventDelays(t *testing.T) {
	p, err := NewFakeProvider(filepath.Join("..", "..", "..", "..", "tests", "fixtures", "fake-runtime"))
	if err != nil {
		t.Fatal(err)
	}
	var delays []time.Duration
	p.wait = func(_ context.Context, delay time.Duration) error { delays = append(delays, delay); return nil }
	id := uuid.New()
	paths := localPaths(t.TempDir(), id)
	lease, err := p.Acquire(context.Background(), AcquireRequest{RunID: id, Paths: paths})
	if err != nil {
		t.Fatal(err)
	}
	r := fakeRequest(id, paths)
	r.Prompt = "[fixture:delayed-success]"
	events, errs := lease.Runtime().Execute(context.Background(), r)
	var got []agentexec.Event
	for event := range events {
		got = append(got, event)
	}
	for err := range errs {
		t.Fatal(err)
	}
	if len(delays) != len(got)-1 {
		t.Fatalf("%d waits for %d events", len(delays), len(got))
	}
	for _, delay := range delays {
		if delay != time.Second {
			t.Fatalf("delay = %v", delay)
		}
	}
	if err := contracts.ValidateRuntimeEventSequence(got); err != nil {
		t.Fatal(err)
	}
	if got[len(got)-1].Type != "agent.completed" {
		t.Fatal("not completed")
	}
}

func TestFakeWaitCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitFakeEvent(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
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
