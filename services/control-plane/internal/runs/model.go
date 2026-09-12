package runs

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type Status string
type Phase string

const (
	Queued      Status = "queued"
	Running     Status = "running"
	Succeeded   Status = "succeeded"
	Failed      Status = "failed"
	Cancelled   Status = "cancelled"
	Interrupted Status = "interrupted"
	Preparing   Phase  = "preparing"
	Agent       Phase  = "agent"
	Publishing  Phase  = "publishing"
)

type Run struct {
	ID                    uuid.UUID       `json:"id"`
	ConversationID        uuid.UUID       `json:"conversation_id"`
	TriggerMessageID      uuid.UUID       `json:"trigger_message_id"`
	Status                Status          `json:"status"`
	Phase                 *Phase          `json:"phase"`
	FinalizedAt           *time.Time      `json:"finalized_at"`
	SourceSDKSessionID    *string         `json:"source_sdk_session_id"`
	CandidateSDKSessionID *string         `json:"candidate_sdk_session_id"`
	SandboxProvider       *string         `json:"-"`
	SandboxRef            *string         `json:"-"`
	Error                 json.RawMessage `json:"error"`
	CreatedAt             time.Time       `json:"created_at"`
	UpdatedAt             time.Time       `json:"updated_at"`
}

type DomainError struct{ Code, Message string }

func (e *DomainError) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

var (
	ErrNotFound = &DomainError{Code: "not_found", Message: "run not found"}
	ErrConflict = &DomainError{Code: "conflict", Message: "run state conflicts with operation"}
)
