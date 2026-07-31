//go:build integration

package projects

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"harness-forge.local/control-plane/internal/objectstore"
	"harness-forge.local/control-plane/internal/postgres"
	"harness-forge.local/control-plane/internal/profiles"
	"harness-forge.local/control-plane/internal/testsupport"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestProjectStoreCRUDListHidesDeletedAndInputRollback(t *testing.T) {
	pool := integrationPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	created, err := store.CreateProject(ctx, Project{ID: uuid.New(), Name: "one", ProfileID: "geo-analysis", ProfileVersion: "1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProject(ctx, Project{ID: uuid.New(), Name: "one", ProfileID: "geo-analysis", ProfileVersion: "1"}); err != nil {
		t.Fatalf("same name: %v", err)
	}
	read, err := store.ReadProject(ctx, created.ID)
	if err != nil || read.Name != "one" {
		t.Fatalf("ReadProject() = %#v, %v", read, err)
	}
	renamed, err := store.RenameProject(ctx, created.ID, "renamed")
	if err != nil || renamed.Name != "renamed" {
		t.Fatalf("RenameProject() = %#v, %v", renamed, err)
	}
	input, err := store.SaveInput(ctx, InputFile{ID: uuid.New(), ProjectID: created.ID, DisplayName: "a.csv", MediaType: "text/csv", SizeBytes: 1, SHA256Digest: strings.Repeat("a", 64), ObjectKey: "key"})
	if err != nil || input.CreatedAt.IsZero() {
		t.Fatalf("SaveInput() = %#v, %v", input, err)
	}
	inputs, err := store.ListInputs(ctx, created.ID)
	if err != nil || len(inputs) != 1 {
		t.Fatalf("ListInputs() = %#v, %v", inputs, err)
	}
	if err := store.DeleteProject(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteProject(ctx, created.ID); err != nil {
		t.Fatalf("idempotent DeleteProject() = %v", err)
	}
	if _, err := store.SaveInput(ctx, InputFile{ID: uuid.New(), ProjectID: created.ID, DisplayName: "late.csv", MediaType: "text/csv", ObjectKey: "late"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("late SaveInput() error = %v", err)
	}
	list, err := store.ListProjects(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListProjects() = %#v, %v", list, err)
	}
}

func TestProjectDeleteProtectsPendingAndUnfinalizedRuns(t *testing.T) {
	statuses := []struct {
		name, status string
		finalized    bool
	}{
		{name: "queued", status: "queued", finalized: false},
		{name: "running", status: "running", finalized: false},
		{name: "unfinalized failed", status: "failed", finalized: false},
	}
	for _, tt := range statuses {
		t.Run(tt.name, func(t *testing.T) {
			pool := integrationPool(t)
			store := NewStore(pool)
			ctx := context.Background()
			project, _ := store.CreateProject(ctx, Project{ID: uuid.New(), Name: "project", ProfileID: "geo-analysis", ProfileVersion: "1"})
			insertRun(t, pool, project.ID, tt.status, tt.finalized)
			if err := store.DeleteProject(ctx, project.ID); !errors.Is(err, ErrConflict) {
				t.Fatalf("DeleteProject() error = %v", err)
			}
		})
	}

	pool := integrationPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	project, _ := store.CreateProject(ctx, Project{ID: uuid.New(), Name: "safe", ProfileID: "geo-analysis", ProfileVersion: "1"})
	insertRun(t, pool, project.ID, "succeeded", true)
	if err := store.DeleteProject(ctx, project.ID); err != nil {
		t.Fatalf("DeleteProject(finalized) = %v", err)
	}
}

func TestUploadDeleteRace(t *testing.T) {
	t.Run("delete commits before upload metadata", func(t *testing.T) {
		pool := integrationPool(t)
		store := NewStore(pool)
		objects := newBarrierObjects()
		service := NewService(store, integrationResolver(t), objects)
		ctx := context.Background()
		project, err := service.CreateProject(ctx, "project", "geo-analysis")
		if err != nil {
			t.Fatal(err)
		}
		objects.blockPut = true
		result := make(chan error, 1)
		go func() {
			_, err := service.UploadInput(ctx, project.ID, "data.csv", "text/csv", strings.NewReader("a,b"))
			result <- err
		}()
		<-objects.putStarted
		if err := service.DeleteProject(ctx, project.ID); err != nil {
			t.Fatal(err)
		}
		close(objects.releasePut)
		if err := <-result; !errors.Is(err, ErrConflict) {
			t.Fatalf("UploadInput() error = %v", err)
		}
		if objects.deleted != objects.putKey {
			t.Fatalf("Delete key = %q, Put key = %q", objects.deleted, objects.putKey)
		}
		var count int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM input_files WHERE project_id=$1", project.ID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("late inputs = %d, %v", count, err)
		}
	})

	t.Run("upload commits before delete sees complete input", func(t *testing.T) {
		pool := integrationPool(t)
		store := NewStore(pool)
		objects := newBarrierObjects()
		service := NewService(store, integrationResolver(t), objects)
		ctx := context.Background()
		project, err := service.CreateProject(ctx, "project", "geo-analysis")
		if err != nil {
			t.Fatal(err)
		}
		input, err := service.UploadInput(ctx, project.ID, "data.csv", "text/csv", strings.NewReader("a,b"))
		if err != nil {
			t.Fatal(err)
		}
		if err := service.DeleteProject(ctx, project.ID); err != nil {
			t.Fatal(err)
		}
		var key string
		if err := pool.QueryRow(ctx, "SELECT object_key FROM input_files WHERE project_id=$1", project.ID).Scan(&key); err != nil {
			t.Fatal(err)
		}
		if key != input.ObjectKey || key != objects.putKey {
			t.Fatalf("stored key = %q, input=%q, put=%q", key, input.ObjectKey, objects.putKey)
		}
	})
}

func integrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, schema := testsupport.NewPostgresSchema(t, os.Getenv("TEST_DATABASE_URL"))
	if err := postgres.Migrate(context.Background(), pool, schema); err != nil {
		t.Fatal(err)
	}
	return pool
}

func integrationResolver(t *testing.T) *profiles.Resolver {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "geo-analysis", "workspace-template")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	yaml := "id: geo-analysis\nversion: '1'\ndisplay_name: Geo Analysis\nsystem_prompt: system-prompt.md\nworkspace_template: workspace-template\ntools:\n  allowed: [Read]\n  permission_mode: acceptEdits\nagent:\n  max_turns: 20\n  max_budget_usd: 5\ninputs:\n  accepted_media_types: [text/csv, application/geo+json]\nartifacts:\n  manifest_schema_version: 1\n  allowed_types: [html, markdown, image, data]\n  max_file_bytes: 10485760\n  max_total_bytes: 52428800\n"
	for path, content := range map[string]string{filepath.Join(root, "geo-analysis", "profile.yaml"): yaml, filepath.Join(root, "geo-analysis", "system-prompt.md"): "prompt", filepath.Join(dir, "README.md"): "template"} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r, err := profiles.NewResolver(root)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func insertRun(t *testing.T, pool *pgxpool.Pool, projectID uuid.UUID, status string, finalized bool) {
	t.Helper()
	ctx := context.Background()
	conversationID, messageID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, "INSERT INTO conversations(id,project_id,title) VALUES($1,$2,'test')", conversationID, projectID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO messages(id,conversation_id,role,content) VALUES($1,$2,'user','test')", messageID, conversationID); err != nil {
		t.Fatal(err)
	}
	var finalizedAt any
	if finalized {
		finalizedAt = time.Now()
	}
	if _, err := pool.Exec(ctx, "INSERT INTO runs(id,conversation_id,trigger_message_id,status,finalized_at) VALUES($1,$2,$3,$4,$5)", uuid.New(), conversationID, messageID, status, finalizedAt); err != nil {
		t.Fatal(err)
	}
}

type barrierObjects struct {
	blockPut        bool
	putStarted      chan struct{}
	releasePut      chan struct{}
	putKey, deleted string
}

func newBarrierObjects() *barrierObjects {
	return &barrierObjects{putStarted: make(chan struct{}), releasePut: make(chan struct{})}
}
func (s *barrierObjects) Put(_ context.Context, key string, reader io.Reader, _ objectstore.PutOptions) error {
	s.putKey = key
	if s.blockPut {
		close(s.putStarted)
		<-s.releasePut
	}
	_, err := io.Copy(io.Discard, reader)
	return err
}
func (s *barrierObjects) Open(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(nil)), nil
}
func (s *barrierObjects) Delete(_ context.Context, key string) error { s.deleted = key; return nil }
func (s *barrierObjects) DeletePrefix(context.Context, string) error { return nil }
func (s *barrierObjects) Stat(context.Context, string) (objectstore.ObjectInfo, error) {
	return objectstore.ObjectInfo{}, nil
}
