package conversations

import (
	"errors"
	"time"

	"harness-forge.local/control-plane/internal/runs"

	"github.com/google/uuid"
)

var (
	ErrInvalid  = errors.New("invalid conversation request")
	ErrNotFound = errors.New("conversation or project not found")
	ErrConflict = errors.New("conversation or project conflicts with operation")
)

type Conversation struct {
	ID                 uuid.UUID  `json:"id"`
	ProjectID          uuid.UUID  `json:"project_id"`
	Title              string     `json:"title"`
	ActiveSDKSessionID *string    `json:"active_sdk_session_id"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	DeletedAt          *time.Time `json:"-"`
}

type Message struct {
	ID             uuid.UUID `json:"id"`
	ConversationID uuid.UUID `json:"conversation_id"`
	Role           string    `json:"role"`
	Content        string    `json:"content"`
	CreatedAt      time.Time `json:"created_at"`
}

type SubmitMessageResult struct {
	Message Message  `json:"message"`
	Run     runs.Run `json:"run"`
}
