package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"harness-forge.local/control-plane/internal/artifacts"
)

type artifactList struct {
	run     uuid.UUID
	records []artifacts.Artifact
	err     error
}

func (s *artifactList) ListByRun(_ context.Context, id uuid.UUID) ([]artifacts.Artifact, error) {
	if s.err != nil {
		return nil, s.err
	}
	if id != s.run {
		return nil, artifacts.ErrNotFound
	}
	return s.records, nil
}

func TestArtifactListUsesConfiguredGatewayOriginAndHidesStorageMetadata(t *testing.T) {
	run, id := uuid.New(), uuid.New()
	store := &artifactList{run: run, records: []artifacts.Artifact{{ID: id, RunID: run, Title: "Report", Type: "html", EntryPath: "report/my #1.html", ObjectPrefix: "projects/private/artifacts/secret/", IsPrimary: true, ManifestVersion: 1, CreatedAt: time.Now().UTC()}}}
	router := NewRouter(Dependencies{Artifacts: store, ArtifactPublicOrigin: "https://artifacts.example"})
	r := httptest.NewRequest("GET", "https://attacker.example/api/v1/runs/"+run.String()+"/artifacts", nil)
	r.Header.Set("X-Forwarded-Host", "injected.example")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("artifact list status: %d %s", w.Code, w.Body.String())
	}
	var got []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(got) != 1 || got[0]["is_primary"] != true || got[0]["gateway_url"] != "https://artifacts.example/artifacts/"+id.String()+"/report/my%20%231.html" {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), "manifest_version") || strings.Contains(w.Body.String(), "attacker") || strings.Contains(w.Body.String(), "injected") {
		t.Fatal(w.Body.String())
	}
	for _, field := range []string{"id", "run_id", "title", "type", "entry_path", "is_primary", "gateway_url", "created_at"} {
		if _, ok := got[0][field]; !ok {
			t.Fatalf("missing %s", field)
		}
	}
}

func TestArtifactListEmptyMissingAndInvalidRun(t *testing.T) {
	run := uuid.New()
	store := &artifactList{run: run, records: []artifacts.Artifact{}}
	router := NewRouter(Dependencies{Artifacts: store, ArtifactPublicOrigin: "http://localhost:8081"})
	for _, tc := range []struct {
		id     string
		status int
	}{{run.String(), 200}, {uuid.NewString(), 404}, {"invalid", 400}} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/runs/"+tc.id+"/artifacts", nil))
		if w.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.id, w.Code, w.Body.String())
		}
		if tc.status == 200 && strings.TrimSpace(w.Body.String()) != "[]" {
			t.Fatal(w.Body.String())
		}
	}
}
