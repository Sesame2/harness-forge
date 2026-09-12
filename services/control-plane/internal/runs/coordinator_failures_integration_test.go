//go:build integration

package runs

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"harness-forge.local/control-plane/internal/agentexec"
	"harness-forge.local/control-plane/internal/artifacts"
	"harness-forge.local/control-plane/internal/contracts"
	"harness-forge.local/control-plane/internal/objectstore"
	"harness-forge.local/control-plane/internal/profiles"
	"harness-forge.local/control-plane/internal/sandbox"
	"harness-forge.local/control-plane/internal/workspaces"
)

type executeProbe struct {
	runtimeProbe
	execute func(context.Context, agentexec.ExecuteRequest) (<-chan agentexec.Event, <-chan error)
}

func (p *executeProbe) Execute(ctx context.Context, r agentexec.ExecuteRequest) (<-chan agentexec.Event, <-chan error) {
	return p.execute(ctx, r)
}

type executeLease struct {
	*leaseProbe
	executor agentexec.Executor
}

func (l *executeLease) Runtime() agentexec.Executor { return l.executor }

type executeProvider struct {
	sandbox.Provider
	lease   sandbox.Lease
	acquire func(context.Context, sandbox.AcquireRequest) (sandbox.Lease, error)
}

func (p executeProvider) Acquire(ctx context.Context, r sandbox.AcquireRequest) (sandbox.Lease, error) {
	if p.acquire != nil {
		return p.acquire(ctx, r)
	}
	return p.lease, nil
}

func TestExecuteAmbiguityRequiresAuthorityBeforeCleanup(t *testing.T) {
	for _, name := range []string{"accepted", "absent", "unknown", "committed"} {
		t.Run(name, func(t *testing.T) {
			pool := integrationPool(t)
			s := NewStore(pool)
			ctx := context.Background()
			run := enqueue(t, pool)
			claimed, err := s.ClaimNext(ctx)
			if err != nil {
				t.Fatal(err)
			}
			objects := objectstore.NewMemory()
			root := t.TempDir()
			m := workspaces.NewMaterializer(root, objects)
			t.Cleanup(func() { _ = os.Chmod(m.Paths(run.ID).Inputs, 0770) })
			resolver, err := profiles.NewResolver("../../../../profiles")
			if err != nil {
				t.Fatal(err)
			}
			calls := []string{}
			runtime := &executeProbe{runtimeProbe: runtimeProbe{calls: &calls}}
			if name == "accepted" {
				runtime.records = []agentexec.Execution{{RunID: run.ID, Lifecycle: agentexec.Running}}
			}
			if name == "unknown" {
				runtime.queryErr = agentexec.ErrUnavailable
			}
			runtime.execute = func(_ context.Context, request agentexec.ExecuteRequest) (<-chan agentexec.Event, <-chan error) {
				calls = append(calls, "execute")
				stored, e := s.Read(ctx, run.ID)
				if e != nil || stored.SandboxRef == nil || stored.SandboxProvider == nil {
					t.Error("execute before provider identity durable")
				}
				events := make(chan agentexec.Event)
				errs := make(chan error, 1)
				if name == "committed" {
					errs <- &agentexec.RuntimeError{Kind: agentexec.ErrFinalized, Decision: agentexec.Commit}
				} else {
					errs <- agentexec.ErrOutcomeUnknown
				}
				close(events)
				close(errs)
				return events, errs
			}
			base := &leaseProbe{calls: &calls, paths: m.Paths(run.ID)}
			lease := &executeLease{base, runtime}
			c := NewCoordinator(s, resolver, m, sandbox.Binding{ID: sandbox.Fake, Provider: executeProvider{lease: lease}}, artifacts.NewPublisher(pool, objects))
			if err := c.Execute(ctx, *claimed); err == nil {
				t.Fatal("ambiguous execute succeeded")
			}
			want := []string{"execute", "query"}
			if name == "committed" {
				want = []string{"execute"}
			}
			if name == "accepted" {
				want = append(want, "cancel", "query", "sync", "finalize:abort", "release")
			}
			if name == "absent" {
				want = append(want, "sync", "release")
			}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("calls=%v want=%v", calls, want)
			}
			got, _ := s.Read(ctx, run.ID)
			if got.Status != Failed || (got.FinalizedAt != nil) != (name != "unknown" && name != "committed") {
				t.Fatal(got)
			}
		})
	}
}

func TestCancellationDuringMaterializationNeverLosesRunIdentity(t *testing.T) {
	pool := integrationPool(t)
	s := NewStore(pool)
	run := enqueue(t, pool)
	ctx := context.Background()
	claimed, _ := s.ClaimNext(ctx)
	objects := objectstore.NewMemory()
	root := t.TempDir()
	m := workspaces.NewMaterializer(root, objects)
	resolver, err := profiles.NewResolver("../../../../profiles")
	if err != nil {
		t.Fatal(err)
	}
	c := NewCoordinator(s, resolver, m, sandbox.Binding{ID: sandbox.Fake}, artifacts.NewPublisher(pool, objects))
	_, err = c.Cancel(ctx, *claimed)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, run.ID.String(), "inputs"), 0770) })
	_ = c.Execute(ctx, *claimed)
	got, _ := s.Read(ctx, run.ID)
	if got.Status != Cancelled || got.FinalizedAt == nil {
		t.Fatalf("cancelled preparation stranded: %#v", got)
	}
}

func TestEventPumpDrainsBothChannelsAndRejectsTrailingTerminal(t *testing.T) {
	pool := integrationPool(t)
	s := NewStore(pool)
	run := enqueue(t, pool)
	_, _ = s.ClaimNext(context.Background())
	c := &Coordinator{store: s}
	events := make(chan agentexec.Event)
	errs := make(chan error)
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		events <- contracts.RuntimeEvent{Version: "1", RunID: run.ID.String(), Sequence: 1, Type: "agent.failed", OccurredAt: time.Now().UTC(), Payload: contracts.AgentFailedPayload{Code: "failed", Message: "original"}}
		events <- contracts.RuntimeEvent{Version: "1", RunID: run.ID.String(), Sequence: 2, Type: "agent.failed", OccurredAt: time.Now().UTC(), Payload: contracts.AgentFailedPayload{Code: "again", Message: "again"}}
		close(events)
		errs <- errors.New("late producer error")
		close(errs)
	}()
	_, err := c.pump(context.Background(), run.ID, events, errs)
	if err == nil {
		t.Fatal("invalid stream accepted")
	}
	<-drained
	got, _ := s.Read(context.Background(), run.ID)
	if got.Status != Failed || got.FinalizedAt != nil {
		t.Fatal(got)
	}
}

func TestRuntimePhaseReplayDoesNotRegressPhase(t *testing.T) {
	pool := integrationPool(t)
	s := NewStore(pool)
	run := enqueue(t, pool)
	ctx := context.Background()
	_, _ = s.ClaimNext(ctx)
	e := contracts.RuntimeEvent{Version: "1", RunID: run.ID.String(), Sequence: 1, Type: "phase.changed", OccurredAt: time.Now().UTC(), Payload: contracts.PhaseChangedPayload{Phase: "preparing"}}
	if err := s.RecordRuntimeEvent(ctx, run.ID, e); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPhase(ctx, run.ID, Agent); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordRuntimeEvent(ctx, run.ID, e); err != nil {
		t.Fatal("replay is not idempotent:", err)
	}
	got, _ := s.Read(ctx, run.ID)
	if got.Phase == nil || *got.Phase != Agent {
		t.Fatal(got)
	}
}

func TestEventPumpIgnoresUnknownAfterKnownTerminal(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(previous)
	pool := integrationPool(t)
	s := NewStore(pool)
	run := enqueue(t, pool)
	_, _ = s.ClaimNext(context.Background())
	c := &Coordinator{store: s}
	events := make(chan agentexec.Event, 3)
	errs := make(chan error)
	for i, e := range []contracts.RuntimeEvent{
		{Type: "artifact.candidate", Payload: contracts.ArtifactCandidatePayload{Artifacts: []contracts.ArtifactCandidate{}}},
		{Type: "agent.completed", Payload: contracts.AgentCompletedPayload{CandidateSDKSessionID: "candidate", Artifacts: []contracts.ArtifactCandidate{}}},
		{Type: "debug.extension", Payload: map[string]any{"info": "ignored"}},
	} {
		e.Version = "1"
		e.RunID = run.ID.String()
		e.Sequence = uint64(i + 1)
		e.OccurredAt = time.Now().UTC()
		events <- e
	}
	close(events)
	close(errs)
	if _, err := c.pump(context.Background(), run.ID, events, errs); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "debug.extension") || strings.Contains(logs.String(), "info=") {
		t.Fatalf("unknown event debug log=%q", logs.String())
	}
	stored, _ := s.ListEvents(context.Background(), run.ID, 0)
	if len(stored) != 2 {
		t.Fatal(stored)
	}
}

func TestAcquireLostAckAndRefWriteFailureRecoverWithoutExecute(t *testing.T) {
	for _, kind := range []string{"lost_ack", "ref_write"} {
		t.Run(kind, func(t *testing.T) {
			pool := integrationPool(t)
			s := NewStore(pool)
			ctx := context.Background()
			run := enqueue(t, pool)
			claimed, _ := s.ClaimNext(ctx)
			objects := objectstore.NewMemory()
			m := workspaces.NewMaterializer(t.TempDir(), objects)
			t.Cleanup(func() { _ = os.Chmod(m.Paths(run.ID).Inputs, 0770) })
			resolver, err := profiles.NewResolver("../../../../profiles")
			if err != nil {
				t.Fatal(err)
			}
			calls := []string{}
			runtime := &runtimeProbe{calls: &calls}
			lease := &leaseProbe{runtime: runtime, calls: &calls, paths: m.Paths(run.ID)}
			provider := &providerProbe{calls: &calls, lease: lease, listed: []sandbox.LeaseInfo{{RunID: run.ID, Ref: "ref"}}}
			wrapped := executeProvider{Provider: provider, acquire: func(_ context.Context, request sandbox.AcquireRequest) (sandbox.Lease, error) {
				stored, _ := s.Read(ctx, request.RunID)
				if stored.SandboxProvider == nil || *stored.SandboxProvider != "fake" {
					t.Error("Acquire before intent")
				}
				if kind == "lost_ack" {
					return nil, sandbox.ErrOutcomeUnknown
				}
				return lease, nil
			}}
			if kind == "ref_write" {
				if _, err := pool.Exec(ctx, `ALTER TABLE runs ADD CONSTRAINT reject_ref CHECK(sandbox_ref IS NULL)`); err != nil {
					t.Fatal(err)
				}
			}
			c := NewCoordinator(s, resolver, m, sandbox.Binding{ID: sandbox.Fake, Provider: wrapped}, artifacts.NewPublisher(pool, objects))
			if err := c.Execute(ctx, *claimed); err == nil {
				t.Fatal("expected acquisition boundary error")
			}
			got, _ := s.Read(ctx, run.ID)
			if got.Status != Failed || got.FinalizedAt != nil || got.SandboxRef != nil {
				t.Fatal(got)
			}
			if kind == "ref_write" {
				_, _ = pool.Exec(ctx, `ALTER TABLE runs DROP CONSTRAINT reject_ref`)
			}
			if err := NewReconciler(s, sandbox.Binding{ID: sandbox.Fake, Provider: wrapped}, m).Reconcile(ctx, true); err != nil {
				t.Fatal(err)
			}
			got, _ = s.Read(ctx, run.ID)
			if got.Status != Failed || got.FinalizedAt == nil || got.SandboxRef == nil {
				t.Fatal(got)
			}
			if !reflect.DeepEqual(calls, []string{"list", "recover", "query", "sync", "release"}) {
				t.Fatal(calls)
			}
		})
	}
}

func TestActiveCancelPersistsBeforeWorkerStopAndNeverResurrects(t *testing.T) {
	pool := integrationPool(t)
	s := NewStore(pool)
	ctx := context.Background()
	run := enqueue(t, pool)
	claimed, _ := s.ClaimNext(ctx)
	objects := objectstore.NewMemory()
	m := workspaces.NewMaterializer(t.TempDir(), objects)
	t.Cleanup(func() { _ = os.Chmod(m.Paths(run.ID).Inputs, 0770) })
	resolver, err := profiles.NewResolver("../../../../profiles")
	if err != nil {
		t.Fatal(err)
	}
	calls := []string{}
	started := make(chan struct{})
	runtime := &executeProbe{runtimeProbe: runtimeProbe{calls: &calls, records: []agentexec.Execution{{RunID: run.ID, Lifecycle: agentexec.Running}}}}
	runtime.execute = func(executionCtx context.Context, _ agentexec.ExecuteRequest) (<-chan agentexec.Event, <-chan error) {
		events := make(chan agentexec.Event)
		errs := make(chan error, 1)
		go func() {
			close(started)
			<-executionCtx.Done()
			stored, e := s.Read(ctx, run.ID)
			if e != nil || stored.Status != Cancelled || stored.FinalizedAt != nil {
				t.Error("cancellation not persisted before stop", stored, e)
			}
			errs <- agentexec.ErrOutcomeUnknown
			close(events)
			close(errs)
		}()
		return events, errs
	}
	lease := &executeLease{&leaseProbe{calls: &calls, paths: m.Paths(run.ID)}, runtime}
	c := NewCoordinator(s, resolver, m, sandbox.Binding{ID: sandbox.Fake, Provider: executeProvider{lease: lease}}, artifacts.NewPublisher(pool, objects))
	done := make(chan error, 1)
	go func() { done <- c.Execute(ctx, *claimed) }()
	<-started
	got, err := c.Cancel(ctx, *claimed)
	if err != nil || got.Status != Cancelled {
		t.Fatal(got, err)
	}
	<-done
	got, _ = s.Read(ctx, run.ID)
	if got.Status != Cancelled || got.FinalizedAt == nil {
		t.Fatal(got)
	}
	if !reflect.DeepEqual(calls, []string{"query", "cancel", "query", "sync", "finalize:abort", "release"}) {
		t.Fatal(calls)
	}
}

func TestPublishingCancellationIsConflict(t *testing.T) {
	pool := integrationPool(t)
	s := NewStore(pool)
	ctx := context.Background()
	run := enqueue(t, pool)
	_, _ = s.ClaimNext(ctx)
	_ = s.SetPhase(ctx, run.ID, Agent)
	_ = s.SetPhase(ctx, run.ID, Publishing)
	c := &Coordinator{store: s}
	if _, err := c.Cancel(ctx, run); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	got, _ := s.Read(ctx, run.ID)
	if got.Status != Running {
		t.Fatal(got)
	}
}

type blackholeRuntime struct {
	agentexec.Executor
	entered chan struct{}
}

func (r blackholeRuntime) ListExecutions(ctx context.Context) ([]agentexec.Execution, error) {
	close(r.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}
func TestCleanupDeadlineKeepsUnknownWorkerUnfinalizedWithoutRelease(t *testing.T) {
	pool := integrationPool(t)
	s := NewStore(pool)
	run := enqueue(t, pool)
	ctx := context.Background()
	_, _ = s.ClaimNext(ctx)
	run, err := s.RecordFailure(ctx, run.ID, Failed, failureDetail("original"))
	if err != nil {
		t.Fatal(err)
	}
	calls := []string{}
	entered := make(chan struct{})
	lease := &executeLease{&leaseProbe{calls: &calls}, blackholeRuntime{entered: entered}}
	attempt, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	err = NewReconciler(s, sandbox.Binding{}, nil).finishLease(attempt, run, lease)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	<-entered
	got, _ := s.Read(ctx, run.ID)
	if got.Status != Failed || got.FinalizedAt != nil || len(calls) != 0 {
		t.Fatalf("unsafe timeout cleanup %#v calls=%v", got, calls)
	}
}

type diagnosticDeadlineLease struct {
	*leaseProbe
	t *testing.T
}

func (l diagnosticDeadlineLease) SyncBack(ctx context.Context) error {
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > 6*time.Second {
		l.t.Error("diagnostic sync lacks a short independent deadline")
	}
	return context.DeadlineExceeded
}
func TestDiagnosticSyncHasOwnBudgetAndDoesNotPreventRelease(t *testing.T) {
	pool := integrationPool(t)
	s := NewStore(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	run := enqueue(t, pool)
	_, _ = s.ClaimNext(ctx)
	run, err := s.RecordFailure(ctx, run.ID, Failed, failureDetail("original"))
	if err != nil {
		t.Fatal(err)
	}
	calls := []string{}
	runtime := &runtimeProbe{calls: &calls}
	lease := diagnosticDeadlineLease{&leaseProbe{runtime: runtime, calls: &calls}, t}
	if err := NewReconciler(s, sandbox.Binding{}, nil).finishLease(ctx, run, lease); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []string{"query", "release"}) {
		t.Fatal(calls)
	}
}
