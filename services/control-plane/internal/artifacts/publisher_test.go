package artifacts

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/google/uuid"
	"harness-forge.local/control-plane/internal/contracts"
	"harness-forge.local/control-plane/internal/objectstore"
)

type observedObjects struct {
	*objectstore.Memory
	keys      []string
	failAt    int
	beforePut func()
}

func (s *observedObjects) Put(ctx context.Context, key string, body io.Reader, options objectstore.PutOptions) error {
	if s.beforePut != nil {
		s.beforePut()
	}
	s.keys = append(s.keys, key)
	if err := s.Memory.Put(ctx, key, body, options); err != nil {
		return err
	}
	if s.failAt > 0 && len(s.keys) == s.failAt {
		return errors.New("upload failed after object write")
	}
	return nil
}

func TestPublishPreparesUnderLockAndPreservesOutputLayoutAndContentType(t *testing.T) {
	root := outputFixture(t, contracts.ArtifactCandidate{Name: "report", Title: "Report", Type: "html", Entry: "report/index.html", Primary: true}, contracts.ArtifactCandidate{Name: "data", Title: "Data", Type: "data", Entry: "shared/data.json"})
	locked, releases := false, 0
	objects := &observedObjects{Memory: objectstore.NewMemory()}
	objects.beforePut = func() {
		if !locked {
			t.Fatal("uploaded without maintenance lock")
		}
	}
	p := &Publisher{objects: objects, acquire: func(context.Context) (func() error, error) {
		locked = true
		return func() error { locked = false; releases++; return nil }, nil
	}}
	project, run := uuid.New(), uuid.New()
	prepared, err := p.Prepare(context.Background(), project, run, root, snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if !locked || releases != 0 || len(prepared.Records) != 2 {
		t.Fatalf("publication %#v lock %v release %d", prepared, locked, releases)
	}
	for i, record := range prepared.Records {
		if record.RunID != run || record.ID == uuid.Nil || record.ObjectPrefix != "projects/"+project.String()+"/artifacts/"+record.ID.String()+"/" || record.IsPrimary != (i == 0) || record.ManifestVersion != 1 {
			t.Fatalf("record %#v", record)
		}
		for _, relative := range []string{"report/index.html", "report/assets/chart.js", "report/style.css", "shared/data.json", "report/photo.png"} {
			info, err := objects.Stat(context.Background(), record.ObjectPrefix+relative)
			if err != nil || info.ContentType != ContentType(relative) {
				t.Fatalf("%s: %#v %v", relative, info, err)
			}
		}
	}
	if prepared.Records[0].ID == prepared.Records[1].ID {
		t.Fatal("reused artifact identity")
	}
	if err := prepared.Release(); err != nil {
		t.Fatal(err)
	}
	if err := prepared.Release(); err != nil {
		t.Fatal(err)
	}
	if locked || releases != 1 {
		t.Fatal("release not idempotent")
	}
}

func TestPublishPartialUploadFailureCleansAllPrefixesAndReleases(t *testing.T) {
	objects := &observedObjects{Memory: objectstore.NewMemory(), failAt: 7}
	releases := 0
	p := &Publisher{objects: objects, acquire: func(context.Context) (func() error, error) { return func() error { releases++; return nil }, nil }}
	root := outputFixture(t, contracts.ArtifactCandidate{Name: "a", Title: "A", Type: "html", Entry: "report/index.html"}, contracts.ArtifactCandidate{Name: "b", Title: "B", Type: "html", Entry: "report/index.html"})
	prepared, err := p.Prepare(context.Background(), uuid.New(), uuid.New(), root, snapshot())
	if err == nil || prepared != nil || releases != 1 {
		t.Fatalf("%#v %v releases=%d", prepared, err, releases)
	}
	for _, key := range objects.keys {
		if _, err := objects.Stat(context.Background(), key); err == nil {
			t.Fatalf("partial object survived: %s", key)
		}
	}
}

func TestPublishValidationAndLockErrorsHaveNoUploadsOrLeak(t *testing.T) {
	for _, acquireFails := range []bool{false, true} {
		objects, releases := &observedObjects{Memory: objectstore.NewMemory()}, 0
		p := &Publisher{objects: objects, acquire: func(context.Context) (func() error, error) {
			if acquireFails {
				return nil, errors.New("lock unavailable")
			}
			return func() error { releases++; return nil }, nil
		}}
		root := t.TempDir()
		writeOutput(t, root, "missing-manifest.html", "html")
		_, err := p.Prepare(context.Background(), uuid.New(), uuid.New(), root, snapshot())
		if err == nil || len(objects.keys) != 0 || (!acquireFails && releases != 1) || (acquireFails && releases != 0) {
			t.Fatalf("%v %v releases=%d", err, objects.keys, releases)
		}
	}
}

func TestPublishCancelledUploadUsesIndependentCleanupContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	objects := &cancelledObjects{observedObjects: &observedObjects{Memory: objectstore.NewMemory()}, cancel: cancel}
	p := &Publisher{objects: objects, acquire: func(context.Context) (func() error, error) { return func() error { return nil }, nil }}
	_, err := p.Prepare(ctx, uuid.New(), uuid.New(), outputFixture(t), snapshot())
	if err == nil || !objects.cleaned {
		t.Fatalf("cancelled publication cleanup: %v cleaned=%v", err, objects.cleaned)
	}
}

type cancelledObjects struct {
	*observedObjects
	cancel  context.CancelFunc
	cleaned bool
}

func (s *cancelledObjects) Put(ctx context.Context, key string, body io.Reader, options objectstore.PutOptions) error {
	_ = s.observedObjects.Put(ctx, key, body, options)
	s.cancel()
	return context.Canceled
}
func (s *cancelledObjects) DeletePrefix(ctx context.Context, prefix string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	s.cleaned = true
	return s.Memory.DeletePrefix(ctx, prefix)
}

func TestPublishDoesNotSniffUnknownObject(t *testing.T) {
	root := outputFixture(t)
	writeOutput(t, root, "payload.unknown", "<html>active</html>")
	objects := objectstore.NewMemory()
	p := &Publisher{objects: objects, acquire: func(context.Context) (func() error, error) { return func() error { return nil }, nil }}
	prepared, err := p.Prepare(context.Background(), uuid.New(), uuid.New(), root, snapshot())
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Release()
	info, err := objects.Stat(context.Background(), prepared.Records[0].ObjectPrefix+"payload.unknown")
	if err != nil || !strings.EqualFold(info.ContentType, "application/octet-stream") {
		t.Fatalf("%#v %v", info, err)
	}
}

func TestPublishCancelledEmptyOutputsReleases(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	releases := 0
	p := &Publisher{objects: objectstore.NewMemory(), acquire: func(context.Context) (func() error, error) {
		cancel()
		return func() error { releases++; return nil }, nil
	}}
	prepared, err := p.Prepare(ctx, uuid.New(), uuid.New(), t.TempDir(), snapshot())
	if !errors.Is(err, context.Canceled) || prepared != nil || releases != 1 {
		t.Fatalf("cancelled empty publication: %#v %v releases %d", prepared, err, releases)
	}
}
