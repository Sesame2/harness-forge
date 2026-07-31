package projects

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"harness-forge.local/control-plane/internal/objectstore"
	"harness-forge.local/control-plane/internal/profiles"

	"github.com/google/uuid"
)

func TestProjectCreateReadRenameListAndProfileEnrichment(t *testing.T) {
	ctx := context.Background()
	store := newFakePersistence()
	service := NewService(store, fakeResolver{snapshot: testSnapshot()}, newFakeObjects())

	first, err := service.CreateProject(ctx, "  First project  ", "geo-analysis")
	if err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}
	second, err := service.CreateProject(ctx, "First project", "geo-analysis")
	if err != nil {
		t.Fatalf("second CreateProject() error = %v", err)
	}
	if first.ID == second.ID {
		t.Fatal("same-name projects have equal IDs")
	}
	if first.Name != "First project" || first.ProfileID != "geo-analysis" || first.ProfileVersion != "1" {
		t.Fatalf("created project = %#v", first)
	}
	if len(first.AcceptedInputMediaTypes) != 2 {
		t.Fatalf("accepted media = %#v", first.AcceptedInputMediaTypes)
	}

	read, err := service.ReadProject(ctx, first.ID)
	if err != nil || read.ID != first.ID || len(read.AcceptedInputMediaTypes) != 2 {
		t.Fatalf("ReadProject() = %#v, %v", read, err)
	}
	renamed, err := service.RenameProject(ctx, first.ID, "  Renamed  ")
	if err != nil || renamed.Name != "Renamed" {
		t.Fatalf("RenameProject() = %#v, %v", renamed, err)
	}
	list, err := service.ListProjects(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("ListProjects() = %#v, %v", list, err)
	}
	for _, project := range list {
		if len(project.AcceptedInputMediaTypes) != 2 {
			t.Errorf("list project missing resolver enrichment: %#v", project)
		}
	}
}

func TestProjectCreateValidatesNameAndProfile(t *testing.T) {
	service := NewService(newFakePersistence(), fakeResolver{err: profiles.ErrNotFound}, newFakeObjects())
	if _, err := service.CreateProject(context.Background(), "name", "missing"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown profile error = %v, want ErrInvalid", err)
	}
	service = NewService(newFakePersistence(), fakeResolver{snapshot: testSnapshot()}, newFakeObjects())
	if _, err := service.CreateProject(context.Background(), " \t\n", "geo-analysis"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("blank name error = %v, want ErrInvalid", err)
	}
}

func TestProjectListHidesDeleted(t *testing.T) {
	store := newFakePersistence()
	service := NewService(store, fakeResolver{snapshot: testSnapshot()}, newFakeObjects())
	active, _ := service.CreateProject(context.Background(), "active", "geo-analysis")
	deleted, _ := service.CreateProject(context.Background(), "deleted", "geo-analysis")
	when := time.Now()
	row := store.projects[deleted.ID]
	row.DeletedAt = &when
	store.projects[deleted.ID] = row
	list, err := service.ListProjects(context.Background())
	if err != nil || len(list) != 1 || list[0].ID != active.ID {
		t.Fatalf("ListProjects() = %#v, %v", list, err)
	}
}

func TestInputFileUploadStreamsHashesAndUsesFinalKey(t *testing.T) {
	ctx := context.Background()
	store := newFakePersistence()
	objects := newFakeObjects()
	service := NewService(store, fakeResolver{snapshot: testSnapshot()}, objects)
	project, _ := service.CreateProject(ctx, "project", "geo-analysis")
	reader := &observedReader{data: []byte("lat,lon\n1,2\n")}
	objects.beforeRead = func() {
		if reader.reads != 0 {
			t.Fatalf("reader consumed %d times before object Put", reader.reads)
		}
	}

	input, err := service.UploadInput(ctx, project.ID, " observations.csv ", "text/csv", reader)
	if err != nil {
		t.Fatalf("UploadInput() error = %v", err)
	}
	wantKey := "projects/" + project.ID.String() + "/inputs/" + input.ID.String() + "/observations.csv"
	if objects.putKey != wantKey || input.ObjectKey != wantKey {
		t.Fatalf("object key = %q/%q, want %q", objects.putKey, input.ObjectKey, wantKey)
	}
	wantDigest := sha256.Sum256([]byte("lat,lon\n1,2\n"))
	if input.SHA256Digest != hex.EncodeToString(wantDigest[:]) || input.SizeBytes != 12 || input.MediaType != "text/csv" || input.DisplayName != "observations.csv" {
		t.Fatalf("input metadata = %#v", input)
	}
	if objects.putOptions.ContentType != "text/csv" {
		t.Errorf("Put ContentType = %q", objects.putOptions.ContentType)
	}
	listed, err := service.ListInputs(ctx, project.ID)
	if err != nil || len(listed) != 1 || listed[0].ID != input.ID {
		t.Fatalf("ListInputs() = %#v, %v", listed, err)
	}
}

func TestInputFileUploadRejectsMediaAndOversize(t *testing.T) {
	ctx := context.Background()
	store := newFakePersistence()
	service := NewService(store, fakeResolver{snapshot: testSnapshot()}, newFakeObjects())
	project, _ := service.CreateProject(ctx, "project", "geo-analysis")
	if _, err := service.UploadInput(ctx, project.ID, "data.json", "application/json", strings.NewReader("{}")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("disallowed media error = %v", err)
	}
	snapshot := testSnapshot()
	snapshot.Artifacts.MaxFileBytes = 3
	service = NewService(store, fakeResolver{snapshot: snapshot}, newFakeObjects())
	if _, err := service.UploadInput(ctx, project.ID, "data.csv", "text/csv", strings.NewReader("1234")); !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("oversize error = %v", err)
	}
}

func TestInputFileUploadCompensatesExactObjectOnMetadataFailure(t *testing.T) {
	ctx := context.Background()
	store := newFakePersistence()
	objects := newFakeObjects()
	service := NewService(store, fakeResolver{snapshot: testSnapshot()}, objects)
	project, _ := service.CreateProject(ctx, "project", "geo-analysis")
	store.saveInputErr = ErrConflict
	_, err := service.UploadInput(ctx, project.ID, "data.csv", "text/csv", strings.NewReader("a,b"))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("UploadInput() error = %v", err)
	}
	if objects.deletedKey == "" || objects.deletedKey != objects.putKey {
		t.Fatalf("compensation key = %q, put key = %q", objects.deletedKey, objects.putKey)
	}
	if len(store.inputs) != 0 {
		t.Fatalf("metadata written after failure: %#v", store.inputs)
	}
}

func TestInputFilePutFailureDoesNotWriteMetadata(t *testing.T) {
	store := newFakePersistence()
	objects := newFakeObjects()
	objects.putErr = errors.New("object unavailable")
	service := NewService(store, fakeResolver{snapshot: testSnapshot()}, objects)
	project, _ := service.CreateProject(context.Background(), "project", "geo-analysis")
	if _, err := service.UploadInput(context.Background(), project.ID, "data.csv", "text/csv", strings.NewReader("a,b")); err == nil {
		t.Fatal("UploadInput() error = nil")
	}
	if len(store.inputs) != 0 {
		t.Fatalf("metadata written after Put failure: %#v", store.inputs)
	}
}

func TestInputFileUploadFailsClosedWhenPinnedProfileIsMissing(t *testing.T) {
	store := newFakePersistence()
	resolver := &versionResolver{snapshot: testSnapshot()}
	service := NewService(store, resolver, newFakeObjects())
	project, _ := service.CreateProject(context.Background(), "project", "geo-analysis")
	resolver.versionErr = profiles.ErrNotFound
	if _, err := service.UploadInput(context.Background(), project.ID, "data.csv", "text/csv", strings.NewReader("a,b")); !errors.Is(err, ErrConflict) {
		t.Fatalf("missing pinned profile error = %v, want ErrConflict", err)
	}
}

func testSnapshot() profiles.Snapshot {
	return profiles.Snapshot{ID: "geo-analysis", Version: "1", AcceptedInputMediaTypes: []string{"text/csv", "application/geo+json"}, Artifacts: profiles.ArtifactPolicy{MaxFileBytes: 10_485_760}}
}

type fakeResolver struct {
	snapshot profiles.Snapshot
	err      error
}

func (r fakeResolver) Resolve(string) (profiles.Snapshot, error) { return r.snapshot, r.err }
func (r fakeResolver) ResolveVersion(string, string) (profiles.Snapshot, error) {
	return r.snapshot, r.err
}

type versionResolver struct {
	snapshot   profiles.Snapshot
	versionErr error
}

func (r *versionResolver) Resolve(string) (profiles.Snapshot, error) { return r.snapshot, nil }
func (r *versionResolver) ResolveVersion(string, string) (profiles.Snapshot, error) {
	return r.snapshot, r.versionErr
}

type fakePersistence struct {
	projects     map[uuid.UUID]Project
	inputs       map[uuid.UUID][]InputFile
	saveInputErr error
}

func newFakePersistence() *fakePersistence {
	return &fakePersistence{projects: map[uuid.UUID]Project{}, inputs: map[uuid.UUID][]InputFile{}}
}
func (s *fakePersistence) CreateProject(_ context.Context, project Project) (Project, error) {
	now := time.Now().UTC()
	project.CreatedAt = now
	project.UpdatedAt = now
	s.projects[project.ID] = project
	return project, nil
}
func (s *fakePersistence) ReadProject(_ context.Context, id uuid.UUID) (Project, error) {
	p, ok := s.projects[id]
	if !ok || p.DeletedAt != nil {
		return Project{}, ErrNotFound
	}
	return p, nil
}
func (s *fakePersistence) RenameProject(_ context.Context, id uuid.UUID, name string) (Project, error) {
	p, err := s.ReadProject(context.Background(), id)
	if err != nil {
		return Project{}, err
	}
	p.Name = name
	p.UpdatedAt = time.Now().UTC()
	s.projects[id] = p
	return p, nil
}
func (s *fakePersistence) ListProjects(context.Context) ([]Project, error) {
	result := []Project{}
	for _, p := range s.projects {
		if p.DeletedAt == nil {
			result = append(result, p)
		}
	}
	return result, nil
}
func (s *fakePersistence) SaveInput(_ context.Context, input InputFile) (InputFile, error) {
	if s.saveInputErr != nil {
		return InputFile{}, s.saveInputErr
	}
	p, ok := s.projects[input.ProjectID]
	if !ok {
		return InputFile{}, ErrNotFound
	}
	if p.DeletedAt != nil {
		return InputFile{}, ErrConflict
	}
	input.CreatedAt = time.Now().UTC()
	s.inputs[input.ProjectID] = append(s.inputs[input.ProjectID], input)
	return input, nil
}
func (s *fakePersistence) ListInputs(_ context.Context, id uuid.UUID) ([]InputFile, error) {
	if _, err := s.ReadProject(context.Background(), id); err != nil {
		return nil, err
	}
	return s.inputs[id], nil
}
func (s *fakePersistence) DeleteProject(_ context.Context, id uuid.UUID) error {
	p, ok := s.projects[id]
	if !ok {
		return ErrNotFound
	}
	if p.DeletedAt != nil {
		return nil
	}
	now := time.Now().UTC()
	p.DeletedAt = &now
	s.projects[id] = p
	return nil
}

type fakeObjects struct {
	putKey, deletedKey string
	putOptions         objectstore.PutOptions
	data               []byte
	putErr             error
	beforeRead         func()
}

func newFakeObjects() *fakeObjects { return &fakeObjects{} }
func (s *fakeObjects) Put(_ context.Context, key string, reader io.Reader, options objectstore.PutOptions) error {
	s.putKey = key
	s.putOptions = options
	if s.beforeRead != nil {
		s.beforeRead()
	}
	if s.putErr != nil {
		return s.putErr
	}
	var err error
	s.data, err = io.ReadAll(reader)
	return err
}
func (s *fakeObjects) Open(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(s.data)), nil
}
func (s *fakeObjects) Delete(_ context.Context, key string) error { s.deletedKey = key; return nil }
func (s *fakeObjects) DeletePrefix(context.Context, string) error { return nil }
func (s *fakeObjects) Stat(context.Context, string) (objectstore.ObjectInfo, error) {
	return objectstore.ObjectInfo{}, nil
}

type observedReader struct {
	data          []byte
	offset, reads int
}

func (r *observedReader) Read(p []byte) (int, error) {
	r.reads++
	if r.offset == len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.offset:])
	r.offset += n
	return n, nil
}
