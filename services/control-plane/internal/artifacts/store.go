package artifacts

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("artifact not found")

type Artifact struct {
	ID              uuid.UUID `json:"id"`
	RunID           uuid.UUID `json:"run_id"`
	Title           string    `json:"title"`
	Type            string    `json:"type"`
	EntryPath       string    `json:"entry_path"`
	ObjectPrefix    string    `json:"-"`
	IsPrimary       bool      `json:"is_primary"`
	ManifestVersion int       `json:"-"`
	CreatedAt       time.Time `json:"created_at"`
}
type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// InsertTx joins the caller's atomic publication transaction; it never commits
// or rolls back. Object uploads alone do not make an artifact visible.
func (s *Store) InsertTx(ctx context.Context, tx pgx.Tx, records []Artifact) error {
	for _, a := range records {
		if _, err := tx.Exec(ctx, `INSERT INTO artifacts(id,run_id,title,type,entry_path,object_prefix,is_primary,manifest_version) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, a.ID, a.RunID, a.Title, a.Type, a.EntryPath, a.ObjectPrefix, a.IsPrimary, a.ManifestVersion); err != nil {
			return fmt.Errorf("insert artifact: %w", err)
		}
	}
	return nil
}

const artifactColumns = `a.id,a.run_id,a.title,a.type,a.entry_path,a.object_prefix,a.is_primary,a.manifest_version,a.created_at`
const artifactOwners = ` FROM artifacts a JOIN runs r ON r.id=a.run_id JOIN conversations c ON c.id=r.conversation_id JOIN projects p ON p.id=c.project_id `

func (s *Store) Read(ctx context.Context, id uuid.UUID) (Artifact, error) {
	return scanArtifact(s.pool.QueryRow(ctx, `SELECT `+artifactColumns+artifactOwners+`WHERE a.id=$1 AND c.deleted_at IS NULL AND p.deleted_at IS NULL`, id))
}
func (s *Store) ListByRun(ctx context.Context, id uuid.UUID) ([]Artifact, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM runs r JOIN conversations c ON c.id=r.conversation_id JOIN projects p ON p.id=c.project_id WHERE r.id=$1 AND c.deleted_at IS NULL AND p.deleted_at IS NULL)`, id).Scan(&exists); err != nil {
		return nil, fmt.Errorf("read artifact run: %w", err)
	}
	if !exists {
		return nil, ErrNotFound
	}
	rows, err := s.pool.Query(ctx, `SELECT `+artifactColumns+artifactOwners+`WHERE a.run_id=$1 AND c.deleted_at IS NULL AND p.deleted_at IS NULL ORDER BY a.created_at,a.id`, id)
	if err != nil {
		return nil, fmt.Errorf("list run artifacts: %w", err)
	}
	defer rows.Close()
	result := []Artifact{}
	for rows.Next() {
		artifact, err := scanArtifact(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, artifact)
	}
	return result, rows.Err()
}
func scanArtifact(row interface{ Scan(...any) error }) (Artifact, error) {
	var a Artifact
	err := row.Scan(&a.ID, &a.RunID, &a.Title, &a.Type, &a.EntryPath, &a.ObjectPrefix, &a.IsPrimary, &a.ManifestVersion, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Artifact{}, ErrNotFound
	}
	if err != nil {
		return Artifact{}, fmt.Errorf("scan artifact: %w", err)
	}
	return a, nil
}
