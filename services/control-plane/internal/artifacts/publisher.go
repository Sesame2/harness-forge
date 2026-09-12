package artifacts

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"harness-forge.local/control-plane/internal/objectstore"
	"harness-forge.local/control-plane/internal/profiles"
)

type Publisher struct {
	objects objectstore.Store
	acquire func(context.Context) (func() error, error)
}

// PreparedPublication owns the maintenance lock. The caller MUST finish its
// metadata transaction (commit or rollback) before Release, including on error.
// Prepare never writes metadata or changes a Run's status.
type PreparedPublication struct {
	Records    []Artifact
	release    func() error
	once       sync.Once
	releaseErr error
}

func NewPublisher(pool *pgxpool.Pool, objects objectstore.Store) *Publisher {
	return &Publisher{objects: objects, acquire: func(ctx context.Context) (func() error, error) { return AcquirePublicationLock(ctx, pool) }}
}
func (p *Publisher) Prepare(ctx context.Context, projectID, runID uuid.UUID, outputsRoot string, snapshot profiles.Snapshot) (_ *PreparedPublication, err error) {
	release, err := p.acquire(ctx)
	if err != nil {
		return nil, err
	}
	prepared := &PreparedPublication{Records: []Artifact{}, release: release}
	defer func() {
		if err != nil {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for _, record := range prepared.Records {
				err = errors.Join(err, p.objects.DeletePrefix(cleanup, record.ObjectPrefix))
			}
			err = errors.Join(err, prepared.Release())
		}
	}()
	if projectID == uuid.Nil || runID == uuid.Nil {
		return nil, errors.New("project and run IDs are required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	validated, err := ValidateManifest(outputsRoot, snapshot)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, candidate := range validated.Manifest.Artifacts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		id := uuid.New()
		record := Artifact{ID: id, RunID: runID, Title: candidate.Title, Type: candidate.Type, EntryPath: candidate.Entry, ObjectPrefix: "projects/" + projectID.String() + "/artifacts/" + id.String() + "/", IsPrimary: candidate.Primary, ManifestVersion: validated.Manifest.SchemaVersion}
		prepared.Records = append(prepared.Records, record)
		// The manifest has no membership list: copy the validated output tree to
		// each artifact, preserving relative assets and shared sibling data.
		for _, file := range validated.Files {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if err := p.objects.Put(ctx, record.ObjectPrefix+file.Path, bytes.NewReader(file.Data), objectstore.PutOptions{ContentType: ContentType(file.Path)}); err != nil {
				return nil, fmt.Errorf("upload artifact %s: %w", id, err)
			}
		}
	}
	return prepared, nil
}
func (p *PreparedPublication) Release() error {
	p.once.Do(func() { p.releaseErr = p.release() })
	return p.releaseErr
}
