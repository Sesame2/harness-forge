//go:build integration

package runs

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"harness-forge.local/control-plane/internal/postgres"
	"harness-forge.local/control-plane/internal/testsupport"
	"harness-forge.local/control-plane/migrations"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func integrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, schema := testsupport.NewPostgresSchema(t, os.Getenv("TEST_DATABASE_URL"))
	if err := postgres.Migrate(context.Background(), pool, schema); err != nil {
		t.Fatal(err)
	}
	return pool
}

func seedRun(t *testing.T, pool *pgxpool.Pool) Run {
	t.Helper()
	ctx := context.Background()
	project, conversation, message := uuid.New(), uuid.New(), uuid.New()
	for _, query := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO projects(id,name,profile_id,profile_version) VALUES($1,'p','geo-analysis','1')`, []any{project}},
		{`INSERT INTO conversations(id,project_id,title) VALUES($1,$2,'c')`, []any{conversation, project}},
		{`INSERT INTO messages(id,conversation_id,role,content) VALUES($1,$2,'user','build')`, []any{message, conversation}},
	} {
		if _, err := pool.Exec(ctx, query.sql, query.args...); err != nil {
			t.Fatal(err)
		}
	}
	return Run{ID: uuid.New(), ConversationID: conversation, TriggerMessageID: message}
}

func enqueue(t *testing.T, pool *pgxpool.Pool) Run {
	t.Helper()
	run := seedRun(t, pool)
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	run, err = NewStore(pool).CreateQueuedTx(context.Background(), tx, run)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	return run
}

func TestCreateQueuedJoinsCallerTransaction(t *testing.T) {
	pool := integrationPool(t)
	store, ctx := NewStore(pool), context.Background()
	run := seedRun(t, pool)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	created, err := store.CreateQueuedTx(ctx, tx, run)
	if err != nil || created.Status != Queued || created.CreatedAt.IsZero() {
		t.Fatalf("create = %#v, %v", created, err)
	}
	if _, err := store.Read(ctx, run.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("uncommitted run visible: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(ctx, run.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rollback left run: %v", err)
	}
}

func TestClaimBlocksUntilEveryActiveOutcomeFinalized(t *testing.T) {
	for _, status := range []Status{Running, Succeeded, Failed, Cancelled, Interrupted} {
		t.Run(string(status), func(t *testing.T) {
			pool := integrationPool(t)
			store, ctx := NewStore(pool), context.Background()
			first, second := enqueue(t, pool), enqueue(t, pool)
			if _, err := pool.Exec(ctx, `UPDATE runs SET status=$2 WHERE id=$1`, first.ID, status); err != nil {
				t.Fatal(err)
			}
			if got, err := store.ClaimNext(ctx); err != nil || got != nil {
				t.Fatalf("claimed past unfinalized %s: %#v %v", status, got, err)
			}
			if _, err := pool.Exec(ctx, `UPDATE runs SET finalized_at=now() WHERE id=$1`, first.ID); err != nil {
				t.Fatal(err)
			}
			got, err := store.ClaimNext(ctx)
			if err != nil || got == nil || got.ID != second.ID || got.Status != Running || got.Phase == nil || *got.Phase != Preparing {
				t.Fatalf("claim = %#v, %v", got, err)
			}
		})
	}
}

func TestConcurrentClaimsSerializeBeforeActiveCheck(t *testing.T) {
	pool := integrationPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	first, second := enqueue(t, pool), enqueue(t, pool)
	// Tie creation times deliberately: UUID is the deterministic FIFO tie breaker.
	if _, err := pool.Exec(ctx, `UPDATE runs SET created_at='2026-01-01T00:00:00Z'`); err != nil {
		t.Fatal(err)
	}
	want := first.ID
	if second.ID.String() < first.ID.String() {
		want = second.ID
	}
	locked, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	store1, store2 := NewStore(pool), NewStore(pool)
	store1.afterClaimLock = func() {
		close(locked)
		select {
		case <-release:
		case <-ctx.Done():
		}
	}
	type result struct {
		run *Run
		err error
	}
	done1, done2 := make(chan result, 1), make(chan result, 1)
	go func() { run, err := store1.ClaimNext(ctx); done1 <- result{run, err} }()
	select {
	case <-locked:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	go func() { run, err := store2.ClaimNext(ctx); done2 <- result{run, err} }()
	for {
		select {
		case got := <-done2:
			t.Fatalf("second claim bypassed held lock: %#v", got)
		default:
		}
		var waiting bool
		err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%harness-forge:run-claim%')`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	unblock()
	got1, got2 := <-done1, <-done2
	if got1.err != nil || got1.run == nil || got1.run.ID != want {
		t.Fatalf("first claim = %#v", got1)
	}
	if got2.err != nil || got2.run != nil {
		t.Fatalf("second claim = %#v", got2)
	}
}

func TestSandboxAcquisitionIntentAndAttachment(t *testing.T) {
	pool := integrationPool(t)
	store, ctx := NewStore(pool), context.Background()
	run := enqueue(t, pool)
	if _, err := store.BeginSandboxAcquire(ctx, run.ID, "docker"); !errors.Is(err, ErrConflict) {
		t.Fatalf("acquired queued run: %v", err)
	}
	if _, err := store.ClaimNext(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AttachSandboxRef(ctx, run.ID, "docker", "lease"); !errors.Is(err, ErrConflict) {
		t.Fatalf("attached before intent: %v", err)
	}
	for i := 0; i < 2; i++ {
		got, err := store.BeginSandboxAcquire(ctx, run.ID, "docker")
		if err != nil || got.SandboxProvider == nil || *got.SandboxProvider != "docker" || got.SandboxRef != nil {
			t.Fatalf("intent = %#v, %v", got, err)
		}
	}
	if _, err := store.BeginSandboxAcquire(ctx, run.ID, "other"); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed provider: %v", err)
	}
	// Unknown Acquire may already have set failed; reconciliation still attaches.
	if _, err := pool.Exec(ctx, `UPDATE runs SET status='failed' WHERE id=$1`, run.ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		got, err := store.AttachSandboxRef(ctx, run.ID, "docker", "lease")
		if err != nil || got.SandboxRef == nil || *got.SandboxRef != "lease" {
			t.Fatalf("attach = %#v, %v", got, err)
		}
	}
	for _, pair := range [][2]string{{"other", "lease"}, {"docker", "different"}, {"docker", ""}} {
		if _, err := store.AttachSandboxRef(ctx, run.ID, pair[0], pair[1]); !errors.Is(err, ErrConflict) {
			t.Fatalf("conflicting attach = %v", err)
		}
	}
	got, err := store.Read(ctx, run.ID)
	if err != nil || got.SandboxProvider == nil || got.SandboxRef == nil {
		t.Fatalf("persisted identity = %#v %v", got, err)
	}
}

func TestEventsDurableSequenceDedupAndFinalization(t *testing.T) {
	pool := integrationPool(t)
	store, ctx := NewStore(pool), context.Background()
	run := enqueue(t, pool)
	now := time.Now().UTC().Truncate(time.Microsecond)
	runtimeSequence := int64(7)
	event := Event{RunID: run.ID, RuntimeSequence: &runtimeSequence, Type: "assistant.delta", Payload: json.RawMessage(`{"text":"one"}`), OccurredAt: now}
	first, err := store.AppendEvent(ctx, event)
	if err != nil || first.Sequence != 1 {
		t.Fatalf("append = %#v %v", first, err)
	}
	event.Payload = json.RawMessage(`{"text":"duplicate"}`)
	duplicate, err := NewStore(pool).AppendEvent(ctx, event)
	if err != nil || duplicate.Sequence != first.Sequence || string(duplicate.Payload) != string(first.Payload) || !duplicate.OccurredAt.Equal(first.OccurredAt) {
		t.Fatalf("duplicate = %#v %v", duplicate, err)
	}
	event.RuntimeSequence = nil
	second, err := store.AppendEvent(ctx, event)
	if err != nil || second.Sequence != 2 {
		t.Fatalf("second = %#v %v", second, err)
	}
	const count = 12
	done := make(chan error, count)
	for i := 0; i < count; i++ {
		go func() { _, err := NewStore(pool).AppendEvent(ctx, event); done <- err }()
	}
	for i := 0; i < count; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	events, err := NewStore(pool).ListEvents(ctx, run.ID, 1)
	if err != nil || len(events) != count+1 {
		t.Fatalf("list = %d %v", len(events), err)
	}
	for i, event := range events {
		if event.Sequence != int64(i+2) {
			t.Fatalf("sequence %d at %d", event.Sequence, i)
		}
	}
	event.Type = "run.failed"
	if _, err := pool.Exec(ctx, `UPDATE runs SET status='failed' WHERE id=$1`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendEvent(ctx, event); !errors.Is(err, ErrConflict) {
		t.Fatalf("unfinalized terminal event: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE runs SET finalized_at=now() WHERE id=$1`, run.ID); err != nil {
		t.Fatal(err)
	}
	final, err := store.AppendEvent(ctx, event)
	if err != nil || final.Sequence != count+3 {
		t.Fatalf("terminal = %#v %v", final, err)
	}
}

func TestMigrationBackfillsExistingEvents(t *testing.T) {
	pool, schema := testsupport.NewPostgresSchema(t, os.Getenv("TEST_DATABASE_URL"))
	ctx := context.Background()
	initial, err := migrations.Files.ReadFile("00001_initial.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(initial), pgx.QueryExecModeSimpleProtocol); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE TABLE schema_migrations(version text PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now()); INSERT INTO schema_migrations(version) VALUES('00001_initial.sql')`, pgx.QueryExecModeSimpleProtocol); err != nil {
		t.Fatal(err)
	}
	run := seedRun(t, pool)
	if _, err := pool.Exec(ctx, `INSERT INTO runs(id,conversation_id,trigger_message_id,status) VALUES($1,$2,$3,'queued')`, run.ID, run.ConversationID, run.TriggerMessageID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO run_events(run_id,sequence,type,payload,occurred_at) VALUES($1,41,'assistant.delta','{}',now())`, run.ID); err != nil {
		t.Fatal(err)
	}
	if err := postgres.Migrate(ctx, pool, schema); err != nil {
		t.Fatal(err)
	}
	event, err := NewStore(pool).AppendEvent(ctx, Event{RunID: run.ID, Type: "assistant.delta", Payload: json.RawMessage(`{}`), OccurredAt: time.Now()})
	if err != nil || event.Sequence != 42 {
		t.Fatalf("upgrade append = %#v %v", event, err)
	}
}
