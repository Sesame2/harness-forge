package cleanup

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"harness-forge.local/control-plane/internal/objectstore"
)

func TestOrphanScannerDeletesOnlyValidPrefixesWithoutMetadata(t *testing.T) {
	ctx := context.Background()
	projectID, keptID, orphanID := uuid.New(), uuid.New(), uuid.New()
	kept := "projects/" + projectID.String() + "/artifacts/" + keptID.String() + "/"
	orphan := "projects/" + projectID.String() + "/artifacts/" + orphanID.String() + "/"
	invalidID := "projects/" + projectID.String() + "/artifacts/not-a-uuid/"
	invalidLevel := "projects/" + projectID.String() + "/unexpected/value/"
	objects := objectstore.NewMemory()
	for _, key := range []string{kept + "index.html", orphan + "index.html", invalidID + "index.html", invalidLevel + "file"} {
		if err := objects.Put(ctx, key, strings.NewReader(key), objectstore.PutOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	locked, released := false, false
	scanner := OrphanScanner{
		objects: objects,
		acquire: func(context.Context) (func() error, error) {
			locked = true
			return func() error { released = true; return nil }, nil
		},
		hasMetadata: func(_ context.Context, id uuid.UUID, prefix string) (bool, error) {
			if !locked || released {
				t.Fatal("metadata compared outside maintenance lock")
			}
			return id == keptID && prefix == kept, nil
		},
	}

	summary, err := scanner.scan(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if !released {
		t.Fatal("maintenance lock not released")
	}
	if len(summary.Orphans) != 1 || summary.Orphans[0] != orphan {
		t.Fatalf("orphans = %v", summary.Orphans)
	}
	if len(summary.Deleted) != 1 || summary.Deleted[0] != orphan {
		t.Fatalf("deleted = %v", summary.Deleted)
	}
	if len(summary.Invalid) != 2 {
		t.Fatalf("invalid = %v", summary.Invalid)
	}
	if _, err := objects.Stat(ctx, kept+"index.html"); err != nil {
		t.Fatalf("metadata-backed prefix deleted: %v", err)
	}
	if _, err := objects.Stat(ctx, invalidID+"index.html"); err != nil {
		t.Fatalf("invalid prefix deleted: %v", err)
	}
	if _, err := objects.Stat(ctx, orphan+"index.html"); err == nil {
		t.Fatal("orphan prefix retained")
	}
}

func TestOrphanScannerDryRunReportsWithoutDeleting(t *testing.T) {
	ctx := context.Background()
	projectID, artifactID := uuid.New(), uuid.New()
	prefix := "projects/" + projectID.String() + "/artifacts/" + artifactID.String() + "/"
	objects := objectstore.NewMemory()
	if err := objects.Put(ctx, prefix+"index.html", strings.NewReader("ok"), objectstore.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	scanner := OrphanScanner{objects: objects, acquire: func(context.Context) (func() error, error) { return func() error { return nil }, nil }, hasMetadata: func(context.Context, uuid.UUID, string) (bool, error) { return false, nil }}
	summary, err := scanner.scan(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Orphans) != 1 || len(summary.Deleted) != 0 {
		t.Fatalf("dry-run summary = %#v", summary)
	}
	if _, err := objects.Stat(ctx, prefix+"index.html"); err != nil {
		t.Fatalf("dry-run deleted object: %v", err)
	}
}
