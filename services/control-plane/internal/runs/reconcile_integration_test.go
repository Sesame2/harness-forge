//go:build integration

package runs

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"harness-forge.local/control-plane/internal/agentexec"
	"harness-forge.local/control-plane/internal/objectstore"
	"harness-forge.local/control-plane/internal/sandbox"
	"harness-forge.local/control-plane/internal/workspaces"
)

type runtimeProbe struct {
	agentexec.Executor
	calls                            *[]string
	records                          []agentexec.Execution
	queryErr, finalizeErr, cancelErr error
	session                          bool
}

func TestOrphanCleanupRunsAfterRetainedRunAndNeverTreatsSuccessAsOrphan(t *testing.T) {
	pool := integrationPool(t)
	s := NewStore(pool)
	ctx := context.Background()
	retained := enqueue(t, pool)
	_, _ = pool.Exec(ctx, `UPDATE runs SET status='succeeded',sandbox_provider='fake',sandbox_ref='ref',finalized_at=now() WHERE id=$1`, retained.ID)
	orphan := uuid.New()
	calls := []string{}
	runtime := &runtimeProbe{calls: &calls, records: []agentexec.Execution{{RunID: orphan, Lifecycle: agentexec.Running}}}
	m := workspaces.NewMaterializer(t.TempDir(), objectstore.NewMemory())
	lease := &leaseProbe{runtime: runtime, calls: &calls, paths: m.Paths(orphan)}
	provider := &providerProbe{calls: &calls, lease: lease, listed: []sandbox.LeaseInfo{{RunID: retained.ID, Ref: "ref"}, {RunID: orphan, Ref: "ref"}}}
	if err := NewReconciler(s, sandbox.Binding{ID: sandbox.Fake, Provider: provider}, m).Reconcile(ctx, false); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []string{"list", "recover", "query", "cancel", "query", "sync", "finalize:abort", "release"}) {
		t.Fatal(calls)
	}
}

func TestReleaseBeforeFinalEventFailureRetriesOnlyFinalization(t *testing.T) {
	pool := integrationPool(t)
	s := NewStore(pool)
	ctx := context.Background()
	run := enqueue(t, pool)
	_, _ = pool.Exec(ctx, `UPDATE runs SET status='succeeded',phase='publishing',sandbox_provider='fake',sandbox_ref='ref',candidate_sdk_session_id='candidate' WHERE id=$1`, run.ID)
	_, _ = pool.Exec(ctx, `UPDATE conversations SET active_sdk_session_id='candidate' WHERE id=$1`, run.ConversationID)
	calls := []string{}
	runtime := &runtimeProbe{calls: &calls, session: true}
	m := workspaces.NewMaterializer(t.TempDir(), objectstore.NewMemory())
	lease := &leaseProbe{runtime: runtime, calls: &calls, paths: m.Paths(run.ID)}
	provider := &providerProbe{calls: &calls, lease: lease}
	reconciler := NewReconciler(s, sandbox.Binding{ID: sandbox.Fake, Provider: provider}, m)
	_, _ = pool.Exec(ctx, `ALTER TABLE run_events ADD CONSTRAINT reject_ack CHECK(type<>'run.succeeded')`)
	if err := reconciler.Reconcile(ctx, false); err == nil {
		t.Fatal("ack failure not injected")
	}
	got, _ := s.Read(ctx, run.ID)
	if got.FinalizedAt != nil || got.Status != Succeeded {
		t.Fatal(got)
	}
	_, _ = pool.Exec(ctx, `ALTER TABLE run_events DROP CONSTRAINT reject_ack`)
	if err := reconciler.Reconcile(ctx, true); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Read(ctx, run.ID)
	if got.FinalizedAt == nil {
		t.Fatal(got)
	}
	want := []string{"list", "recover", "finalize:commit", "head", "release", "list", "recover", "finalize:commit", "head", "release"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatal(calls)
	}
}

func TestContradictoryTombstoneRemainsHardAfterRuntimeListOmitsIt(t *testing.T) {
	pool := integrationPool(t)
	s := NewStore(pool)
	ctx := context.Background()
	run := enqueue(t, pool)
	_, _ = s.ClaimNext(ctx)
	_, _ = s.BeginSandboxAcquire(ctx, run.ID, "fake")
	_, _ = s.AttachSandboxRef(ctx, run.ID, "fake", "ref")
	run, err := s.RecordFailure(ctx, run.ID, Failed, failureDetail("original"))
	if err != nil {
		t.Fatal(err)
	}
	calls := []string{}
	runtime := &runtimeProbe{calls: &calls, records: []agentexec.Execution{{RunID: run.ID, Lifecycle: agentexec.AwaitingFinalize}}, finalizeErr: agentexec.ErrConflict}
	m := workspaces.NewMaterializer(t.TempDir(), objectstore.NewMemory())
	lease := &leaseProbe{runtime: runtime, calls: &calls, paths: m.Paths(run.ID)}
	provider := &providerProbe{calls: &calls, lease: lease, listed: []sandbox.LeaseInfo{{RunID: run.ID, Ref: "ref"}}}
	reconciler := NewReconciler(s, sandbox.Binding{ID: sandbox.Fake, Provider: provider}, m)
	if err := reconciler.Reconcile(ctx, true); !errors.Is(err, ErrConsistency) {
		t.Fatal(err)
	}
	runtime.records = nil
	runtime.finalizeErr = nil
	calls = nil
	if err := reconciler.Reconcile(ctx, true); !errors.Is(err, ErrConsistency) {
		t.Fatalf("forgot contradictory tombstone: %v calls=%v", err, calls)
	}
	got, _ := s.Read(ctx, run.ID)
	if got.FinalizedAt != nil || got.Status != Failed {
		t.Fatal(got)
	}
	for _, call := range calls {
		if call == "release" {
			t.Fatal("released after known contradictory tombstone")
		}
	}
}

func (p *runtimeProbe) ListExecutions(context.Context) ([]agentexec.Execution, error) {
	*p.calls = append(*p.calls, "query")
	return p.records, p.queryErr
}
func (p *runtimeProbe) Cancel(context.Context, uuid.UUID) error {
	*p.calls = append(*p.calls, "cancel")
	if p.cancelErr == nil {
		for i := range p.records {
			p.records[i].Lifecycle = agentexec.AwaitingFinalize
		}
	}
	return p.cancelErr
}
func (p *runtimeProbe) Finalize(_ context.Context, _ uuid.UUID, d agentexec.Decision) error {
	*p.calls = append(*p.calls, "finalize:"+string(d))
	return p.finalizeErr
}
func (p *runtimeProbe) SessionExists(context.Context, agentexec.SessionID) (bool, error) {
	*p.calls = append(*p.calls, "head")
	return p.session, nil
}

type leaseProbe struct {
	runtime             *runtimeProbe
	paths               agentexec.Paths
	calls               *[]string
	syncErr, releaseErr error
}

func (p *leaseProbe) Ref() string                 { return "ref" }
func (p *leaseProbe) Runtime() agentexec.Executor { return p.runtime }
func (p *leaseProbe) Paths() agentexec.Paths      { return p.paths }
func (p *leaseProbe) SyncBack(context.Context) error {
	*p.calls = append(*p.calls, "sync")
	return p.syncErr
}
func (p *leaseProbe) Release(context.Context) error {
	*p.calls = append(*p.calls, "release")
	return p.releaseErr
}

type providerProbe struct {
	lease               *leaseProbe
	calls               *[]string
	listed              []sandbox.LeaseInfo
	listErr, recoverErr error
}

func (p *providerProbe) Acquire(context.Context, sandbox.AcquireRequest) (sandbox.Lease, error) {
	*p.calls = append(*p.calls, "acquire")
	return p.lease, nil
}
func (p *providerProbe) List(context.Context) ([]sandbox.LeaseInfo, error) {
	*p.calls = append(*p.calls, "list")
	return p.listed, p.listErr
}
func (p *providerProbe) Recover(context.Context, sandbox.RecoverRequest) (sandbox.Lease, error) {
	*p.calls = append(*p.calls, "recover")
	return p.lease, p.recoverErr
}

func TestReconcilePriorityAndAmbiguousExecutionAuthority(t *testing.T) {
	tests := []struct {
		name       string
		status     Status
		record     bool
		active     bool
		queryErr   error
		ref        bool
		resource   bool
		recoverErr error
		releaseErr error
		syncErr    error
		promoted   bool
		want       []string
		final      bool
		wantStatus Status
		hard       bool
	}{
		{name: "failed accepted execution", status: Failed, record: true, active: true, ref: true, resource: true, want: []string{"list", "recover", "query", "cancel", "query", "sync", "finalize:abort", "release"}, final: true, wantStatus: Failed},
		{name: "failed authoritative absent", status: Failed, ref: true, resource: true, want: []string{"list", "recover", "query", "sync", "release"}, final: true, wantStatus: Failed},
		{name: "failed execution unknown", status: Failed, ref: true, resource: true, queryErr: agentexec.ErrUnavailable, want: []string{"list", "recover", "query"}, wantStatus: Failed},
		{name: "running execution unknown", status: Running, ref: true, resource: true, queryErr: agentexec.ErrUnavailable, want: []string{"list", "recover", "query"}, wantStatus: Running},
		{name: "cancelled active", status: Cancelled, record: true, active: true, ref: true, resource: true, want: []string{"list", "recover", "query", "cancel", "query", "sync", "finalize:abort", "release"}, final: true, wantStatus: Cancelled},
		{name: "running no execution", status: Running, ref: true, resource: true, want: []string{"list", "recover", "query", "sync", "release"}, final: true, wantStatus: Interrupted},
		{name: "running worker", status: Running, record: true, active: true, ref: true, resource: true, want: []string{"list", "recover", "query", "cancel", "query", "sync", "finalize:abort", "release"}, final: true, wantStatus: Interrupted},
		{name: "running lease gone", status: Running, ref: true, recoverErr: sandbox.ErrNotFound, want: []string{"list", "recover"}, final: true, wantStatus: Interrupted},
		{name: "running intent no resource", status: Running, want: []string{"list"}, final: true, wantStatus: Interrupted},
		{name: "failed acquire absent", status: Failed, want: []string{"list"}, final: true, wantStatus: Failed},
		{name: "acquire lost ack", status: Failed, resource: true, record: true, want: []string{"list", "recover", "query", "sync", "finalize:abort", "release"}, final: true, wantStatus: Failed},
		{name: "release failed", status: Failed, record: true, ref: true, resource: true, releaseErr: sandbox.ErrUnavailable, want: []string{"list", "recover", "query", "sync", "finalize:abort", "release"}, wantStatus: Failed},
		{name: "diagnostic sync failed", status: Failed, record: true, ref: true, resource: true, syncErr: sandbox.ErrUnavailable, want: []string{"list", "recover", "query", "sync", "finalize:abort", "release"}, final: true, wantStatus: Failed},
		{name: "success tombstone", status: Succeeded, ref: true, resource: true, promoted: true, want: []string{"list", "recover", "finalize:commit", "head", "release"}, final: true, wantStatus: Succeeded},
		{name: "success unpromoted", status: Succeeded, ref: true, resource: true, record: true, want: []string{"list", "recover", "query", "sync", "finalize:abort", "release"}, wantStatus: Succeeded, hard: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool := integrationPool(t)
			s := NewStore(pool)
			ctx := context.Background()
			run := enqueue(t, pool)
			var ref *string
			if tt.ref {
				v := "ref"
				ref = &v
			}
			_, err := pool.Exec(ctx, `UPDATE runs SET status=$2,phase='agent',sandbox_provider='fake',sandbox_ref=$3,candidate_sdk_session_id=CASE WHEN $2='succeeded' THEN 'candidate' END,error='{"code":"original"}' WHERE id=$1`, run.ID, tt.status, ref)
			if err != nil {
				t.Fatal(err)
			}
			if tt.promoted {
				_, _ = pool.Exec(ctx, `UPDATE conversations SET active_sdk_session_id='candidate' WHERE id=$1`, run.ConversationID)
			}
			calls := []string{}
			runtime := &runtimeProbe{calls: &calls, queryErr: tt.queryErr, session: true}
			if tt.record {
				lifecycle := agentexec.AwaitingFinalize
				if tt.active {
					lifecycle = agentexec.Running
				}
				runtime.records = []agentexec.Execution{{RunID: run.ID, Lifecycle: lifecycle}}
			}
			materializer := workspaces.NewMaterializer(t.TempDir(), objectstore.NewMemory())
			lease := &leaseProbe{runtime: runtime, paths: materializer.Paths(run.ID), calls: &calls, releaseErr: tt.releaseErr, syncErr: tt.syncErr}
			provider := &providerProbe{lease: lease, calls: &calls, recoverErr: tt.recoverErr}
			if tt.resource {
				provider.listed = []sandbox.LeaseInfo{{RunID: run.ID, Ref: "ref"}}
			}
			err = NewReconciler(s, sandbox.Binding{ID: sandbox.Fake, Provider: provider}, materializer).Reconcile(ctx, false)
			if tt.hard && !errors.Is(err, ErrConsistency) {
				t.Fatalf("hard error=%v", err)
			}
			if !reflect.DeepEqual(calls, tt.want) {
				t.Fatalf("calls=%v want=%v err=%v", calls, tt.want, err)
			}
			got, _ := s.Read(ctx, run.ID)
			if (got.FinalizedAt != nil) != tt.final || got.Status != tt.wantStatus {
				t.Fatalf("run=%#v err=%v", got, err)
			}
			events, _ := s.ListEvents(ctx, run.ID, 0)
			terminalCount := 0
			for _, e := range events {
				if e.Type == "run."+string(tt.wantStatus) {
					terminalCount++
				}
			}
			if tt.final && terminalCount != 1 || !tt.final && terminalCount != 0 {
				t.Fatal(events)
			}
			if tt.syncErr != nil {
				var original, diagnostic string
				if err := pool.QueryRow(ctx, `SELECT error->>'code',error->'diagnostic_sync'->>'code' FROM runs WHERE id=$1`, run.ID).Scan(&original, &diagnostic); err != nil || original != "original" || diagnostic == "" {
					t.Fatalf("diagnostic original=%s detail=%s err=%v", original, diagnostic, err)
				}
			}
		})
	}
}
