package conversations

import (
	"context"
	"fmt"
	"strings"

	"harness-forge.local/control-plane/internal/runs"

	"github.com/google/uuid"
)

type Service struct {
	store *Store
	runs  *runs.Store
}

func NewService(store *Store, runStore *runs.Store) *Service {
	return &Service{store: store, runs: runStore}
}

func (s *Service) CreateConversation(ctx context.Context, projectID uuid.UUID, title string) (Conversation, error) {
	return s.store.CreateConversation(ctx, Conversation{ID: uuid.New(), ProjectID: projectID, Title: strings.TrimSpace(title)})
}

func (s *Service) ListConversations(ctx context.Context, projectID uuid.UUID) ([]Conversation, error) {
	return s.store.ListConversations(ctx, projectID)
}

func (s *Service) ReadConversation(ctx context.Context, id uuid.UUID) (Conversation, error) {
	return s.store.ReadConversation(ctx, id)
}

func (s *Service) RenameConversation(ctx context.Context, id uuid.UUID, title string) (Conversation, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return Conversation{}, fmt.Errorf("%w: title is required", ErrInvalid)
	}
	return s.store.RenameConversation(ctx, id, title)
}

func (s *Service) DeleteConversation(ctx context.Context, id uuid.UUID) error {
	return s.store.DeleteConversation(ctx, id)
}

func (s *Service) ListMessages(ctx context.Context, id uuid.UUID) ([]Message, error) {
	return s.store.ListMessages(ctx, id)
}

// SubmitMessage commits the user message and its queued run as one unit.
func (s *Service) SubmitMessage(ctx context.Context, id uuid.UUID, content string) (SubmitMessageResult, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return SubmitMessageResult{}, fmt.Errorf("%w: content is required", ErrInvalid)
	}
	tx, err := s.store.pool.Begin(ctx)
	if err != nil {
		return SubmitMessageResult{}, fmt.Errorf("begin submit message: %w", err)
	}
	defer tx.Rollback(ctx)
	conversation, err := lockConversation(ctx, tx, id)
	if err != nil {
		return SubmitMessageResult{}, err
	}
	if conversation.DeletedAt != nil {
		return SubmitMessageResult{}, ErrConflict
	}
	var first bool
	if err := tx.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM messages WHERE conversation_id=$1)`, id).Scan(&first); err != nil {
		return SubmitMessageResult{}, fmt.Errorf("check first message: %w", err)
	}
	message, err := s.store.createMessageTx(ctx, tx, Message{ID: uuid.New(), ConversationID: id, Role: "user", Content: content})
	if err != nil {
		return SubmitMessageResult{}, err
	}
	run, err := s.runs.CreateQueuedTx(ctx, tx, runs.Run{ID: uuid.New(), ConversationID: id, TriggerMessageID: message.ID, SourceSDKSessionID: conversation.ActiveSDKSessionID})
	if err != nil {
		return SubmitMessageResult{}, err
	}
	title := conversation.Title
	if first && title == "" {
		title = messageTitle(content)
	}
	if _, err := tx.Exec(ctx, `UPDATE conversations SET title=$2,updated_at=clock_timestamp() WHERE id=$1`, id, title); err != nil {
		return SubmitMessageResult{}, fmt.Errorf("update conversation activity: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return SubmitMessageResult{}, fmt.Errorf("commit submit message: %w", err)
	}
	return SubmitMessageResult{Message: message, Run: run}, nil
}

func messageTitle(content string) string {
	title := []rune(strings.Join(strings.Fields(content), " "))
	if len(title) > 40 {
		title = title[:40]
	}
	return string(title)
}
