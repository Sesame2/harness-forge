package objectstore

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

func TestMinIODeletePrefixPropagatesListingError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("location") {
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/"></LocationConstraint>`))
			return
		}
		if r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2" {
			http.Error(w, "listing failed", http.StatusInternalServerError)
			return
		}
		t.Fatalf("unexpected MinIO request: %s %s", r.Method, r.URL.String())
	}))
	defer server.Close()
	client, err := minio.New(strings.TrimPrefix(server.URL, "http://"), &minio.Options{Creds: credentials.NewStaticV4("test", "test", ""), Secure: false})
	if err != nil {
		t.Fatal(err)
	}
	store := &MinIO{client: client, bucket: "test"}
	if err := store.DeletePrefix(context.Background(), "projects/test/"); err == nil || !strings.Contains(err.Error(), "list objects") {
		t.Fatalf("DeletePrefix() error = %v, want listing error", err)
	}
}
