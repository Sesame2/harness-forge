//go:build integration

package runs

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"harness-forge.local/control-plane/internal/artifacts"
	"harness-forge.local/control-plane/internal/objectstore"
	"harness-forge.local/control-plane/internal/profiles"
	"harness-forge.local/control-plane/internal/sandbox"
	"harness-forge.local/control-plane/internal/workspaces"
)

func TestCoordinatorFakeSuccessAndCurrentSessionForQueuedFollowup(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	s := NewStore(pool)
	objects := objectstore.NewMemory()
	root := t.TempDir()
	resolver, err := profiles.NewResolver("../../../../profiles")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := sandbox.NewFakeProvider("../../../../tests/fixtures/fake-runtime")
	if err != nil {
		t.Fatal(err)
	}
	c := NewCoordinator(s, resolver, workspaces.NewMaterializer(root, objects), sandbox.Binding{ID: sandbox.Fake, Provider: provider}, artifacts.NewPublisher(pool, objects))
	first := enqueue(t, pool)
	second := enqueue(t, pool)
	if _, err := pool.Exec(ctx, `UPDATE messages SET conversation_id=$2 WHERE id=$1`, second.TriggerMessageID, first.ConversationID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE runs SET conversation_id=$2 WHERE id=$1`, second.ID, first.ConversationID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{first.ID.String(), second.ID.String()} {
		t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, id, "inputs"), 0770) })
	}
	for i := 0; i < 2; i++ {
		run, err := s.ClaimNext(ctx)
		if err != nil || run == nil {
			t.Fatalf("claim %v %v", run, err)
		}
		if err := c.Execute(ctx, *run); err != nil {
			t.Fatal(err)
		}
	}
	a, _ := s.Read(ctx, first.ID)
	b, _ := s.Read(ctx, second.ID)
	if a.Status != Succeeded || b.Status != Succeeded || a.FinalizedAt == nil || b.FinalizedAt == nil {
		t.Fatalf("%#v %#v", a, b)
	}
	if a.SourceSDKSessionID != nil || b.SourceSDKSessionID == nil || *b.SourceSDKSessionID != *a.CandidateSDKSessionID {
		t.Fatalf("sources first=%v second=%v", a.SourceSDKSessionID, b.SourceSDKSessionID)
	}
	records, err := artifacts.NewStore(pool).ListByRun(ctx, first.ID)
	if err != nil || len(records) == 0 {
		t.Fatalf("artifacts=%v %v", records, err)
	}
	events, _ := s.ListEvents(ctx, second.ID, 0)
	if len(events) == 0 || events[len(events)-1].Type != "run.succeeded" {
		t.Fatal(events)
	}
	leases, err := provider.List(ctx)
	if err != nil || len(leases) != 0 {
		t.Fatalf("leases=%v %v", leases, err)
	}
}

type publicationFailureStore struct {
	objectstore.Store
	beforePut func()
}

func (s publicationFailureStore) Put(context.Context, string, io.Reader, objectstore.PutOptions) error {
	s.beforePut()
	return errors.New("upload failed")
}
func TestPublicationFailureAbortsWithoutProductsAndAnnouncesPhaseBeforeUpload(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	s := NewStore(pool)
	run := enqueue(t, pool)
	claimed, _ := s.ClaimNext(ctx)
	objects := objectstore.NewMemory()
	root := t.TempDir()
	m := workspaces.NewMaterializer(root, objects)
	t.Cleanup(func() { _ = os.Chmod(m.Paths(run.ID).Inputs, 0770) })
	resolver, err := profiles.NewResolver("../../../../profiles")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := sandbox.NewFakeProvider("../../../../tests/fixtures/fake-runtime")
	if err != nil {
		t.Fatal(err)
	}
	uploads := 0
	failing := publicationFailureStore{Store: objects, beforePut: func() {
		uploads++
		got, e := s.Read(ctx, run.ID)
		if e != nil || got.Phase == nil || *got.Phase != Publishing {
			t.Error("upload preceded publishing transaction", got, e)
		}
		events, _ := s.ListEvents(ctx, run.ID, 0)
		if len(events) == 0 || string(events[len(events)-1].Payload) != `{"phase": "publishing"}` {
			t.Error("publishing event not durable before upload", events)
		}
	}}
	c := NewCoordinator(s, resolver, m, sandbox.Binding{ID: sandbox.Fake, Provider: provider}, artifacts.NewPublisher(pool, failing))
	if err := c.Execute(ctx, *claimed); err == nil {
		t.Fatal("failed upload succeeded")
	}
	got, _ := s.Read(ctx, run.ID)
	if got.Status != Failed || got.FinalizedAt == nil || got.CandidateSDKSessionID != nil || uploads == 0 {
		t.Fatal(got, uploads)
	}
	data, _ := s.LoadContext(ctx, run.ID)
	if data.ActiveSession != nil {
		t.Fatal("failed publication promoted session")
	}
	artifacts, err := artifacts.NewStore(pool).ListByRun(ctx, run.ID)
	if err != nil || len(artifacts) != 0 {
		t.Fatal(artifacts, err)
	}
	leases, _ := provider.List(ctx)
	if len(leases) != 0 {
		t.Fatal("failed publication retained lease")
	}
}

func TestReconcileRejectsProviderSwitchIncludingFinalizedIntent(t *testing.T) {
	pool := integrationPool(t)
	s := NewStore(pool)
	r := enqueue(t, pool)
	ctx := context.Background()
	_, _ = pool.Exec(ctx, `UPDATE runs SET status='failed',sandbox_provider='docker',finalized_at=now() WHERE id=$1`, r.ID)
	reconcile := NewReconciler(s, sandbox.Binding{ID: sandbox.Fake}, workspaces.NewMaterializer(t.TempDir(), objectstore.NewMemory()))
	if err := reconcile.Reconcile(ctx, false); err == nil {
		t.Fatal("provider switch not rejected before access")
	}
}
