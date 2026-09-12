//go:build integration

package httpapi

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"harness-forge.local/control-plane/internal/conversations"
	"harness-forge.local/control-plane/internal/postgres"
	"harness-forge.local/control-plane/internal/projects"
	"harness-forge.local/control-plane/internal/runs"
	"harness-forge.local/control-plane/internal/testsupport"
)

func TestSSEPollsPostgresCommitsWithoutBrokerNotification(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 14*time.Second)
	defer cancel()
	pool, schema := testsupport.NewPostgresSchema(t, os.Getenv("TEST_DATABASE_URL"))
	if err := postgres.Migrate(ctx, pool, schema); err != nil {
		t.Fatal(err)
	}
	project, err := projects.NewStore(pool).CreateProject(ctx, projects.Project{ID: uuid.New(), Name: "project", ProfileID: "geo-analysis", ProfileVersion: "1"})
	if err != nil {
		t.Fatal(err)
	}
	// The writer deliberately has no broker: commits are durable but cannot
	// wake the independently subscribed HTTP stream.
	store := runs.NewStore(pool)
	service := conversations.NewService(conversations.NewStore(pool), store)
	conversation, err := service.CreateConversation(ctx, project.ID, "conversation")
	if err != nil {
		t.Fatal(err)
	}
	submitted, err := service.SubmitMessage(ctx, conversation.ID, "build")
	if err != nil {
		t.Fatal(err)
	}
	id := submitted.Run.ID
	if _, err := store.ClaimNext(ctx); err != nil {
		t.Fatal(err)
	}
	appendDelta := func() {
		t.Helper()
		if _, err := store.AppendEvent(ctx, runs.Event{RunID: id, Type: "assistant.delta", Payload: []byte(`{"text":"durable"}`), OccurredAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	appendDelta()
	server := httptest.NewServer(NewRouter(Dependencies{Runs: store, Broker: runs.NewBroker()}))
	defer server.Close()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/v1/runs/"+id.String()+"/events/stream", nil)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	got := readSSEIDs(t, scanner, 1)
	appendDelta()
	got = append(got, readSSEIDs(t, scanner, 1)...)
	if _, err := store.RecordFailure(ctx, id, runs.Failed, []byte(`{"code":"test"}`)); err != nil {
		t.Fatal(err)
	}
	if err := store.AcknowledgeFinalized(ctx, id); err != nil {
		t.Fatal(err)
	}
	got = append(got, readSSEIDs(t, scanner, 1)...)
	if !reflect.DeepEqual(got, []int64{1, 2, 3}) {
		t.Fatalf("silent commits replayed or skipped durable IDs: %v", got)
	}
	finalized, err := store.Read(ctx, id)
	if err != nil || finalized.FinalizedAt == nil || finalized.Status != runs.Failed {
		t.Fatal("terminal event did not match durable finalization", finalized, err)
	}
}
