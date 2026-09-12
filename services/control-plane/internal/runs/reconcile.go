package runs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"harness-forge.local/control-plane/internal/agentexec"
	"harness-forge.local/control-plane/internal/sandbox"
	"harness-forge.local/control-plane/internal/workspaces"
)

var ErrConsistency = errors.New("run/provider consistency error; scheduler paused, manual inspection required")

type Reconciler struct {
	store        *Store
	binding      sandbox.Binding
	materializer *workspaces.Materializer
}

func NewReconciler(store *Store, binding sandbox.Binding, materializer *workspaces.Materializer) *Reconciler {
	return &Reconciler{store, binding, materializer}
}

func (r *Reconciler) Reconcile(ctx context.Context, terminalOnly bool) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// This preflight includes finalized rows and intent-only rows, before List or
	// any other provider access. Changing configuration cannot change ownership.
	var mismatch bool
	if err := r.store.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM runs WHERE sandbox_provider IS NOT NULL AND sandbox_provider<>$1)`, string(r.binding.ID)).Scan(&mismatch); err != nil {
		return err
	}
	if mismatch {
		return fmt.Errorf("retained run requires its original sandbox provider: %w", ErrConsistency)
	}
	listed, err := r.binding.Provider.List(ctx)
	if err != nil {
		return err
	}
	resources := map[uuid.UUID]sandbox.LeaseInfo{}
	for _, info := range listed {
		if info.RunID == uuid.Nil || info.Ref == "" {
			return ErrConsistency
		}
		if _, exists := resources[info.RunID]; exists {
			return ErrConsistency
		}
		resources[info.RunID] = info
	}
	rows, err := r.store.pool.Query(ctx, `SELECT `+runColumns+` FROM runs ORDER BY created_at,id`)
	if err != nil {
		return err
	}
	var retained []Run
	known := map[uuid.UUID]bool{}
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			rows.Close()
			return err
		}
		retained = append(retained, run)
		known[run.ID] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	var result error
	for _, run := range retained {
		if run.FinalizedAt != nil || run.Status == Queued || (terminalOnly && !terminal(run.Status)) {
			continue
		}
		if cleanupConflict(run) {
			return ErrConsistency
		}
		info, present := resources[run.ID]
		if run.SandboxRef == nil && present {
			if run.SandboxProvider == nil {
				return ErrConsistency
			}
			run, err = r.store.AttachSandboxRef(ctx, run.ID, string(r.binding.ID), info.Ref)
			if err != nil {
				return err
			}
		}
		if run.SandboxRef == nil {
			if run.Status == Succeeded {
				return ErrConsistency
			}
			if err := r.finalizeAbsent(ctx, run); err != nil {
				result = errors.Join(result, err)
			}
			continue
		}
		if present && info.Ref != *run.SandboxRef {
			return ErrConsistency
		}
		lease, err := r.binding.Provider.Recover(ctx, sandbox.RecoverRequest{RunID: run.ID, Ref: *run.SandboxRef, Paths: r.materializer.Paths(run.ID)})
		if err != nil {
			if errors.Is(err, sandbox.ErrNotFound) && !present && run.Status != Succeeded {
				err = r.finalizeAbsent(ctx, run)
			}
			result = errors.Join(result, err)
			continue
		}
		if err := r.finishLease(ctx, run, lease); err != nil {
			if errors.Is(err, ErrConsistency) {
				return err
			}
			result = errors.Join(result, err)
		}
	}
	// Orphans are handled last and only when no retained Run owns the lease.
	for _, info := range listed {
		if known[info.RunID] {
			continue
		}
		lease, err := r.binding.Provider.Recover(ctx, sandbox.RecoverRequest{RunID: info.RunID, Ref: info.Ref, Paths: r.materializer.Paths(info.RunID)})
		if err != nil {
			result = errors.Join(result, err)
			continue
		}
		if err := r.abortLease(ctx, Run{ID: info.RunID}, lease, false); err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}
func (r *Reconciler) finishLease(ctx context.Context, run Run, lease sandbox.Lease) error {
	if cleanupConflict(run) {
		return ErrConsistency
	}
	if run.Status == Succeeded {
		var promoted bool
		if err := r.store.pool.QueryRow(ctx, `SELECT COALESCE(r.candidate_sdk_session_id IS NOT NULL AND r.candidate_sdk_session_id=c.active_sdk_session_id,false) FROM runs r JOIN conversations c ON c.id=r.conversation_id WHERE r.id=$1`, run.ID).Scan(&promoted); err != nil {
			return err
		}
		if !promoted {
			return errors.Join(ErrConsistency, r.abortLease(ctx, run, lease, true))
		}
		if err := lease.Runtime().Finalize(ctx, run.ID, agentexec.Commit); err != nil {
			return finalizationError(err)
		}
		exists, err := lease.Runtime().SessionExists(ctx, agentexec.SessionID(*run.CandidateSDKSessionID))
		if err != nil {
			return err
		}
		if !exists {
			return agentexec.ErrUnavailable
		}
		if err := lease.Release(ctx); err != nil {
			return err
		}
		return r.store.AcknowledgeFinalized(ctx, run.ID)
	}
	if run.Status != Running && run.Status != Failed && run.Status != Cancelled && run.Status != Interrupted {
		return ErrConsistency
	}
	if err := r.abortLease(ctx, run, lease, true); err != nil {
		return err
	}
	return r.store.AcknowledgeFinalized(ctx, run.ID)
}

func cleanupConflict(run Run) bool {
	var detail struct {
		CleanupConsistency bool `json:"cleanup_consistency"`
	}
	_ = json.Unmarshal(run.Error, &detail)
	return detail.CleanupConsistency
}

func (r *Reconciler) finalizeAbsent(ctx context.Context, run Run) error {
	if run.Status == Running {
		if _, err := r.store.RecordFailure(ctx, run.ID, Interrupted, failureDetail("process_interrupted")); err != nil {
			return err
		}
	}
	return r.store.AcknowledgeFinalized(ctx, run.ID)
}
func (r *Reconciler) abortLease(ctx context.Context, run Run, lease sandbox.Lease, persistDiagnostic bool) error {
	records, err := lease.Runtime().ListExecutions(ctx)
	if err != nil {
		return err
	}
	record, present, err := executionRecord(records, run.ID)
	if err != nil {
		return err
	}
	if run.Status == Running && persistDiagnostic {
		if _, err := r.store.RecordFailure(ctx, run.ID, Interrupted, failureDetail("process_interrupted")); err != nil {
			return err
		}
	}
	if present && (record.Lifecycle == agentexec.Starting || record.Lifecycle == agentexec.Running) {
		if err := lease.Runtime().Cancel(ctx, run.ID); err != nil && !errors.Is(err, agentexec.ErrNotFound) {
			return err
		}
		records, err = lease.Runtime().ListExecutions(ctx)
		if err != nil {
			return err
		}
		confirmed, exists, err := executionRecord(records, run.ID)
		if err != nil {
			return err
		}
		if exists && confirmed.Lifecycle != agentexec.AwaitingFinalize {
			return agentexec.ErrOutcomeUnknown
		}
	}
	diagnosticCtx, diagnosticDone := context.WithTimeout(ctx, 5*time.Second)
	diagnosticErr := lease.SyncBack(diagnosticCtx)
	diagnosticDone()
	if diagnosticErr != nil && persistDiagnostic {
		// Diagnostic failure must not overwrite the original outcome or prevent cleanup.
		_, _ = r.store.pool.Exec(ctx, `UPDATE runs SET error=jsonb_set(COALESCE(error,'{}'::jsonb),'{diagnostic_sync}','{"code":"sync_failed"}'::jsonb),updated_at=now() WHERE id=$1`, run.ID)
	}
	if present {
		if err := lease.Runtime().Finalize(ctx, run.ID, agentexec.Abort); err != nil {
			if errors.Is(err, agentexec.ErrConflict) && persistDiagnostic {
				return errors.Join(finalizationError(err), r.store.markConsistency(ctx, run.ID))
			}
			return finalizationError(err)
		}
	}
	return lease.Release(ctx)
}

func (s *Store) markConsistency(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `UPDATE runs SET error=jsonb_set(COALESCE(error,'{}'::jsonb),'{cleanup_consistency}','true'::jsonb),updated_at=now() WHERE id=$1`, id)
	return err
}
func executionRecord(records []agentexec.Execution, id uuid.UUID) (agentexec.Execution, bool, error) {
	var result agentexec.Execution
	present := false
	for _, record := range records {
		if record.RunID != id {
			continue
		}
		if present {
			return result, false, ErrConsistency
		}
		if record.Lifecycle != agentexec.Starting && record.Lifecycle != agentexec.Running && record.Lifecycle != agentexec.AwaitingFinalize {
			return result, false, agentexec.ErrOutcomeUnknown
		}
		result = record
		present = true
	}
	return result, present, nil
}
func finalizationError(err error) error {
	if errors.Is(err, agentexec.ErrConflict) {
		return errors.Join(ErrConsistency, err)
	}
	return err
}
