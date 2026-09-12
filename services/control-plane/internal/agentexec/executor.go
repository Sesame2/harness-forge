package agentexec

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/google/uuid"
	"harness-forge.local/control-plane/internal/contracts"
)

type RunID = uuid.UUID
type SessionID string
type Event = contracts.RuntimeEvent
type Decision string

const (
	Commit Decision = "commit"
	Abort  Decision = "abort"
)

type Lifecycle string

const (
	Starting         Lifecycle = "starting"
	Running          Lifecycle = "running"
	AwaitingFinalize Lifecycle = "awaiting_finalize"
)

// Execution is the V1 reconciliation record. Runtime may include other fields.
type Execution struct {
	RunID                 RunID      `json:"run_id"`
	Lifecycle             Lifecycle  `json:"lifecycle"`
	CandidateSDKSessionID *SessionID `json:"candidate_sdk_session_id,omitempty"`
}

// Profile is the immutable resolved snapshot, not a request to re-resolve by ID.
type Profile struct {
	ID      string         `json:"id"`
	Version string         `json:"version"`
	Digest  string         `json:"digest"`
	Config  map[string]any `json:"config"`
}
type Limits struct {
	MaxTurns     int     `json:"max_turns"`
	MaxBudgetUSD float64 `json:"max_budget_usd"`
}
type ExecuteRequest struct {
	Version            string     `json:"version"`
	RunID              RunID      `json:"run_id"`
	ProjectID          uuid.UUID  `json:"project_id"`
	ConversationID     uuid.UUID  `json:"conversation_id"`
	Prompt             string     `json:"prompt"`
	SourceSDKSessionID *SessionID `json:"source_sdk_session_id"`
	Profile            Profile    `json:"profile"`
	Paths              Paths      `json:"paths"`
	Limits             Limits     `json:"limits"`
}

func (r ExecuteRequest) Validate() error {
	if r.Version != "1" || r.RunID == uuid.Nil || r.ProjectID == uuid.Nil || r.ConversationID == uuid.Nil || strings.TrimSpace(r.Prompt) == "" || !r.Paths.Valid() || r.Profile.ID == "" || r.Profile.Version == "" || r.Profile.Digest == "" || r.Profile.Config == nil || r.Limits.MaxTurns < 1 || r.Limits.MaxBudgetUSD < 0 || math.IsNaN(r.Limits.MaxBudgetUSD) || math.IsInf(r.Limits.MaxBudgetUSD, 0) || (r.SourceSDKSessionID != nil && strings.TrimSpace(string(*r.SourceSDKSessionID)) == "") {
		return ErrInvalid
	}
	return nil
}

type Executor interface {
	Execute(context.Context, ExecuteRequest) (<-chan Event, <-chan error)
	Cancel(context.Context, RunID) error
	Finalize(context.Context, RunID, Decision) error
	ListExecutions(context.Context) ([]Execution, error)
	SessionExists(context.Context, SessionID) (bool, error)
	DeleteExecution(context.Context, RunID) error
	DeleteSession(context.Context, SessionID) error
}

var (
	ErrInvalid        = errors.New("invalid runtime request")
	ErrNotFound       = errors.New("runtime record not found")
	ErrConflict       = errors.New("runtime conflict")
	ErrUnavailable    = errors.New("runtime unavailable")
	ErrOutcomeUnknown = errors.New("runtime outcome unknown")
	ErrFinalized      = errors.New("runtime execution already finalized")
)

// RuntimeError deliberately excludes response text and URLs, which can contain credentials.
type RuntimeError struct {
	Operation  string
	RunID      RunID
	StatusCode int
	Code       string
	Decision   Decision
	Kind       error
}

func (e *RuntimeError) Error() string {
	return fmt.Sprintf("runtime %s run=%s: %s", e.Operation, e.RunID, e.Kind)
}
func (e *RuntimeError) Unwrap() error { return e.Kind }
