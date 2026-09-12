package runs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"harness-forge.local/control-plane/internal/agentexec"
)

func TestExecutionAuthorityRejectsUnknownLifecycleAndDuplicates(t *testing.T) {
	id := uuid.New()
	if _, present, err := executionRecord(nil, id); err != nil || present {
		t.Fatalf("absent=%v %v", present, err)
	}
	if _, _, err := executionRecord([]agentexec.Execution{{RunID: id, Lifecycle: "unknown"}}, id); !errors.Is(err, agentexec.ErrOutcomeUnknown) {
		t.Fatal(err)
	}
	record := agentexec.Execution{RunID: id, Lifecycle: agentexec.Running}
	if _, _, err := executionRecord([]agentexec.Execution{record, record}, id); !errors.Is(err, ErrConsistency) {
		t.Fatal(err)
	}
}
func TestCleanupAttemptIsBoundedAndIndependent(t *testing.T) {
	ctx, done := cleanupContext()
	defer done()
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > 31*time.Second || time.Until(deadline) < time.Second {
		t.Fatal("unbounded cleanup")
	}
	done()
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal(ctx.Err())
	}
}
