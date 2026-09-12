package sandbox

import (
	"errors"
	"fmt"
	"harness-forge.local/control-plane/internal/agentexec"
)

var (
	ErrNotFound       = errors.New("sandbox not found")
	ErrUnavailable    = errors.New("sandbox unavailable")
	ErrConflict       = errors.New("sandbox conflict")
	ErrOutcomeUnknown = errors.New("sandbox acquire outcome unknown")
)

// Error contains correlation only, never provider URLs, response bodies or credentials.
type Error struct {
	Operation string
	Provider  ProviderID
	RunID     agentexec.RunID
	Kind      error
}

func (e *Error) Error() string {
	return fmt.Sprintf("sandbox %s provider=%s run=%s: %s", e.Operation, e.Provider, e.RunID, e.Kind)
}
func (e *Error) Unwrap() error { return e.Kind }
