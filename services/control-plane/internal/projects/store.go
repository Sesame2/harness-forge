package projects

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) CreateProject(ctx context.Context, project Project) (Project, error) {
	err := s.pool.QueryRow(ctx, `INSERT INTO projects(id,name,profile_id,profile_version) VALUES($1,$2,$3,$4) RETURNING created_at,updated_at`, project.ID, project.Name, project.ProfileID, project.ProfileVersion).Scan(&project.CreatedAt, &project.UpdatedAt)
	if err != nil {
		return Project{}, fmt.Errorf("create project: %w", err)
	}
	return project, nil
}

func (s *Store) ReadProject(ctx context.Context, id uuid.UUID) (Project, error) {
	project, err := scanProject(s.pool.QueryRow(ctx, `SELECT id,name,profile_id,profile_version,created_at,updated_at,deleted_at FROM projects WHERE id=$1 AND deleted_at IS NULL`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	if err != nil {
		return Project{}, fmt.Errorf("read project: %w", err)
	}
	return project, nil
}

func (s *Store) RenameProject(ctx context.Context, id uuid.UUID, name string) (Project, error) {
	project, err := scanProject(s.pool.QueryRow(ctx, `UPDATE projects SET name=$2,updated_at=now() WHERE id=$1 AND deleted_at IS NULL RETURNING id,name,profile_id,profile_version,created_at,updated_at,deleted_at`, id, name))
	if errors.Is(err, pgx.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	if err != nil {
		return Project{}, fmt.Errorf("rename project: %w", err)
	}
	return project, nil
}

func (s *Store) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,name,profile_id,profile_version,created_at,updated_at,deleted_at FROM projects WHERE deleted_at IS NULL ORDER BY created_at,id`)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()
	result := []Project{}
	for rows.Next() {
		project, err := scanProject(rows)
		if err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		result = append(result, project)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	return result, nil
}

func (s *Store) SaveInput(ctx context.Context, input InputFile) (_ InputFile, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return InputFile{}, fmt.Errorf("begin save input: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var deleted bool
	err = tx.QueryRow(ctx, `SELECT deleted_at IS NOT NULL FROM projects WHERE id=$1 FOR UPDATE`, input.ProjectID).Scan(&deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return InputFile{}, ErrNotFound
	}
	if err != nil {
		return InputFile{}, fmt.Errorf("lock input project: %w", err)
	}
	if deleted {
		return InputFile{}, ErrConflict
	}
	err = tx.QueryRow(ctx, `INSERT INTO input_files(id,project_id,display_name,media_type,size_bytes,sha256_digest,object_key) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING created_at`, input.ID, input.ProjectID, input.DisplayName, input.MediaType, input.SizeBytes, input.SHA256Digest, input.ObjectKey).Scan(&input.CreatedAt)
	if err != nil {
		return InputFile{}, fmt.Errorf("save input metadata: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return InputFile{}, fmt.Errorf("commit input metadata: %w", err)
	}
	return input, nil
}

func (s *Store) ListInputs(ctx context.Context, projectID uuid.UUID) ([]InputFile, error) {
	if _, err := s.ReadProject(ctx, projectID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id,project_id,display_name,media_type,size_bytes,sha256_digest,object_key,created_at FROM input_files WHERE project_id=$1 ORDER BY created_at,id`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list input files: %w", err)
	}
	defer rows.Close()
	result := []InputFile{}
	for rows.Next() {
		var input InputFile
		if err := rows.Scan(&input.ID, &input.ProjectID, &input.DisplayName, &input.MediaType, &input.SizeBytes, &input.SHA256Digest, &input.ObjectKey, &input.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan input file: %w", err)
		}
		result = append(result, input)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list input files: %w", err)
	}
	return result, nil
}

func (s *Store) DeleteProject(ctx context.Context, id uuid.UUID) (err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin delete project: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var deleted bool
	err = tx.QueryRow(ctx, `SELECT deleted_at IS NOT NULL FROM projects WHERE id=$1 FOR UPDATE`, id).Scan(&deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock project for delete: %w", err)
	}
	if deleted {
		return tx.Commit(ctx)
	}
	var protected bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM runs JOIN conversations ON conversations.id=runs.conversation_id WHERE conversations.project_id=$1 AND (runs.status IN ('queued','running') OR runs.finalized_at IS NULL))`, id).Scan(&protected)
	if err != nil {
		return fmt.Errorf("check project runs: %w", err)
	}
	if protected {
		return ErrConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE projects SET deleted_at=now(),updated_at=now() WHERE id=$1`, id); err != nil {
		return fmt.Errorf("delete project: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit delete project: %w", err)
	}
	return nil
}

type rowScanner interface{ Scan(...any) error }

func scanProject(row rowScanner) (Project, error) {
	var project Project
	err := row.Scan(&project.ID, &project.Name, &project.ProfileID, &project.ProfileVersion, &project.CreatedAt, &project.UpdatedAt, &project.DeletedAt)
	return project, err
}
