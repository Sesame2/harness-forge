//go:build integration

package runs

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"harness-forge.local/control-plane/internal/agentexec"
	"harness-forge.local/control-plane/internal/artifacts"
	"harness-forge.local/control-plane/internal/objectstore"
	"harness-forge.local/control-plane/internal/profiles"
	"harness-forge.local/control-plane/internal/sandbox"
	"harness-forge.local/control-plane/internal/workspaces"
)

type gatedProvider struct {
	sandbox.Provider
	allow   <-chan struct{}
	queried chan<- struct{}
}

type countingProvider struct {
	sandbox.Provider
	count *atomic.Int64
}

func (p countingProvider) List(ctx context.Context) ([]sandbox.LeaseInfo, error) {
	p.count.Add(1)
	return p.Provider.List(ctx)
}
func TestSchedulerLatchesHardConsistencyUntilOperatorRestart(t *testing.T) {
	pool := integrationPool(t)
	s := NewStore(pool)
	queued := enqueue(t, pool)
	orphan := uuid.New()
	calls := []string{}
	runtime := &runtimeProbe{calls: &calls, records: []agentexec.Execution{{RunID: orphan, Lifecycle: agentexec.AwaitingFinalize}}, finalizeErr: agentexec.ErrConflict}
	m := workspaces.NewMaterializer(t.TempDir(), objectstore.NewMemory())
	lease := &leaseProbe{calls: &calls, runtime: runtime, paths: m.Paths(orphan)}
	base := &providerProbe{calls: &calls, lease: lease, listed: []sandbox.LeaseInfo{{RunID: orphan, Ref: "ref"}}}
	var count atomic.Int64
	binding := sandbox.Binding{ID: sandbox.Fake, Provider: countingProvider{base, &count}}
	scheduler := NewScheduler(s, nil, NewReconciler(s, binding, m))
	scheduler.retry = time.Millisecond
	scheduler.poll = time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	scheduler.Run(ctx)
	if count.Load() != 1 {
		t.Fatalf("hard consistency retried provider %d times", count.Load())
	}
	got, _ := s.Read(context.Background(), queued.ID)
	if got.Status != Queued || scheduler.Ready() {
		t.Fatal("claim after hard consistency", got)
	}
}

func (p gatedProvider) List(ctx context.Context) ([]sandbox.LeaseInfo, error) {
	select {
	case p.queried <- struct{}{}:
	default:
	}
	select {
	case <-p.allow:
		return p.Provider.List(ctx)
	default:
		return nil, sandbox.ErrUnavailable
	}
}
func TestSchedulerWaitsForStartupAuthorityThenClaims(t *testing.T) {
	pool := integrationPool(t)
	s := NewStore(pool)
	objects := objectstore.NewMemory()
	root := t.TempDir()
	r := enqueue(t, pool)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resolver, err := profiles.NewResolver("../../../../profiles")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := sandbox.NewFakeProvider("../../../../tests/fixtures/fake-runtime")
	if err != nil {
		t.Fatal(err)
	}
	allow := make(chan struct{})
	queried := make(chan struct{}, 1)
	binding := sandbox.Binding{ID: sandbox.Fake, Provider: gatedProvider{provider, allow, queried}}
	m := workspaces.NewMaterializer(root, objects)
	c := NewCoordinator(s, resolver, m, binding, artifacts.NewPublisher(pool, objects))
	scheduler := NewScheduler(s, c, NewReconciler(s, binding, m))
	scheduler.poll = 5 * time.Millisecond
	scheduler.retry = 5 * time.Millisecond
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, r.ID.String(), "inputs"), 0770) })
	done := make(chan struct{})
	go func() { scheduler.Run(ctx); close(done) }()
	select {
	case <-queried:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	got, _ := s.Read(ctx, r.ID)
	if got.Status != Queued || scheduler.Ready() {
		t.Fatal("claim escaped startup reconciliation")
	}
	close(allow)
	for {
		got, err = s.Read(ctx, r.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.FinalizedAt != nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	<-done
	if got.Status != Succeeded {
		t.Fatal(got)
	}
}

func TestSchedulerRecoversItsReturnedRunWhenFailurePersistenceFailed(t *testing.T) {
	pool := integrationPool(t)
	s := NewStore(pool)
	run := enqueue(t, pool)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := pool.Exec(ctx, `ALTER TABLE runs ADD CONSTRAINT reject_failure_status CHECK(status<>'failed')`)
	if err != nil {
		t.Fatal(err)
	}
	objects := objectstore.NewMemory()
	m := workspaces.NewMaterializer(t.TempDir(), objects)
	t.Cleanup(func() { _ = os.Chmod(m.Paths(run.ID).Inputs, 0770) })
	resolver, err := profiles.NewResolver("../../../../profiles")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := sandbox.NewFakeProvider("../../../../tests/fixtures/fake-runtime")
	if err != nil {
		t.Fatal(err)
	}
	binding := sandbox.Binding{ID: sandbox.Fake, Provider: provider}
	c := NewCoordinator(s, resolver, m, binding, artifacts.NewPublisher(pool, publicationFailureStore{Store: objects, beforePut: func() {}}))
	scheduler := NewScheduler(s, c, NewReconciler(s, binding, m))
	scheduler.poll = time.Millisecond
	scheduler.retry = time.Millisecond
	done := make(chan struct{})
	go func() { scheduler.Run(ctx); close(done) }()
	for {
		got, err := s.Read(ctx, run.ID)
		if err != nil {
			cancel()
			<-done
			t.Fatal(err)
		}
		if got.FinalizedAt != nil {
			if got.Status != Interrupted {
				t.Error(got)
			}
			cancel()
			<-done
			return
		}
		select {
		case <-ctx.Done():
			<-done
			t.Fatal("returned running residue was never reconciled")
		case <-time.After(time.Millisecond):
		}
	}
}

type lostClaimCommitAck struct {
	armed     atomic.Bool
	committed chan error
}

func (p *lostClaimCommitAck) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if data.SQL == "commit" && p.armed.CompareAndSwap(true, false) {
		// Commit through pgconn first (bypassing this pgx tracer), then cancel
		// only the outward COMMIT call: durable success with an uncertain ACK.
		_, err := conn.PgConn().Exec(ctx, "commit").ReadAll()
		p.committed <- err
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		return cancelled
	}
	return ctx
}
func (*lostClaimCommitAck) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestSchedulerRecoversClaimCommittedWithoutAcknowledgement(t *testing.T) {
	pool := integrationPool(t)
	run := enqueue(t, pool)
	next := enqueue(t, pool)
	tracer := &lostClaimCommitAck{committed: make(chan error, 1)}
	tracer.armed.Store(true)
	config := pool.Config()
	config.ConnConfig.Tracer = tracer
	claimPool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer claimPool.Close()
	s := NewStore(claimPool)
	provider, err := sandbox.NewFakeProvider("../../../../tests/fixtures/fake-runtime")
	if err != nil {
		t.Fatal(err)
	}
	objects := objectstore.NewMemory()
	materializer := workspaces.NewMaterializer(t.TempDir(), objects)
	t.Cleanup(func() { _ = os.Chmod(materializer.Paths(next.ID).Inputs, 0770) })
	resolver, err := profiles.NewResolver("../../../../profiles")
	if err != nil {
		t.Fatal(err)
	}
	binding := sandbox.Binding{ID: sandbox.Fake, Provider: provider}
	coordinator := NewCoordinator(s, resolver, materializer, binding, artifacts.NewPublisher(claimPool, objects))
	scheduler := NewScheduler(s, coordinator, NewReconciler(s, binding, materializer))
	scheduler.retry = time.Millisecond
	scheduler.poll = time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { scheduler.Run(ctx); close(done) }()
	select {
	case err := <-tracer.committed:
		if err != nil {
			cancel()
			<-done
			t.Fatal("server COMMIT failed:", err)
		}
	case <-ctx.Done():
		<-done
		t.Fatal("claim never committed")
	}
	for {
		got, err := NewStore(pool).Read(context.Background(), run.ID)
		if err != nil {
			cancel()
			<-done
			t.Fatal(err)
		}
		following, err := NewStore(pool).Read(context.Background(), next.ID)
		if err != nil {
			cancel()
			<-done
			t.Fatal(err)
		}
		if got.FinalizedAt != nil && following.FinalizedAt != nil {
			cancel()
			<-done
			if got.Status != Interrupted || got.SandboxProvider != nil {
				t.Fatal("uncertain claim executed or was not interrupted", got)
			}
			if following.Status != Succeeded {
				t.Fatal("queue did not advance after uncertain claim", following)
			}
			return
		}
		select {
		case <-ctx.Done():
			<-done
			t.Fatal("committed claim stranded without Execute")
		case <-time.After(time.Millisecond):
		}
	}
}
