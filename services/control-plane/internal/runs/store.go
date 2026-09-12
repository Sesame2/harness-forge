package runs

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"strings"
)

type Store struct {
	pool           *pgxpool.Pool
	afterClaimLock func()
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const runColumns = `id,conversation_id,trigger_message_id,status,phase,finalized_at,source_sdk_session_id,candidate_sdk_session_id,sandbox_provider,sandbox_ref,error,created_at,updated_at`

// CreateQueuedTx participates in the caller's Message + Run transaction.
// It never commits or rolls back that transaction.
func (s *Store) CreateQueuedTx(ctx context.Context, tx pgx.Tx, run Run) (Run, error) {
	return scanRun(tx.QueryRow(ctx, `INSERT INTO runs(id,conversation_id,trigger_message_id,status,source_sdk_session_id) VALUES($1,$2,$3,'queued',$4) RETURNING `+runColumns, run.ID, run.ConversationID, run.TriggerMessageID, run.SourceSDKSessionID))
}

func (s *Store) Read(ctx context.Context, id uuid.UUID) (Run, error) {
	return scanRun(s.pool.QueryRow(ctx, `SELECT `+runColumns+` FROM runs WHERE id=$1`, id))
}

func (s *Store) ClaimNext(ctx context.Context) (*Run, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("begin run claim: %w", err)
	}
	defer tx.Rollback(ctx)
	// ponytail: one global V0 execution slot; revisit only for multi-slot scheduling.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('harness-forge:run-claim',0))`); err != nil {
		return nil, fmt.Errorf("lock run claim: %w", err)
	}
	if s.afterClaimLock != nil {
		s.afterClaimLock()
	}
	var active bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM runs WHERE status <> 'queued' AND finalized_at IS NULL)`).Scan(&active); err != nil {
		return nil, fmt.Errorf("check active run: %w", err)
	}
	if active {
		return nil, nil
	}
	run, err := scanRun(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM runs WHERE status='queued' ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1`))
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	run, err = scanRun(tx.QueryRow(ctx, `UPDATE runs SET status='running',phase='preparing',updated_at=now() WHERE id=$1 RETURNING `+runColumns, run.ID))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit run claim: %w", err)
	}
	return &run, nil
}

// BeginSandboxAcquire durably records intent before the external Acquire RPC.
func (s *Store) BeginSandboxAcquire(ctx context.Context, id uuid.UUID, provider string) (Run, error) {
	if strings.TrimSpace(provider) == "" {
		return Run{}, ErrConflict
	}
	run, err := scanRun(s.pool.QueryRow(ctx, `UPDATE runs SET sandbox_provider=$2,updated_at=CASE WHEN sandbox_provider IS NULL THEN now() ELSE updated_at END WHERE id=$1 AND (sandbox_provider=$2 OR (sandbox_provider IS NULL AND status='running' AND phase='preparing' AND finalized_at IS NULL)) RETURNING `+runColumns, id, provider))
	if errors.Is(err, ErrNotFound) {
		return s.acquisitionConflict(ctx, id)
	}
	return run, err
}

// AttachSandboxRef may reconcile a terminal but unfinalized uncertain Acquire.
// An already attached identity remains immutable, including after finalization.
func (s *Store) AttachSandboxRef(ctx context.Context, id uuid.UUID, provider, ref string) (Run, error) {
	if strings.TrimSpace(provider) == "" || strings.TrimSpace(ref) == "" {
		return Run{}, ErrConflict
	}
	run, err := scanRun(s.pool.QueryRow(ctx, `UPDATE runs SET sandbox_ref=$3,updated_at=CASE WHEN sandbox_ref IS NULL THEN now() ELSE updated_at END WHERE id=$1 AND sandbox_provider=$2 AND (sandbox_ref=$3 OR (sandbox_ref IS NULL AND status <> 'queued' AND finalized_at IS NULL)) RETURNING `+runColumns, id, provider, ref))
	if errors.Is(err, ErrNotFound) {
		return s.acquisitionConflict(ctx, id)
	}
	return run, err
}

func (s *Store) acquisitionConflict(ctx context.Context, id uuid.UUID) (Run, error) {
	if _, err := s.Read(ctx, id); err != nil {
		return Run{}, err
	}
	return Run{}, ErrConflict
}

type rowScanner interface{ Scan(...any) error }

func scanRun(row rowScanner) (Run, error) {
	var run Run
	err := row.Scan(&run.ID, &run.ConversationID, &run.TriggerMessageID, &run.Status, &run.Phase, &run.FinalizedAt, &run.SourceSDKSessionID, &run.CandidateSDKSessionID, &run.SandboxProvider, &run.SandboxRef, &run.Error, &run.CreatedAt, &run.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, fmt.Errorf("scan run: %w", err)
	}
	return run, nil
}
