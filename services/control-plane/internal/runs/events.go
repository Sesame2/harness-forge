package runs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"strings"
	"time"
)

type Event struct {
	RunID           uuid.UUID       `json:"run_id"`
	Sequence        int64           `json:"sequence"`
	RuntimeSequence *int64          `json:"-"`
	Type            string          `json:"type"`
	Payload         json.RawMessage `json:"payload"`
	OccurredAt      time.Time       `json:"occurred_at"`
}

func validateEvent(run Run, event Event) error {
	if strings.TrimSpace(event.Type) == "" || !json.Valid(event.Payload) || !bytes.HasPrefix(bytes.TrimSpace(event.Payload), []byte("{")) || event.OccurredAt.IsZero() || (event.RuntimeSequence != nil && *event.RuntimeSequence < 0) {
		return ErrConflict
	}
	status := Status(strings.TrimPrefix(event.Type, "run."))
	if strings.HasPrefix(event.Type, "run.") && terminal(status) && (run.FinalizedAt == nil || run.Status != status) {
		return ErrConflict
	}
	return nil
}

const eventColumns = `run_id,sequence,runtime_sequence,type,payload,occurred_at`

func (s *Store) AppendEvent(ctx context.Context, event Event) (Event, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Event{}, fmt.Errorf("begin append event: %w", err)
	}
	defer tx.Rollback(ctx)
	event, err = s.AppendEventTx(ctx, tx, event)
	if err != nil {
		return Event{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Event{}, fmt.Errorf("commit event: %w", err)
	}
	s.broker.Notify(event.RunID)
	return event, nil
}

// AppendEventTx reuses the sequence/deduplication primitive inside product-state transactions.
// Caller commits the transaction and notifies the Broker only after successful commit.
func (s *Store) AppendEventTx(ctx context.Context, tx pgx.Tx, event Event) (Event, error) {
	// The Run row serializes all product and Runtime appends for this Run.
	run, err := scanRun(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM runs WHERE id=$1 FOR UPDATE`, event.RunID))
	if err != nil {
		return Event{}, err
	}
	if event.RuntimeSequence != nil {
		existing, err := scanEvent(tx.QueryRow(ctx, `SELECT `+eventColumns+` FROM run_events WHERE run_id=$1 AND runtime_sequence=$2`, event.RunID, *event.RuntimeSequence))
		if err == nil {
			return existing, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return Event{}, fmt.Errorf("read duplicate event: %w", err)
		}
	}
	if err := validateEvent(run, event); err != nil {
		return Event{}, err
	}
	if err := tx.QueryRow(ctx, `UPDATE runs SET next_event_sequence=next_event_sequence+1 WHERE id=$1 RETURNING next_event_sequence-1`, event.RunID).Scan(&event.Sequence); err != nil {
		return Event{}, fmt.Errorf("allocate event sequence: %w", err)
	}
	event, err = scanEvent(tx.QueryRow(ctx, `INSERT INTO run_events(run_id,sequence,runtime_sequence,type,payload,occurred_at) VALUES($1,$2,$3,$4,$5,$6) RETURNING `+eventColumns, event.RunID, event.Sequence, event.RuntimeSequence, event.Type, event.Payload, event.OccurredAt))
	if err != nil {
		return Event{}, fmt.Errorf("insert event: %w", err)
	}
	return event, nil
}

func (s *Store) ListEvents(ctx context.Context, id uuid.UUID, after int64) ([]Event, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+eventColumns+` FROM run_events WHERE run_id=$1 AND sequence>$2 ORDER BY sequence`, id, after)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()
	events := []Event{}
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	return events, nil
}

func scanEvent(row rowScanner) (Event, error) {
	var event Event
	err := row.Scan(&event.RunID, &event.Sequence, &event.RuntimeSequence, &event.Type, &event.Payload, &event.OccurredAt)
	return event, err
}
