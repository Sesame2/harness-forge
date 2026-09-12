package artifacthttp

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"harness-forge.local/control-plane/internal/artifacts"
	"harness-forge.local/control-plane/internal/objectstore"
)

type metadata struct {
	records map[uuid.UUID]artifacts.Artifact
	err     error
}

func (m *metadata) Read(_ context.Context, id uuid.UUID) (artifacts.Artifact, error) {
	if m.err != nil {
		return artifacts.Artifact{}, m.err
	}
	record, ok := m.records[id]
	if !ok {
		return artifacts.Artifact{}, artifacts.ErrNotFound
	}
	return record, nil
}

func TestGatewayMetadataGateAssetsAndExactHeaders(t *testing.T) {
	ctx := context.Background()
	id := uuid.New()
	prefix := "projects/" + uuid.NewString() + "/artifacts/" + id.String() + "/"
	objects := objectstore.NewMemory()
	db := &metadata{records: map[uuid.UUID]artifacts.Artifact{}}
	server := NewServer(db, objects, "http://localhost:5173")
	files := map[string]string{"report/index.html": "text/html; charset=utf-8", "report/assets/chart.js": "text/javascript; charset=utf-8", "report/style.css": "text/css; charset=utf-8", "report/photo.png": "image/png", "shared/data.json": "application/json", "unknown.bin": "application/octet-stream", "missing-type.bin": "", "exact.bin": "text/plain; charset=iso-8859-1"}
	for name, contentType := range files {
		if err := objects.Put(ctx, prefix+name, strings.NewReader("body:"+name), objectstore.PutOptions{ContentType: contentType}); err != nil {
			t.Fatal(err)
		}
	}
	request := func(name string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://localhost:8081/artifacts/"+id.String()+"/"+name, nil)
		r.Header.Set("Cookie", "session=control-plane")
		r.Header.Set("Origin", "https://evil.example")
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		return w
	}
	if w := request("report/index.html"); w.Code != 404 {
		t.Fatalf("uncommitted prefix exposed: %d", w.Code)
	}
	db.records[id] = artifacts.Artifact{ID: id, ObjectPrefix: prefix, EntryPath: "report/index.html"}
	for name, want := range files {
		w := request(name)
		if want == "" {
			want = "application/octet-stream"
		}
		if w.Code != 200 || w.Body.String() != "body:"+name || w.Header().Get("Content-Type") != want {
			t.Fatalf("%s: %d %v %s", name, w.Code, w.Header(), w.Body.String())
		}
		if got := w.Header().Get("Content-Security-Policy"); got != "default-src 'self' data: blob:; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; connect-src 'none'; frame-ancestors http://localhost:5173" {
			t.Fatal(got)
		}
		if w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Fatal(w.Header())
		}
		for key := range w.Header() {
			if strings.HasPrefix(strings.ToLower(key), "access-control-") || strings.EqualFold(key, "Set-Cookie") {
				t.Fatalf("control-plane header %s", key)
			}
		}
	}
	if w := request("missing.js"); w.Code != 404 {
		t.Fatal(w.Code)
	}
	delete(db.records, id)
	if w := request("report/assets/chart.js"); w.Code != 404 {
		t.Fatal("removed metadata still visible")
	}
}

func TestGatewayRejectsUnsafePathsWithoutRedirect(t *testing.T) {
	id := uuid.New()
	prefix := "projects/" + uuid.NewString() + "/artifacts/" + id.String() + "/"
	objects := objectstore.NewMemory()
	db := &metadata{records: map[uuid.UUID]artifacts.Artifact{id: {ID: id, ObjectPrefix: prefix, EntryPath: "report/index.html"}}}
	server := NewServer(db, objects, "https://web.example")
	for _, suffix := range []string{"/etc/passwd", "../secret", "report/../../other", "%2e%2e/secret", "%2Fetc/passwd", "report/%2e%2e/secret", "report%2f..%2fsecret", "..%5csecret", "https://evil.example/a", "projects/p/artifacts/other/index.html", "%252e%252e/secret"} {
		w := httptest.NewRecorder()
		server.ServeHTTP(w, httptest.NewRequest("GET", "/artifacts/"+id.String()+"/"+suffix, nil))
		if w.Code != 400 || w.Header().Get("Location") != "" {
			t.Errorf("%s: %d %v", suffix, w.Code, w.Header())
		}
	}
}

func TestGatewayDecodesPathsOnceAndHasNoControlPlaneRoutes(t *testing.T) {
	id := uuid.New()
	prefix := "projects/" + uuid.NewString() + "/artifacts/" + id.String() + "/"
	objects := objectstore.NewMemory()
	if err := objects.Put(context.Background(), prefix+"report/a b.html", strings.NewReader("space"), objectstore.PutOptions{ContentType: "text/html; charset=utf-8"}); err != nil {
		t.Fatal(err)
	}
	server := NewServer(&metadata{records: map[uuid.UUID]artifacts.Artifact{id: {ID: id, ObjectPrefix: prefix}}}, objects, "http://localhost:5173")
	for _, tc := range []struct {
		method, path string
		status       int
	}{{"GET", "/artifacts/" + id.String() + "/report/a%20b.html", 200}, {"GET", "/api/v1/projects", 404}, {"POST", "/artifacts/" + id.String() + "/report/a%20b.html", 405}, {"GET", "/artifacts/not-an-id/report/index.html", 400}} {
		w := httptest.NewRecorder()
		server.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.status {
			t.Fatalf("%s: %d", tc.path, w.Code)
		}
	}
	server = NewServer(&metadata{err: errors.New("database offline")}, objects, "https://web.example")
	w := httptest.NewRecorder()
	server.ServeHTTP(w, httptest.NewRequest("GET", "/artifacts/"+id.String()+"/index.html", nil))
	if w.Code != 500 {
		t.Fatal(w.Code)
	}
}

func TestGatewayMissingPathRedirectsOnlyCommittedArtifactToStoredEntry(t *testing.T) {
	id := uuid.New()
	prefix := "projects/" + uuid.NewString() + "/artifacts/" + id.String() + "/"
	db := &metadata{records: map[uuid.UUID]artifacts.Artifact{}}
	server := NewServer(db, objectstore.NewMemory(), "https://web.example")
	for _, suffix := range []string{"", "/"} {
		w := httptest.NewRecorder()
		server.ServeHTTP(w, httptest.NewRequest("GET", "/artifacts/"+id.String()+suffix, nil))
		if w.Code != 404 {
			t.Fatal("uncommitted entry redirected")
		}
	}
	db.records[id] = artifacts.Artifact{ID: id, ObjectPrefix: prefix, EntryPath: "report/my #1.html"}
	for _, suffix := range []string{"", "/"} {
		w := httptest.NewRecorder()
		server.ServeHTTP(w, httptest.NewRequest("GET", "/artifacts/"+id.String()+suffix, nil))
		if w.Code != 302 || w.Header().Get("Location") != "/artifacts/"+id.String()+"/report/my%20%231.html" {
			t.Fatalf("entry redirect: %d %v", w.Code, w.Header())
		}
	}
}
