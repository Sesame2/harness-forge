package projects

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"harness-forge.local/control-plane/internal/objectstore"
	"harness-forge.local/control-plane/internal/profiles"

	"github.com/google/uuid"
)

type persistence interface {
	CreateProject(context.Context, Project) (Project, error)
	ReadProject(context.Context, uuid.UUID) (Project, error)
	RenameProject(context.Context, uuid.UUID, string) (Project, error)
	ListProjects(context.Context) ([]Project, error)
	SaveInput(context.Context, InputFile) (InputFile, error)
	ListInputs(context.Context, uuid.UUID) ([]InputFile, error)
	DeleteProject(context.Context, uuid.UUID) error
}

type profileResolver interface {
	Resolve(string) (profiles.Snapshot, error)
	ResolveVersion(string, string) (profiles.Snapshot, error)
}

type Service struct {
	store    persistence
	profiles profileResolver
	objects  objectstore.Store
}

func NewService(store persistence, profiles profileResolver, objects objectstore.Store) *Service {
	return &Service{store: store, profiles: profiles, objects: objects}
}

func (s *Service) CreateProject(ctx context.Context, name, profileID string) (Project, error) {
	name = strings.TrimSpace(name)
	profileID = strings.TrimSpace(profileID)
	if name == "" || profileID == "" {
		return Project{}, fmt.Errorf("%w: name and profile_id are required", ErrInvalid)
	}
	snapshot, err := s.profiles.Resolve(profileID)
	if err != nil {
		return Project{}, fmt.Errorf("%w: unknown profile %q", ErrInvalid, profileID)
	}
	project, err := s.store.CreateProject(ctx, Project{ID: uuid.New(), Name: name, ProfileID: snapshot.ID, ProfileVersion: snapshot.Version})
	if err != nil {
		return Project{}, err
	}
	project.AcceptedInputMediaTypes = append([]string(nil), snapshot.AcceptedInputMediaTypes...)
	return project, nil
}

func (s *Service) ReadProject(ctx context.Context, id uuid.UUID) (Project, error) {
	project, err := s.store.ReadProject(ctx, id)
	if err != nil {
		return Project{}, err
	}
	return s.enrich(project)
}

func (s *Service) RenameProject(ctx context.Context, id uuid.UUID, name string) (Project, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Project{}, fmt.Errorf("%w: name is required", ErrInvalid)
	}
	project, err := s.store.RenameProject(ctx, id, name)
	if err != nil {
		return Project{}, err
	}
	return s.enrich(project)
}

func (s *Service) ListProjects(ctx context.Context) ([]Project, error) {
	projects, err := s.store.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	for index := range projects {
		projects[index], err = s.enrich(projects[index])
		if err != nil {
			return nil, err
		}
	}
	return projects, nil
}

func (s *Service) UploadInput(ctx context.Context, projectID uuid.UUID, displayName, mediaType string, reader io.Reader) (InputFile, error) {
	project, err := s.store.ReadProject(ctx, projectID)
	if err != nil {
		return InputFile{}, err
	}
	snapshot, err := s.profiles.ResolveVersion(project.ProfileID, project.ProfileVersion)
	if err != nil {
		return InputFile{}, fmt.Errorf("%w: pinned profile %s@%s is unavailable", ErrConflict, project.ProfileID, project.ProfileVersion)
	}
	displayName = strings.TrimSpace(displayName)
	mediaType = strings.TrimSpace(strings.Split(mediaType, ";")[0])
	if displayName == "" || path.Base(displayName) != displayName || !contains(snapshot.AcceptedInputMediaTypes, mediaType) {
		return InputFile{}, fmt.Errorf("%w: invalid filename or media type", ErrInvalid)
	}
	input := InputFile{ID: uuid.New(), ProjectID: projectID, DisplayName: displayName, MediaType: mediaType}
	input.ObjectKey = fmt.Sprintf("projects/%s/inputs/%s/%s", projectID, input.ID, displayName)
	hash := sha256.New()
	counter := &countWriter{}
	stream := io.TeeReader(io.LimitReader(reader, snapshot.Artifacts.MaxFileBytes+1), io.MultiWriter(hash, counter))
	if err := s.objects.Put(ctx, input.ObjectKey, stream, objectstore.PutOptions{ContentType: mediaType}); err != nil {
		_ = s.objects.Delete(ctx, input.ObjectKey)
		return InputFile{}, fmt.Errorf("store input object: %w", err)
	}
	if counter.count > snapshot.Artifacts.MaxFileBytes {
		_ = s.objects.Delete(ctx, input.ObjectKey)
		return InputFile{}, fmt.Errorf("%w: input exceeds %d bytes", ErrPayloadTooLarge, snapshot.Artifacts.MaxFileBytes)
	}
	input.SizeBytes, input.SHA256Digest = counter.count, hex.EncodeToString(hash.Sum(nil))
	objectKey := input.ObjectKey
	input, err = s.store.SaveInput(ctx, input)
	if err != nil {
		if deleteErr := s.objects.Delete(ctx, objectKey); deleteErr != nil {
			return InputFile{}, errors.Join(err, fmt.Errorf("compensate input object: %w", deleteErr))
		}
		return InputFile{}, err
	}
	return input, nil
}

func (s *Service) ListInputs(ctx context.Context, projectID uuid.UUID) ([]InputFile, error) {
	return s.store.ListInputs(ctx, projectID)
}

func (s *Service) DeleteProject(ctx context.Context, projectID uuid.UUID) error {
	return s.store.DeleteProject(ctx, projectID)
}

func (s *Service) enrich(project Project) (Project, error) {
	snapshot, err := s.profiles.ResolveVersion(project.ProfileID, project.ProfileVersion)
	if err != nil {
		return Project{}, fmt.Errorf("%w: pinned profile %s@%s is unavailable", ErrConflict, project.ProfileID, project.ProfileVersion)
	}
	project.AcceptedInputMediaTypes = append([]string(nil), snapshot.AcceptedInputMediaTypes...)
	return project, nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

type countWriter struct{ count int64 }

func (w *countWriter) Write(p []byte) (int, error) { w.count += int64(len(p)); return len(p), nil }
