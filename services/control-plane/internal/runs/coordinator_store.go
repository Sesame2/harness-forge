package runs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"harness-forge.local/control-plane/internal/artifacts"
	"harness-forge.local/control-plane/internal/contracts"
)

type RunContext struct {
	ProjectID                         uuid.UUID
	ProfileID, ProfileVersion, Prompt string
	ActiveSession                     *string
}

func (s *Store) LoadContext(ctx context.Context, id uuid.UUID) (RunContext, error) {
	var data RunContext
	err := s.pool.QueryRow(ctx, `SELECT p.id,p.profile_id,p.profile_version,m.content,c.active_sdk_session_id FROM runs r JOIN messages m ON m.id=r.trigger_message_id AND m.conversation_id=r.conversation_id AND m.role='user' JOIN conversations c ON c.id=r.conversation_id JOIN projects p ON p.id=c.project_id WHERE r.id=$1 AND c.deleted_at IS NULL AND p.deleted_at IS NULL`, id).Scan(&data.ProjectID, &data.ProfileID, &data.ProfileVersion, &data.Prompt, &data.ActiveSession)
	if errors.Is(err, pgx.ErrNoRows) {
		return data, ErrNotFound
	}
	return data, err
}
func (s *Store) RefreshSource(ctx context.Context, id uuid.UUID) (Run, error) {
	return scanRun(s.pool.QueryRow(ctx, `UPDATE runs r SET source_sdk_session_id=c.active_sdk_session_id,updated_at=now() FROM conversations c JOIN projects p ON p.id=c.project_id WHERE r.id=$1 AND r.conversation_id=c.id AND c.deleted_at IS NULL AND p.deleted_at IS NULL AND r.status='running' AND r.phase='preparing' AND r.finalized_at IS NULL RETURNING `+prefixedRunColumns("r"), id))
}
func prefixedRunColumns(prefix string) string {
	return prefix + "." + strings.ReplaceAll(runColumns, ",", ","+prefix+".")
}

func (s *Store) SetPhase(ctx context.Context, id uuid.UUID, phase Phase) error {
	return s.change(ctx, id, func(tx pgx.Tx) error {
		r, err := scanRun(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM runs WHERE id=$1 FOR UPDATE`, id))
		if err != nil {
			return err
		}
		if r.Status != Running || r.Phase == nil || r.FinalizedAt != nil {
			return ErrConflict
		}
		if *r.Phase != phase {
			if _, err := Advance(r, phase, time.Now()); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE runs SET phase=$2,updated_at=now() WHERE id=$1`, id, phase); err != nil {
			return err
		}
		key := "phase:" + string(phase)
		payload, _ := json.Marshal(map[string]any{"phase": phase})
		_, err = s.AppendEventTx(ctx, tx, Event{RunID: id, Type: "phase.changed", Payload: payload, OccurredAt: time.Now().UTC(), DedupeKey: &key})
		return err
	})
}

func (s *Store) RecordRuntimeEvent(ctx context.Context, id uuid.UUID, e contracts.RuntimeEvent) error {
	if e.RunID != id.String() || e.Sequence > math.MaxInt64 {
		return ErrConflict
	}
	return s.change(ctx, id, func(tx pgx.Tx) error {
		r, err := scanRun(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM runs WHERE id=$1 FOR UPDATE`, id))
		if err != nil {
			return err
		}
		// A concurrent user cancellation owns the outcome; late Runtime data must not resurrect it.
		if r.Status != Running {
			return nil
		}
		var replay bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM run_events WHERE run_id=$1 AND runtime_sequence=$2)`, id, int64(e.Sequence)).Scan(&replay); err != nil {
			return err
		}
		if replay {
			return nil
		}
		payload, err := json.Marshal(e.Payload)
		if err != nil {
			return err
		}
		seq := int64(e.Sequence)
		if p, ok := e.Payload.(contracts.PhaseChangedPayload); ok {
			phase := Phase(p.Phase)
			if r.Phase == nil {
				return ErrConflict
			}
			if phase != *r.Phase {
				if _, err := Advance(r, phase, time.Now()); err != nil {
					return err
				}
			}
			if phase != Preparing && phase != Agent {
				return ErrConflict
			}
			if _, err := tx.Exec(ctx, `UPDATE runs SET phase=$2,updated_at=now() WHERE id=$1`, id, phase); err != nil {
				return err
			}
		}
		if _, err := s.AppendEventTx(ctx, tx, Event{RunID: id, RuntimeSequence: &seq, Type: e.Type, Payload: payload, OccurredAt: e.OccurredAt}); err != nil {
			return err
		}
		if p, ok := e.Payload.(contracts.AssistantMessagePayload); ok {
			if _, err := tx.Exec(ctx, `INSERT INTO messages(id,conversation_id,role,content,run_id,runtime_sequence) VALUES($1,$2,'assistant',$3,$4,$5) ON CONFLICT(run_id,runtime_sequence) WHERE role='assistant' AND run_id IS NOT NULL AND runtime_sequence IS NOT NULL DO NOTHING`, uuid.New(), r.ConversationID, p.Text, id, seq); err != nil {
				return err
			}
		}
		if _, ok := e.Payload.(contracts.AgentFailedPayload); ok {
			_, err := tx.Exec(ctx, `UPDATE runs SET status='failed',error=$2,updated_at=now() WHERE id=$1`, id, payload)
			return err
		}
		return nil
	})
}

// RecordFailure persists an outcome before any external cleanup. Existing
// terminal outcomes are immutable, notably when cancellation wins a race.
func (s *Store) RecordFailure(ctx context.Context, id uuid.UUID, status Status, detail json.RawMessage) (Run, error) {
	if status != Failed && status != Cancelled && status != Interrupted {
		return Run{}, ErrConflict
	}
	var result Run
	err := s.change(ctx, id, func(tx pgx.Tx) error {
		r, err := scanRun(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM runs WHERE id=$1 FOR UPDATE`, id))
		if err != nil {
			return err
		}
		if terminal(r.Status) {
			result = r
			return nil
		}
		if r.Status != Running || r.FinalizedAt != nil || (status == Cancelled && r.Phase != nil && *r.Phase == Publishing) {
			return ErrConflict
		}
		result, err = scanRun(tx.QueryRow(ctx, `UPDATE runs SET status=$2,error=$3,updated_at=now() WHERE id=$1 RETURNING `+runColumns, id, status, detail))
		return err
	})
	return result, err
}

// Product visibility, active-session promotion and succeeded status share a
// transaction. Lock order matches deletion: Project, then Conversation, then Run.
func (s *Store) CommitProducts(ctx context.Context, id, projectID uuid.UUID, candidate string, records []artifacts.Artifact) error {
	if candidate == "" {
		return ErrConflict
	}
	return s.change(ctx, id, func(tx pgx.Tx) error {
		var deleted bool
		if err := tx.QueryRow(ctx, `SELECT deleted_at IS NOT NULL FROM projects WHERE id=$1 FOR UPDATE`, projectID).Scan(&deleted); err != nil {
			return err
		}
		if deleted {
			return ErrConflict
		}
		var conversationID uuid.UUID
		var active *string
		if err := tx.QueryRow(ctx, `SELECT c.id,c.deleted_at IS NOT NULL,c.active_sdk_session_id FROM conversations c JOIN runs r ON r.conversation_id=c.id WHERE r.id=$1 AND c.project_id=$2 FOR UPDATE OF c`, id, projectID).Scan(&conversationID, &deleted, &active); err != nil {
			return err
		}
		if deleted {
			return ErrConflict
		}
		r, err := scanRun(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM runs WHERE id=$1 FOR UPDATE`, id))
		if err != nil {
			return err
		}
		if r.Status != Running || r.Phase == nil || *r.Phase != Publishing || r.FinalizedAt != nil {
			return ErrConflict
		}
		if (active == nil) != (r.SourceSDKSessionID == nil) || (active != nil && *active != *r.SourceSDKSessionID) {
			return ErrConflict
		}
		for _, a := range records {
			if a.RunID != id {
				return ErrConflict
			}
		}
		if err := artifacts.NewStore(s.pool).InsertTx(ctx, tx, records); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE conversations SET active_sdk_session_id=$2,updated_at=now() WHERE id=$1`, conversationID, candidate); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE runs SET candidate_sdk_session_id=$2,status='succeeded',updated_at=now() WHERE id=$1`, id, candidate)
		return err
	})
}

// AcknowledgeFinalized is called only after authoritative Runtime finalization
// and lease release. The durable acknowledgement and all public events are atomic.
func (s *Store) AcknowledgeFinalized(ctx context.Context, id uuid.UUID) error {
	return s.change(ctx, id, func(tx pgx.Tx) error {
		r, err := scanRun(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM runs WHERE id=$1 FOR UPDATE`, id))
		if err != nil {
			return err
		}
		if !terminal(r.Status) {
			return ErrConflict
		}
		if r.FinalizedAt != nil {
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE runs SET finalized_at=now(),updated_at=now() WHERE id=$1`, id); err != nil {
			return err
		}
		now := time.Now().UTC()
		if r.Status == Succeeded {
			rows, err := tx.Query(ctx, `SELECT id,title,type,entry_path,is_primary,created_at FROM artifacts WHERE run_id=$1 ORDER BY created_at,id`, id)
			if err != nil {
				return err
			}
			var events []Event
			for rows.Next() {
				var a artifacts.Artifact
				a.RunID = id
				if err := rows.Scan(&a.ID, &a.Title, &a.Type, &a.EntryPath, &a.IsPrimary, &a.CreatedAt); err != nil {
					rows.Close()
					return err
				}
				key := "artifact:" + a.ID.String() + ":published"
				payload, _ := json.Marshal(a)
				events = append(events, Event{RunID: id, Type: "artifact.published", Payload: payload, OccurredAt: now, DedupeKey: &key})
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
			for _, e := range events {
				if _, err := s.AppendEventTx(ctx, tx, e); err != nil {
					return err
				}
			}
		}
		key := "terminal:" + string(r.Status)
		_, err = s.AppendEventTx(ctx, tx, Event{RunID: id, Type: "run." + string(r.Status), Payload: []byte(`{}`), OccurredAt: now, DedupeKey: &key})
		return err
	})
}
func (s *Store) change(ctx context.Context, id uuid.UUID, fn func(pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { cleanup, cancel := cleanupContext(); defer cancel(); _ = tx.Rollback(cleanup) }()
	if err := fn(tx); err != nil {
		return err
	}
	err = tx.Commit(ctx)
	// Wakeups are hints, not commit acknowledgements. The transaction may
	// already be durable even when its acknowledgement is lost.
	s.broker.Notify(id)
	if err != nil {
		return fmt.Errorf("commit run state: %w", err)
	}
	return nil
}
