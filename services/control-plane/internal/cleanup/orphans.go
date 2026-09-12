package cleanup

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"harness-forge.local/control-plane/internal/artifacts"
	"harness-forge.local/control-plane/internal/objectstore"
)

type OrphanSummary struct {
	Orphans []string `json:"orphans"`
	Deleted []string `json:"deleted"`
	Invalid []string `json:"invalid"`
}

type OrphanScanner struct {
	objects     objectstore.Store
	acquire     func(context.Context) (func() error, error)
	hasMetadata func(context.Context, uuid.UUID, string) (bool, error)
}

func NewOrphanScanner(pool *pgxpool.Pool, objects objectstore.Store) *OrphanScanner {
	return &OrphanScanner{
		objects: objects,
		acquire: func(ctx context.Context) (func() error, error) {
			return artifacts.AcquirePublicationLock(ctx, pool)
		},
		hasMetadata: func(ctx context.Context, id uuid.UUID, prefix string) (bool, error) {
			var exists bool
			err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM artifacts WHERE id=$1 AND object_prefix=$2)`, id, prefix).Scan(&exists)
			return exists, err
		},
	}
}

func (s *OrphanScanner) Scan(ctx context.Context, apply bool) (OrphanSummary, error) {
	return s.scan(ctx, apply)
}

func (s *OrphanScanner) scan(ctx context.Context, apply bool) (summary OrphanSummary, err error) {
	summary = OrphanSummary{Orphans: []string{}, Deleted: []string{}, Invalid: []string{}}
	release, err := s.acquire(ctx)
	if err != nil {
		return summary, err
	}
	defer func() { err = errors.Join(err, release()) }()
	projects, err := s.objects.ListPrefixes(ctx, "projects/")
	if err != nil {
		return summary, err
	}
	for _, projectPrefix := range projects {
		projectID := strings.TrimSuffix(strings.TrimPrefix(projectPrefix, "projects/"), "/")
		if uuid.Validate(projectID) != nil || projectPrefix != "projects/"+projectID+"/" {
			summary.Invalid = append(summary.Invalid, projectPrefix)
			continue
		}
		children, err := s.objects.ListPrefixes(ctx, projectPrefix)
		if err != nil {
			return summary, err
		}
		artifactBase := projectPrefix + "artifacts/"
		for _, child := range children {
			switch child {
			case artifactBase:
				if err := s.scanArtifacts(ctx, artifactBase, apply, &summary); err != nil {
					return summary, err
				}
			case projectPrefix + "inputs/":
				// Inputs are project-owned, not publication orphans.
			default:
				summary.Invalid = append(summary.Invalid, child)
			}
		}
	}
	return summary, nil
}

func (s *OrphanScanner) scanArtifacts(ctx context.Context, base string, apply bool, summary *OrphanSummary) error {
	prefixes, err := s.objects.ListPrefixes(ctx, base)
	if err != nil {
		return err
	}
	for _, prefix := range prefixes {
		rawID := strings.TrimSuffix(strings.TrimPrefix(prefix, base), "/")
		id, parseErr := uuid.Parse(rawID)
		if parseErr != nil || prefix != base+rawID+"/" {
			summary.Invalid = append(summary.Invalid, prefix)
			continue
		}
		exists, err := s.hasMetadata(ctx, id, prefix)
		if err != nil {
			return fmt.Errorf("check artifact metadata %s: %w", id, err)
		}
		if exists {
			continue
		}
		summary.Orphans = append(summary.Orphans, prefix)
		if apply {
			if err := s.objects.DeletePrefix(ctx, prefix); err != nil {
				return err
			}
			summary.Deleted = append(summary.Deleted, prefix)
		}
	}
	return nil
}
