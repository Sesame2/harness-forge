package conversations

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

const conversationColumns = `id,project_id,title,active_sdk_session_id,created_at,updated_at,deleted_at`

func (s *Store) CreateConversation(ctx context.Context, conversation Conversation) (Conversation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Conversation{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockProject(ctx, tx, conversation.ProjectID); err != nil {
		return Conversation{}, err
	}
	conversation, err = scanConversation(tx.QueryRow(ctx, `INSERT INTO conversations(id,project_id,title) VALUES($1,$2,$3) RETURNING `+conversationColumns, conversation.ID, conversation.ProjectID, conversation.Title))
	if err != nil {
		return Conversation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Conversation{}, err
	}
	return conversation, nil
}

func (s *Store) ListConversations(ctx context.Context, projectID uuid.UUID) ([]Conversation, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE id=$1 AND deleted_at IS NULL)`, projectID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	rows, err := s.pool.Query(ctx, `SELECT `+conversationColumns+` FROM conversations WHERE project_id=$1 AND deleted_at IS NULL ORDER BY updated_at DESC,id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Conversation{}
	for rows.Next() {
		conversation, err := scanConversation(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, conversation)
	}
	return result, rows.Err()
}

func (s *Store) ReadConversation(ctx context.Context, id uuid.UUID) (Conversation, error) {
	return scanConversation(s.pool.QueryRow(ctx, `SELECT `+conversationColumns+` FROM conversations WHERE id=$1 AND deleted_at IS NULL AND EXISTS(SELECT 1 FROM projects WHERE projects.id=conversations.project_id AND projects.deleted_at IS NULL)`, id))
}

func (s *Store) RenameConversation(ctx context.Context, id uuid.UUID, title string) (Conversation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Conversation{}, err
	}
	defer tx.Rollback(ctx)
	conversation, err := lockConversation(ctx, tx, id)
	if err != nil {
		return Conversation{}, err
	}
	if conversation.DeletedAt != nil {
		return Conversation{}, ErrConflict
	}
	conversation, err = scanConversation(tx.QueryRow(ctx, `UPDATE conversations SET title=$2,updated_at=clock_timestamp() WHERE id=$1 RETURNING `+conversationColumns, id, title))
	if err != nil {
		return Conversation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Conversation{}, err
	}
	return conversation, nil
}

func (s *Store) DeleteConversation(ctx context.Context, id uuid.UUID) error {
	// A completed delete stays idempotent even after its parent is deleted.
	var deleted bool
	err := s.pool.QueryRow(ctx, `SELECT deleted_at IS NOT NULL FROM conversations WHERE id=$1`, id).Scan(&deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if deleted {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	conversation, err := lockConversation(ctx, tx, id)
	if err != nil {
		return err
	}
	if conversation.DeletedAt != nil {
		return tx.Commit(ctx)
	}
	var protected bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM runs WHERE conversation_id=$1 AND (status IN ('queued','running') OR finalized_at IS NULL))`, id).Scan(&protected); err != nil {
		return err
	}
	if protected {
		return ErrConflict
	}
	if _, err := tx.Exec(ctx, `UPDATE conversations SET deleted_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ListMessages(ctx context.Context, id uuid.UUID) ([]Message, error) {
	if _, err := s.ReadConversation(ctx, id); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id,conversation_id,role,content,created_at FROM messages WHERE conversation_id=$1 ORDER BY created_at,id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Message{}
	for rows.Next() {
		var message Message
		if err := rows.Scan(&message.ID, &message.ConversationID, &message.Role, &message.Content, &message.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, message)
	}
	return result, rows.Err()
}

func (s *Store) createMessageTx(ctx context.Context, tx pgx.Tx, message Message) (Message, error) {
	err := tx.QueryRow(ctx, `INSERT INTO messages(id,conversation_id,role,content) VALUES($1,$2,$3,$4) RETURNING created_at`, message.ID, message.ConversationID, message.Role, message.Content).Scan(&message.CreatedAt)
	if err != nil {
		return Message{}, fmt.Errorf("insert message: %w", err)
	}
	return message, nil
}

func lockProject(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	var deleted bool
	err := tx.QueryRow(ctx, `SELECT deleted_at IS NOT NULL FROM projects WHERE id=$1 FOR UPDATE`, id).Scan(&deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock conversation project: %w", err)
	}
	if deleted {
		return ErrConflict
	}
	return nil
}

// All mutations take the Project lock before the Conversation lock, matching
// Project deletion so deleted parents cannot gain late messages or queued runs.
func lockConversation(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Conversation, error) {
	var projectID uuid.UUID
	err := tx.QueryRow(ctx, `SELECT project_id FROM conversations WHERE id=$1`, id).Scan(&projectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Conversation{}, ErrNotFound
	}
	if err != nil {
		return Conversation{}, err
	}
	if err := lockProject(ctx, tx, projectID); err != nil {
		return Conversation{}, err
	}
	return scanConversation(tx.QueryRow(ctx, `SELECT `+conversationColumns+` FROM conversations WHERE id=$1 FOR UPDATE`, id))
}

type rowScanner interface{ Scan(...any) error }

func scanConversation(row rowScanner) (Conversation, error) {
	var conversation Conversation
	err := row.Scan(&conversation.ID, &conversation.ProjectID, &conversation.Title, &conversation.ActiveSDKSessionID, &conversation.CreatedAt, &conversation.UpdatedAt, &conversation.DeletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Conversation{}, ErrNotFound
	}
	if err != nil {
		return Conversation{}, fmt.Errorf("scan conversation: %w", err)
	}
	return conversation, nil
}
