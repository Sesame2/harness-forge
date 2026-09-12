//go:build integration

package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"harness-forge.local/control-plane/internal/conversations"
	"harness-forge.local/control-plane/internal/postgres"
	"harness-forge.local/control-plane/internal/projects"
	"harness-forge.local/control-plane/internal/runs"
	"harness-forge.local/control-plane/internal/testsupport"

	"github.com/google/uuid"
)

func TestConversationRepeatDeleteAfterProjectDeletionHTTP(t *testing.T) {
	ctx := context.Background()
	pool, schema := testsupport.NewPostgresSchema(t, os.Getenv("TEST_DATABASE_URL"))
	if err := postgres.Migrate(ctx, pool, schema); err != nil {
		t.Fatal(err)
	}
	projectStore := projects.NewStore(pool)
	project, err := projectStore.CreateProject(ctx, projects.Project{ID: uuid.New(), Name: "project", ProfileID: "geo-analysis", ProfileVersion: "1"})
	if err != nil {
		t.Fatal(err)
	}
	service := conversations.NewService(conversations.NewStore(pool), runs.NewStore(pool))
	conversation, err := service.CreateConversation(ctx, project.ID, "conversation")
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(Dependencies{Projects: projects.NewService(projectStore, nil, nil), Conversations: service})
	deletePath := func(path string, want int) {
		t.Helper()
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, path, nil))
		if response.Code != want {
			t.Fatalf("DELETE %s = %d, want %d; body=%s", path, response.Code, want, response.Body.String())
		}
		if want == http.StatusNoContent && response.Body.Len() != 0 {
			t.Fatal("204 response has a body")
		}
	}
	path := "/api/v1/conversations/" + conversation.ID.String()
	deletePath(path, http.StatusNoContent)
	var originalDeletedAt time.Time
	if err := pool.QueryRow(ctx, `SELECT deleted_at FROM conversations WHERE id=$1`, conversation.ID).Scan(&originalDeletedAt); err != nil {
		t.Fatal(err)
	}
	deletePath("/api/v1/projects/"+project.ID.String(), http.StatusNoContent)
	for i := 0; i < 2; i++ {
		deletePath(path, http.StatusNoContent)
	}
	deletePath("/api/v1/conversations/"+uuid.NewString(), http.StatusNotFound)
	var repeatedDeletedAt time.Time
	if err := pool.QueryRow(ctx, `SELECT deleted_at FROM conversations WHERE id=$1`, conversation.ID).Scan(&repeatedDeletedAt); err != nil {
		t.Fatal(err)
	}
	if !repeatedDeletedAt.Equal(originalDeletedAt) {
		t.Fatal("repeat delete mutated deleted_at")
	}
}
