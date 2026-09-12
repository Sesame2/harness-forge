//go:build integration

package artifacts_test

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"harness-forge.local/control-plane/internal/artifacthttp"
	"harness-forge.local/control-plane/internal/artifacts"
	"harness-forge.local/control-plane/internal/httpapi"
	"harness-forge.local/control-plane/internal/objectstore"
	"harness-forge.local/control-plane/internal/postgres"
	"harness-forge.local/control-plane/internal/profiles"
	"harness-forge.local/control-plane/internal/testsupport"
)

func publicationDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, schema := testsupport.NewPostgresSchema(t, os.Getenv("TEST_DATABASE_URL"))
	if err := postgres.Migrate(context.Background(), pool, schema); err != nil {
		t.Fatal(err)
	}
	return pool
}
func publicationRun(t *testing.T, pool *pgxpool.Pool) (uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	p, c, m, r := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, q := range []struct {
		sql  string
		args []any
	}{{`INSERT INTO projects(id,name,profile_id,profile_version)VALUES($1,'P','test','1')`, []any{p}}, {`INSERT INTO conversations(id,project_id,title)VALUES($1,$2,'C')`, []any{c, p}}, {`INSERT INTO messages(id,conversation_id,role,content)VALUES($1,$2,'user','Hi')`, []any{m, c}}, {`INSERT INTO runs(id,conversation_id,trigger_message_id,status,phase)VALUES($1,$2,$3,'running','publishing')`, []any{r, c, m}}} {
		if _, err := pool.Exec(context.Background(), q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	return p, c, r
}
func publicationOutputs(t *testing.T) (string, profiles.Snapshot) {
	t.Helper()
	root := t.TempDir()
	for name, data := range map[string]string{"artifact-manifest.json": `{"schema_version":1,"artifacts":[{"name":"report","title":"Report","type":"html","entry":"index.html","primary":true}]}`, "index.html": "<script src=\"chart.js\"></script>", "chart.js": "window.ok=1;"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root, profiles.Snapshot{Artifacts: profiles.ArtifactPolicy{ManifestSchemaVersion: 1, AllowedTypes: []string{"html"}, MaxFileBytes: 10485760, MaxTotalBytes: 52428800}}
}
func maintenanceAvailable(t *testing.T, pool *pgxpool.Pool) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	var available bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended('harness-forge:artifact-maintenance',0))`).Scan(&available); err != nil {
		t.Fatal(err)
	}
	if available {
		if _, err := conn.Exec(ctx, `SELECT pg_advisory_unlock(hashtextextended('harness-forge:artifact-maintenance',0))`); err != nil {
			t.Fatal(err)
		}
	}
	return available
}

func TestPublishRealSessionLockSpansUploadsAndCallerMetadataCommit(t *testing.T) {
	pool := publicationDB(t)
	project, _, run := publicationRun(t, pool)
	root, profile := publicationOutputs(t)
	objects := &lockCheckingObjects{Memory: objectstore.NewMemory(), before: func() {
		if maintenanceAvailable(t, pool) {
			t.Fatal("upload occurred without session lock")
		}
	}}
	prepared, err := artifacts.NewPublisher(pool, objects).Prepare(context.Background(), project, run, root, profile)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Release()
	if maintenanceAvailable(t, pool) {
		t.Fatal("Prepare released lock before caller metadata transaction")
	}
	store := artifacts.NewStore(pool)
	record := prepared.Records[0]
	gateway := artifacthttp.NewServer(store, objects, "http://localhost:5173")
	checkGateway := func(want int) {
		w := httptest.NewRecorder()
		gateway.ServeHTTP(w, httptest.NewRequest("GET", "/artifacts/"+record.ID.String()+"/index.html", nil))
		if w.Code != want {
			t.Fatalf("gateway %d want %d", w.Code, want)
		}
	}
	checkGateway(404)
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if err := store.InsertTx(context.Background(), tx, prepared.Records); err != nil {
		t.Fatal(err)
	}
	checkGateway(404)
	listed, err := store.ListByRun(context.Background(), run)
	if err != nil || len(listed) != 0 {
		t.Fatalf("uncommitted metadata visible: %#v %v", listed, err)
	}
	if maintenanceAvailable(t, pool) {
		t.Fatal("lock lost during metadata transaction")
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if maintenanceAvailable(t, pool) {
		t.Fatal("lock released without caller Release")
	}
	checkGateway(200)
	if err := prepared.Release(); err != nil {
		t.Fatal(err)
	}
	if err := prepared.Release(); err != nil {
		t.Fatal(err)
	}
	if !maintenanceAvailable(t, pool) {
		t.Fatal("maintenance lock leaked after release")
	}
	var status string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM runs WHERE id=$1`, run).Scan(&status); err != nil || status != "running" {
		t.Fatalf("publisher changed run: %s %v", status, err)
	}
	if pool.Stat().AcquiredConns() != 0 {
		t.Fatalf("leaked acquired connection: %d", pool.Stat().AcquiredConns())
	}
}

type lockCheckingObjects struct {
	*objectstore.Memory
	before func()
}

func (s *lockCheckingObjects) Put(ctx context.Context, key string, r io.Reader, opts objectstore.PutOptions) error {
	s.before()
	return s.Memory.Put(ctx, key, r, opts)
}

func TestPublishDBCommitFailureLeavesInvisibleOrphan(t *testing.T) {
	pool := publicationDB(t)
	project, _, run := publicationRun(t, pool)
	root, profile := publicationOutputs(t)
	objects := objectstore.NewMemory()
	ctx := context.Background()
	prepared, err := artifacts.NewPublisher(pool, objects).Prepare(ctx, project, run, root, profile)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Release()
	if _, err := pool.Exec(ctx, `ALTER TABLE artifacts ALTER CONSTRAINT artifacts_run_id_fkey DEFERRABLE INITIALLY DEFERRED`); err != nil {
		t.Fatal(err)
	}
	store := artifacts.NewStore(pool)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	invalid := append([]artifacts.Artifact(nil), prepared.Records...)
	invalid[0].RunID = uuid.New()
	if err := store.InsertTx(ctx, tx, invalid); err != nil {
		t.Fatalf("expected deferred commit failure, insert: %v", err)
	}
	if err := tx.Commit(ctx); err == nil {
		t.Fatal("expected DB commit failure")
	}
	if maintenanceAvailable(t, pool) {
		t.Fatal("failed commit prematurely released lock")
	}
	if err := prepared.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := objects.Stat(ctx, prepared.Records[0].ObjectPrefix+"index.html"); err != nil {
		t.Fatal("failed DB commit should leave scanner-owned orphan", err)
	}
	if _, err := store.Read(ctx, prepared.Records[0].ID); !errors.Is(err, artifacts.ErrNotFound) {
		t.Fatalf("orphan metadata visible: %v", err)
	}
	w := httptest.NewRecorder()
	artifacthttp.NewServer(store, objects, "http://localhost:5173").ServeHTTP(w, httptest.NewRequest("GET", "/artifacts/"+prepared.Records[0].ID.String()+"/index.html", nil))
	if w.Code != 404 {
		t.Fatal("orphan served")
	}
	if !maintenanceAvailable(t, pool) || pool.Stat().AcquiredConns() != 0 {
		t.Fatal("failed commit leaked lock/connection")
	}
}

func TestPublicationLockCancellationAndFailedUnlockDoNotLeak(t *testing.T) {
	pool := publicationDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	release, err := artifacts.AcquirePublicationLock(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	cancel()
	blocked, cancelBlocked := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelBlocked()
	if other, err := artifacts.AcquirePublicationLock(blocked, pool); err == nil {
		other()
		t.Fatal("second session bypassed held lock")
	}
	if err := release(); err != nil {
		t.Fatalf("cancelled caller prevented cleanup: %v", err)
	}
	if !maintenanceAvailable(t, pool) {
		t.Fatal("cancelled acquire left lock")
	}
	release, err = artifacts.AcquirePublicationLock(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	var killed bool
	if err := pool.QueryRow(context.Background(), `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND query='SELECT pg_advisory_lock(hashtextextended(''harness-forge:artifact-maintenance'',0))'`).Scan(&killed); err != nil || !killed {
		t.Fatalf("terminate lock session %v %v", killed, err)
	}
	if err := release(); err == nil {
		t.Fatal("expected unlock failure")
	}
	if !maintenanceAvailable(t, pool) || pool.Stat().AcquiredConns() != 0 {
		t.Fatal("failed unlock returned locked/acquired connection")
	}
}

func TestArtifactStoreListsCommittedOwnedRowsAndHonorsTombstones(t *testing.T) {
	pool := publicationDB(t)
	project, conversation, run := publicationRun(t, pool)
	otherProject, _, otherRun := publicationRun(t, pool)
	store := artifacts.NewStore(pool)
	ctx := context.Background()
	if rows, err := store.ListByRun(ctx, run); err != nil || len(rows) != 0 {
		t.Fatalf("empty: %v %v", rows, err)
	}
	if _, err := store.ListByRun(ctx, uuid.New()); !errors.Is(err, artifacts.ErrNotFound) {
		t.Fatal(err)
	}
	for _, pair := range [][2]uuid.UUID{{project, run}, {otherProject, otherRun}} {
		id := uuid.New()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if err := store.InsertTx(ctx, tx, []artifacts.Artifact{{ID: id, RunID: pair[1], Title: "Report", Type: "html", EntryPath: "index.html", ObjectPrefix: "projects/" + pair[0].String() + "/artifacts/" + id.String() + "/", IsPrimary: true, ManifestVersion: 1}}); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := store.ListByRun(ctx, run)
	if err != nil || len(rows) != 1 || rows[0].RunID != run || !rows[0].IsPrimary || rows[0].CreatedAt.IsZero() {
		t.Fatalf("rows %#v %v", rows, err)
	}
	w := httptest.NewRecorder()
	router := httpapi.NewRouter(httpapi.Dependencies{Artifacts: store, ArtifactPublicOrigin: "https://artifacts.example"})
	r := httptest.NewRequest("GET", "https://evil.example/api/v1/runs/"+run.String()+"/artifacts", nil)
	r.Header.Set("X-Forwarded-Host", "evil.example")
	router.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"is_primary":true`) || strings.Contains(w.Body.String(), "evil.example") || strings.Contains(w.Body.String(), "object_prefix") {
		t.Fatalf("list %d %s", w.Code, w.Body.String())
	}
	for _, q := range []struct {
		hide, restore string
		id            uuid.UUID
	}{{`UPDATE conversations SET deleted_at=now() WHERE id=$1`, `UPDATE conversations SET deleted_at=NULL WHERE id=$1`, conversation}, {`UPDATE projects SET deleted_at=now() WHERE id=$1`, `UPDATE projects SET deleted_at=NULL WHERE id=$1`, project}} {
		if _, err := pool.Exec(ctx, q.hide, q.id); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Read(ctx, rows[0].ID); !errors.Is(err, artifacts.ErrNotFound) {
			t.Fatalf("deleted owner readable: %v", err)
		}
		if _, err := store.ListByRun(ctx, run); !errors.Is(err, artifacts.ErrNotFound) {
			t.Fatalf("deleted owner listed: %v", err)
		}
		if _, err := pool.Exec(ctx, q.restore, q.id); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPublishRealPreparationErrorsReleaseSessionAndCleanPartialObjects(t *testing.T) {
	pool := publicationDB(t)
	project, _, run := publicationRun(t, pool)
	ctx := context.Background()
	for _, kind := range []string{"manifest", "upload", "cancelled", "identity"} {
		t.Run(kind, func(t *testing.T) {
			root, profile := publicationOutputs(t)
			objects := &failedUploadObjects{Memory: objectstore.NewMemory(), fail: kind == "upload"}
			localCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			projectID := project
			switch kind {
			case "manifest":
				if err := os.Remove(filepath.Join(root, "artifact-manifest.json")); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				cancel()
			case "identity":
				projectID = uuid.Nil
			}
			prepared, err := artifacts.NewPublisher(pool, objects).Prepare(localCtx, projectID, run, root, profile)
			if err == nil || prepared != nil {
				t.Fatalf("%s unexpectedly prepared: %#v %v", kind, prepared, err)
			}
			for _, key := range objects.keys {
				if _, err := objects.Stat(ctx, key); err == nil {
					t.Fatalf("partial object remains: %s", key)
				}
			}
			if !maintenanceAvailable(t, pool) || pool.Stat().AcquiredConns() != 0 {
				t.Fatalf("%s leaked lock/session", kind)
			}
		})
	}
}

type failedUploadObjects struct {
	*objectstore.Memory
	keys []string
	fail bool
}

func (s *failedUploadObjects) Put(ctx context.Context, key string, body io.Reader, options objectstore.PutOptions) error {
	s.keys = append(s.keys, key)
	if err := s.Memory.Put(ctx, key, body, options); err != nil {
		return err
	}
	if s.fail {
		return errors.New("partial upload failure")
	}
	return nil
}
