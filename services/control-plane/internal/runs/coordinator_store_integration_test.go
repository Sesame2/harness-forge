//go:build integration

package runs

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"harness-forge.local/control-plane/internal/artifacts"
	"harness-forge.local/control-plane/internal/contracts"
)

func TestRuntimeMessageAndEventAreAtomicAndIdempotent(t *testing.T) {
	pool := integrationPool(t)
	s := NewStore(pool)
	ctx := context.Background()
	r := enqueue(t, pool)
	_, _ = s.ClaimNext(ctx)
	e := contracts.RuntimeEvent{Version: "1", RunID: r.ID.String(), Sequence: 1, Type: "assistant.message", OccurredAt: time.Now().UTC(), Payload: contracts.AssistantMessagePayload{Text: "reply"}}
	for i := 0; i < 2; i++ {
		if err := s.RecordRuntimeEvent(ctx, r.ID, e); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM messages WHERE run_id=$1 AND content='reply'`, r.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("messages=%d %v", n, err)
	}
	events, _ := s.ListEvents(ctx, r.ID, 0)
	if len(events) != 1 {
		t.Fatal(events)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE messages ADD CONSTRAINT reject_reply CHECK(content<>'blocked')`); err != nil {
		t.Fatal(err)
	}
	e.Sequence = 2
	e.Payload = contracts.AssistantMessagePayload{Text: "blocked"}
	if err := s.RecordRuntimeEvent(ctx, r.ID, e); err == nil {
		t.Fatal("expected insert failure")
	}
	events, _ = s.ListEvents(ctx, r.ID, 0)
	if len(events) != 1 {
		t.Fatal("event committed without message")
	}
}

func TestEveryProductStatementAndCommitFailAtomically(t *testing.T) {
	for _, failure := range []struct{ name, sql string }{
		{"artifact insert", `ALTER TABLE artifacts ADD CONSTRAINT inject_failure CHECK(false)`},
		{"conversation update", `ALTER TABLE conversations ADD CONSTRAINT inject_failure CHECK(active_sdk_session_id IS NULL)`},
		{"run update", `ALTER TABLE runs ADD CONSTRAINT inject_failure CHECK(status<>'succeeded')`},
		{"commit", `CREATE FUNCTION reject_products() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.status='succeeded' THEN RAISE EXCEPTION 'injected commit failure'; END IF; RETURN NEW; END$$; CREATE CONSTRAINT TRIGGER reject_products AFTER UPDATE ON runs DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_products()`},
	} {
		t.Run(failure.name, func(t *testing.T) {
			pool := integrationPool(t)
			s := NewStore(pool)
			ctx := context.Background()
			r := enqueue(t, pool)
			_, _ = s.ClaimNext(ctx)
			data, err := s.LoadContext(ctx, r.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.SetPhase(ctx, r.ID, Agent); err != nil {
				t.Fatal(err)
			}
			if err := s.SetPhase(ctx, r.ID, Publishing); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, failure.sql); err != nil {
				t.Fatal(err)
			}
			records := []artifacts.Artifact{{ID: uuid.New(), RunID: r.ID, Title: "one", Type: "html", EntryPath: "index.html", ObjectPrefix: "one/", ManifestVersion: 1}, {ID: uuid.New(), RunID: r.ID, Title: "two", Type: "data", EntryPath: "data.json", ObjectPrefix: "two/", ManifestVersion: 1}}
			if err := s.CommitProducts(ctx, r.ID, data.ProjectID, "candidate", records); err == nil {
				t.Fatal("failure was not injected")
			}
			var count int
			var active *string
			_ = pool.QueryRow(ctx, `SELECT count(*) FROM artifacts`).Scan(&count)
			_ = pool.QueryRow(ctx, `SELECT active_sdk_session_id FROM conversations WHERE id=$1`, r.ConversationID).Scan(&active)
			got, _ := s.Read(ctx, r.ID)
			if count != 0 || active != nil || got.CandidateSDKSessionID != nil || got.Status != Running {
				t.Fatalf("partial products artifacts=%d active=%v run=%#v", count, active, got)
			}
		})
	}
}

func TestEveryFinalAcknowledgementStatementAndCommitFailAtomically(t *testing.T) {
	for _, failure := range []struct{ name, sql string }{
		{"timestamp", `ALTER TABLE runs ADD CONSTRAINT inject_failure CHECK(finalized_at IS NULL)`},
		{"sequence allocation", `CREATE FUNCTION reject_sequence() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.next_event_sequence<>OLD.next_event_sequence THEN RAISE EXCEPTION 'injected sequence failure'; END IF; RETURN NEW; END$$;CREATE TRIGGER reject_sequence BEFORE UPDATE ON runs FOR EACH ROW EXECUTE FUNCTION reject_sequence()`},
		{"artifact event", `ALTER TABLE run_events ADD CONSTRAINT inject_failure CHECK(type<>'artifact.published')`},
		{"terminal event", `ALTER TABLE run_events ADD CONSTRAINT inject_failure CHECK(type<>'run.succeeded')`},
		{"commit", `CREATE FUNCTION reject_final_ack() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.finalized_at IS NOT NULL THEN RAISE EXCEPTION 'injected commit failure'; END IF; RETURN NEW; END$$;CREATE CONSTRAINT TRIGGER reject_final_ack AFTER UPDATE ON runs DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_final_ack()`},
	} {
		t.Run(failure.name, func(t *testing.T) {
			pool := integrationPool(t)
			s := NewStore(pool)
			ctx := context.Background()
			r := enqueue(t, pool)
			_, _ = s.ClaimNext(ctx)
			data, err := s.LoadContext(ctx, r.ID)
			if err != nil {
				t.Fatal(err)
			}
			_ = s.SetPhase(ctx, r.ID, Agent)
			_ = s.SetPhase(ctx, r.ID, Publishing)
			a := artifacts.Artifact{ID: uuid.New(), RunID: r.ID, Title: "one", Type: "html", EntryPath: "index.html", ObjectPrefix: "one/", ManifestVersion: 1}
			if err := s.CommitProducts(ctx, r.ID, data.ProjectID, "candidate", []artifacts.Artifact{a}); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, failure.sql); err != nil {
				t.Fatal(err)
			}
			if err := s.AcknowledgeFinalized(ctx, r.ID); err == nil {
				t.Fatal("failure was not injected")
			}
			got, _ := s.Read(ctx, r.ID)
			events, _ := s.ListEvents(ctx, r.ID, 0)
			if got.FinalizedAt != nil || got.Status != Succeeded || len(events) != 2 {
				t.Fatalf("partial final ack %#v %s", got, fmt.Sprint(events))
			}
		})
	}
}

func TestProductsAndFinalAcknowledgementTransactions(t *testing.T) {
	pool := integrationPool(t)
	s := NewStore(pool)
	ctx := context.Background()
	r := enqueue(t, pool)
	_, _ = s.ClaimNext(ctx)
	data, err := s.LoadContext(ctx, r.ID)
	if err != nil || data.Prompt != "build" {
		t.Fatalf("context %#v %v", data, err)
	}
	if err := s.SetPhase(ctx, r.ID, Agent); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPhase(ctx, r.ID, Publishing); err != nil {
		t.Fatal(err)
	}
	a := artifacts.Artifact{ID: uuid.New(), RunID: r.ID, Title: "report", Type: "html", EntryPath: "index.html", ObjectPrefix: "prefix/", ManifestVersion: 1}
	if _, err := pool.Exec(ctx, `ALTER TABLE runs ADD CONSTRAINT reject_success CHECK(status<>'succeeded')`); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitProducts(ctx, r.ID, data.ProjectID, "candidate", []artifacts.Artifact{a}); err == nil {
		t.Fatal("expected failed product transaction")
	}
	var count int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM artifacts`).Scan(&count)
	if count != 0 {
		t.Fatal("partial artifact commit")
	}
	var active *string
	_ = pool.QueryRow(ctx, `SELECT active_sdk_session_id FROM conversations WHERE id=$1`, r.ConversationID).Scan(&active)
	if active != nil {
		t.Fatal("partial promotion")
	}
	_, _ = pool.Exec(ctx, `ALTER TABLE runs DROP CONSTRAINT reject_success`)
	if err := s.CommitProducts(ctx, r.ID, data.ProjectID, "candidate", []artifacts.Artifact{a}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Read(ctx, r.ID)
	if got.Status != Succeeded || got.FinalizedAt != nil {
		t.Fatal(got)
	}
	_, _ = pool.Exec(ctx, `ALTER TABLE run_events ADD CONSTRAINT reject_terminal CHECK(type<>'run.succeeded')`)
	if err := s.AcknowledgeFinalized(ctx, r.ID); err == nil {
		t.Fatal("expected final event failure")
	}
	got, _ = s.Read(ctx, r.ID)
	if got.FinalizedAt != nil {
		t.Fatal("timestamp committed without terminal event")
	}
	events, _ := s.ListEvents(ctx, r.ID, 0)
	for _, e := range events {
		if e.Type == "artifact.published" {
			t.Fatal("partial final events")
		}
	}
	_, _ = pool.Exec(ctx, `ALTER TABLE run_events DROP CONSTRAINT reject_terminal`)
	for i := 0; i < 2; i++ {
		if err := s.AcknowledgeFinalized(ctx, r.ID); err != nil {
			t.Fatal(err)
		}
	}
	events, _ = s.ListEvents(ctx, r.ID, 0)
	if len(events) != 4 {
		t.Fatalf("events=%#v", events)
	}
}

func TestTerminalFailurePreservesCancellationAndDefersEvent(t *testing.T) {
	pool := integrationPool(t)
	s := NewStore(pool)
	ctx := context.Background()
	r := enqueue(t, pool)
	_, _ = s.ClaimNext(ctx)
	_, err := s.RecordFailure(ctx, r.ID, Cancelled, json.RawMessage(`{"code":"cancelled"}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.RecordFailure(ctx, r.ID, Failed, json.RawMessage(`{"code":"stream"}`))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.Read(ctx, r.ID)
	if got.Status != Cancelled || got.FinalizedAt != nil {
		t.Fatal(got)
	}
	events, _ := s.ListEvents(ctx, r.ID, 0)
	if len(events) != 0 {
		t.Fatal("early terminal event")
	}
	if err := s.AcknowledgeFinalized(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	events, _ = s.ListEvents(ctx, r.ID, 0)
	if len(events) != 1 || events[0].Type != "run.cancelled" {
		t.Fatal(events)
	}
}
