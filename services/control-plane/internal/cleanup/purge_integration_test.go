//go:build integration

package cleanup

import (
	"context"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"harness-forge.local/control-plane/internal/artifacthttp"
	"harness-forge.local/control-plane/internal/artifacts"
	"harness-forge.local/control-plane/internal/objectstore"
	"harness-forge.local/control-plane/internal/postgres"
	"harness-forge.local/control-plane/internal/profiles"
	"harness-forge.local/control-plane/internal/sandbox"
	"harness-forge.local/control-plane/internal/testsupport"
)

func TestOrphanScannerFindsInvisibleFailedPublicationAndDeletesIt(t *testing.T) {
	pool := cleanupDB(t)
	objects := cleanupMinIO(t)
	projectID, _, runID := cleanupRun(t, pool)
	root, profile := cleanupOutputs(t)
	prepared, err := artifacts.NewPublisher(pool, objects).Prepare(context.Background(), projectID, runID, root, profile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `ALTER TABLE artifacts ALTER CONSTRAINT artifacts_run_id_fkey DEFERRABLE INITIALLY DEFERRED`); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	invalid := append([]artifacts.Artifact(nil), prepared.Records...)
	invalid[0].RunID = uuid.New()
	if err := artifacts.NewStore(pool).InsertTx(context.Background(), tx, invalid); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); err == nil {
		t.Fatal("expected deferred metadata commit failure")
	}
	if err := prepared.Release(); err != nil {
		t.Fatal(err)
	}
	record := prepared.Records[0]
	w := httptest.NewRecorder()
	artifacthttp.NewServer(artifacts.NewStore(pool), objects, "http://localhost:5173").ServeHTTP(w, httptest.NewRequest("GET", "/artifacts/"+record.ID.String()+"/index.html", nil))
	if w.Code != 404 {
		t.Fatalf("orphan gateway status = %d", w.Code)
	}
	scanner := NewOrphanScanner(pool, objects)
	dry, err := scanner.Scan(context.Background(), false)
	if err != nil || len(dry.Orphans) != 1 || len(dry.Deleted) != 0 {
		t.Fatalf("dry scan = %#v, %v", dry, err)
	}
	if _, err := objects.Stat(context.Background(), record.ObjectPrefix+"index.html"); err != nil {
		t.Fatalf("dry scan removed orphan: %v", err)
	}
	applied, err := scanner.Scan(context.Background(), true)
	if err != nil || len(applied.Deleted) != 1 {
		t.Fatalf("apply scan = %#v, %v", applied, err)
	}
	if _, err := objects.Stat(context.Background(), record.ObjectPrefix+"index.html"); err == nil {
		t.Fatal("orphan object retained")
	}
}

func TestOrphanScannerWaitsForPublicationMetadataCommit(t *testing.T) {
	pool := cleanupDB(t)
	objects := cleanupMinIO(t)
	projectID, _, runID := cleanupRun(t, pool)
	root, profile := cleanupOutputs(t)
	prepared, err := artifacts.NewPublisher(pool, objects).Prepare(context.Background(), projectID, runID, root, profile)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan struct {
		summary OrphanSummary
		err     error
	}, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		summary, err := NewOrphanScanner(pool, objects).Scan(ctx, true)
		result <- struct {
			summary OrphanSummary
			err     error
		}{summary, err}
	}()
	select {
	case got := <-result:
		t.Fatalf("scanner entered during publication: %#v %v", got.summary, got.err)
	case <-time.After(100 * time.Millisecond):
	}
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := artifacts.NewStore(pool).InsertTx(context.Background(), tx, prepared.Records); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := prepared.Release(); err != nil {
		t.Fatal(err)
	}
	got := <-result
	if got.err != nil || len(got.summary.Orphans) != 0 {
		t.Fatalf("scanner treated committed publication as orphan: %#v %v", got.summary, got.err)
	}
	if _, err := objects.Stat(context.Background(), prepared.Records[0].ObjectPrefix+"index.html"); err != nil {
		t.Fatalf("scanner deleted committed artifact: %v", err)
	}
}

func TestPurgeHardDeletesConversationWithoutProjectInputs(t *testing.T) {
	pool := cleanupDB(t)
	projectID, conversationID, _ := cleanupRun(t, pool)
	inputID := uuid.New()
	inputKey := "projects/" + projectID.String() + "/inputs/" + inputID.String() + "/shared.txt"
	if _, err := pool.Exec(context.Background(), `INSERT INTO input_files(id,project_id,display_name,media_type,size_bytes,sha256_digest,object_key) VALUES($1,$2,'shared.txt','text/plain',1,'x',$3)`, inputID, projectID, inputKey); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE conversations SET deleted_at=now() WHERE id=$1`, conversationID); err != nil {
		t.Fatal(err)
	}
	objects := objectstore.NewMemory()
	if err := objects.Put(context.Background(), inputKey, strings.NewReader("x"), objectstore.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	workspaceRoot := t.TempDir()
	purger := NewPurger(pool, objects, sandbox.Binding{ID: sandbox.Fake, Provider: &recordingProvider{}}, workspaceRoot)
	summary, err := purger.Purge(context.Background(), true)
	if err != nil || len(summary.Purged) != 1 {
		t.Fatalf("purge = %#v, %v", summary, err)
	}
	var conversations int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM conversations WHERE id=$1`, conversationID).Scan(&conversations); err != nil || conversations != 0 {
		t.Fatalf("conversation count = %d, %v", conversations, err)
	}
	var projects, inputs int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM projects WHERE id=$1`, projectID).Scan(&projects); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM input_files WHERE project_id=$1`, projectID).Scan(&inputs); err != nil {
		t.Fatal(err)
	}
	if projects != 1 || inputs != 1 {
		t.Fatalf("conversation purge removed shared project data: projects=%d inputs=%d", projects, inputs)
	}
	if _, err := objects.Stat(context.Background(), inputKey); err != nil {
		t.Fatalf("conversation purge removed shared input object: %v", err)
	}
}

func TestPurgeHardDeletesProjectWithBidirectionalMessageRunReferences(t *testing.T) {
	pool := cleanupDB(t)
	projectID, conversationID, runID := cleanupRun(t, pool)
	inputID, artifactID, assistantID := uuid.New(), uuid.New(), uuid.New()
	inputKey := "projects/" + projectID.String() + "/inputs/" + inputID.String() + "/input.txt"
	artifactPrefix := "projects/" + projectID.String() + "/artifacts/" + artifactID.String() + "/"
	queries := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO input_files(id,project_id,display_name,media_type,size_bytes,sha256_digest,object_key) VALUES($1,$2,'input.txt','text/plain',1,'x',$3)`, []any{inputID, projectID, inputKey}},
		{`INSERT INTO messages(id,conversation_id,role,content,run_id,runtime_sequence) VALUES($1,$2,'assistant','done',$3,1)`, []any{assistantID, conversationID, runID}},
		{`INSERT INTO artifacts(id,run_id,title,type,entry_path,object_prefix,is_primary,manifest_version) VALUES($1,$2,'Report','html','index.html',$3,true,1)`, []any{artifactID, runID, artifactPrefix}},
		{`UPDATE projects SET deleted_at=now() WHERE id=$1`, []any{projectID}},
	}
	for _, item := range queries {
		if _, err := pool.Exec(context.Background(), item.query, item.args...); err != nil {
			t.Fatal(err)
		}
	}
	objects := objectstore.NewMemory()
	for key, value := range map[string]string{inputKey: "x", artifactPrefix + "index.html": "ok"} {
		if err := objects.Put(context.Background(), key, strings.NewReader(value), objectstore.PutOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	purger := NewPurger(pool, objects, sandbox.Binding{ID: sandbox.Fake, Provider: &recordingProvider{}}, t.TempDir())
	if summary, err := purger.Purge(context.Background(), true); err != nil || len(summary.Purged) != 1 {
		t.Fatalf("purge = %#v, %v", summary, err)
	}
	for table, id := range map[string]uuid.UUID{"projects": projectID, "conversations": conversationID, "runs": runID, "messages": assistantID, "artifacts": artifactID} {
		var count int
		if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM `+table+` WHERE id=$1`, id).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s count = %d, %v", table, count, err)
		}
	}
	if _, err := objects.Stat(context.Background(), inputKey); err == nil {
		t.Fatal("project input object retained")
	}
	if _, err := objects.Stat(context.Background(), artifactPrefix+"index.html"); err == nil {
		t.Fatal("project artifact object retained")
	}
}

func cleanupDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, schema := testsupport.NewPostgresSchema(t, os.Getenv("TEST_DATABASE_URL"))
	if err := postgres.Migrate(context.Background(), pool, schema); err != nil {
		t.Fatal(err)
	}
	return pool
}

func cleanupRun(t *testing.T, pool *pgxpool.Pool) (uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	projectID, conversationID, messageID, runID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	queries := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO projects(id,name,profile_id,profile_version) VALUES($1,'P','test','1')`, []any{projectID}},
		{`INSERT INTO conversations(id,project_id,title) VALUES($1,$2,'C')`, []any{conversationID, projectID}},
		{`INSERT INTO messages(id,conversation_id,role,content) VALUES($1,$2,'user','Hi')`, []any{messageID, conversationID}},
		{`INSERT INTO runs(id,conversation_id,trigger_message_id,status,finalized_at) VALUES($1,$2,$3,'succeeded',now())`, []any{runID, conversationID, messageID}},
	}
	for _, item := range queries {
		if _, err := pool.Exec(context.Background(), item.query, item.args...); err != nil {
			t.Fatal(err)
		}
	}
	return projectID, conversationID, runID
}

func cleanupOutputs(t *testing.T) (string, profiles.Snapshot) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "artifact-manifest.json"), []byte(`{"schema_version":1,"artifacts":[{"name":"report","title":"Report","type":"html","entry":"index.html","primary":true}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("ok"), 0600); err != nil {
		t.Fatal(err)
	}
	return root, profiles.Snapshot{Artifacts: profiles.ArtifactPolicy{ManifestSchemaVersion: 1, AllowedTypes: []string{"html"}, MaxFileBytes: 1024, MaxTotalBytes: 2048}}
}

func cleanupMinIO(t *testing.T) *objectstore.MinIO {
	t.Helper()
	endpoint := os.Getenv("TEST_MINIO_ENDPOINT")
	if endpoint == "" {
		endpoint = "http://localhost:9000"
	}
	access := os.Getenv("TEST_MINIO_ACCESS_KEY")
	if access == "" {
		access = "harness_forge"
	}
	secret := os.Getenv("TEST_MINIO_SECRET_KEY")
	if secret == "" {
		secret = "local-dev-only"
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	client, err := minio.New(parsed.Host, &minio.Options{Creds: credentials.NewStaticV4(access, secret, ""), Secure: parsed.Scheme == "https"})
	if err != nil {
		t.Fatal(err)
	}
	bucket := "hf-cleanup-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := client.MakeBucket(context.Background(), bucket, minio.MakeBucketOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		objects := client.ListObjects(context.Background(), bucket, minio.ListObjectsOptions{Recursive: true})
		for object := range objects {
			if object.Err == nil {
				_ = client.RemoveObject(context.Background(), bucket, object.Key, minio.RemoveObjectOptions{})
			}
		}
		if err := client.RemoveBucket(context.Background(), bucket); err != nil {
			t.Errorf("remove isolated MinIO bucket: %v", err)
		}
	})
	store, err := objectstore.NewMinIO(context.Background(), endpoint, access, secret, bucket)
	if err != nil {
		t.Fatal(err)
	}
	return store
}
