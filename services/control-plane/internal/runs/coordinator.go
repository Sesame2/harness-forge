package runs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"sync"
	"time"

	"github.com/google/uuid"

	"harness-forge.local/control-plane/internal/agentexec"
	"harness-forge.local/control-plane/internal/artifacts"
	"harness-forge.local/control-plane/internal/contracts"
	"harness-forge.local/control-plane/internal/profiles"
	"harness-forge.local/control-plane/internal/projects"
	"harness-forge.local/control-plane/internal/sandbox"
	"harness-forge.local/control-plane/internal/workspaces"
)

type Coordinator struct {
	store        *Store
	profiles     *profiles.Resolver
	materializer *workspaces.Materializer
	binding      sandbox.Binding
	publisher    *artifacts.Publisher
	mu           sync.Mutex
	active       map[uuid.UUID]context.CancelFunc
}

func NewCoordinator(store *Store, profiles *profiles.Resolver, materializer *workspaces.Materializer, binding sandbox.Binding, publisher *artifacts.Publisher) *Coordinator {
	return &Coordinator{store: store, profiles: profiles, materializer: materializer, binding: binding, publisher: publisher, active: map[uuid.UUID]context.CancelFunc{}}
}

// Execute is scheduler-owned. Browser and cancel-request contexts never own
// its lifetime. Cleanup uses a fresh context after the execution is stopped.
func (c *Coordinator) Execute(ctx context.Context, run Run) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	c.mu.Lock()
	if _, ok := c.active[run.ID]; ok {
		c.mu.Unlock()
		return ErrConflict
	}
	c.active[run.ID] = cancel
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.active, run.ID); c.mu.Unlock() }()
	fail := func(code string, cause error, lease sandbox.Lease, absent bool) error {
		cleanupCtx, done := cleanupContext()
		defer done()
		if _, err := c.store.RecordFailure(cleanupCtx, run.ID, Failed, failureDetail(code)); err != nil {
			return errors.Join(cause, err)
		}
		if errors.Is(cause, ErrConsistency) {
			return errors.Join(cause, c.store.markConsistency(cleanupCtx, run.ID))
		}
		if absent {
			return errors.Join(cause, c.store.AcknowledgeFinalized(cleanupCtx, run.ID))
		}
		if lease != nil {
			current, err := c.store.Read(cleanupCtx, run.ID)
			if err != nil {
				return errors.Join(cause, err)
			}
			return errors.Join(cause, NewReconciler(c.store, c.binding, c.materializer).finishLease(cleanupCtx, current, lease))
		}
		return cause
	}
	data, err := c.store.LoadContext(ctx, run.ID)
	if err != nil {
		return fail("preparation_failed", err, nil, true)
	}
	snapshot, err := c.profiles.ResolveVersion(data.ProfileID, data.ProfileVersion)
	if err != nil {
		return fail("profile_unavailable", err, nil, true)
	}
	inputs, err := projects.NewStore(c.store.pool).ListInputs(ctx, data.ProjectID)
	if err != nil {
		return fail("inputs_unavailable", err, nil, true)
	}
	for _, input := range inputs {
		if input.ProjectID != data.ProjectID {
			return fail("input_ownership", ErrConflict, nil, true)
		}
	}
	localPaths, err := c.materializer.Prepare(ctx, run.ID, snapshot, inputs)
	if err != nil {
		return fail("preparation_failed", err, nil, true)
	}
	refreshed, err := c.store.RefreshSource(ctx, run.ID)
	if err != nil {
		return fail("source_unavailable", err, nil, true)
	}
	run = refreshed
	if _, err := c.store.BeginSandboxAcquire(ctx, run.ID, string(c.binding.ID)); err != nil {
		return fail("acquire_intent_failed", err, nil, true)
	}
	lease, err := c.binding.Provider.Acquire(ctx, sandbox.AcquireRequest{RunID: run.ID, Paths: localPaths})
	if err != nil {
		return fail("acquire_failed", err, nil, !errors.Is(err, sandbox.ErrOutcomeUnknown))
	}
	if _, err := c.store.AttachSandboxRef(ctx, run.ID, string(c.binding.ID), lease.Ref()); err != nil {
		return fail("acquire_attachment_failed", err, nil, false)
	}
	current, err := c.store.Read(ctx, run.ID)
	if err != nil {
		return fail("state_unavailable", err, lease, false)
	}
	if current.Status != Running {
		return fail("cancelled", ErrConflict, lease, false)
	}
	request := buildRequest(run, data, snapshot, lease.Paths())
	events, errs := lease.Runtime().Execute(ctx, request)
	completed, err := c.pump(ctx, run.ID, events, errs)
	if err != nil {
		return fail("execution_failed", err, lease, false)
	}
	current, err = c.store.Read(ctx, run.ID)
	if err != nil {
		return fail("state_unavailable", err, lease, false)
	}
	if current.Status != Running {
		return fail("execution_failed", ErrConflict, lease, false)
	}
	if err := lease.SyncBack(ctx); err != nil {
		return fail("sync_failed", err, lease, false)
	}
	manifest, err := artifacts.ValidateManifest(localPaths.Outputs, snapshot)
	if err != nil {
		return fail("manifest_invalid", err, lease, false)
	}
	if !reflect.DeepEqual(manifest.Manifest.Artifacts, completed.Artifacts) {
		return fail("manifest_mismatch", ErrConflict, lease, false)
	}
	if err := c.store.SetPhase(ctx, run.ID, Publishing); err != nil {
		return fail("publishing_failed", err, lease, false)
	}
	publication, err := c.publisher.Prepare(ctx, data.ProjectID, run.ID, localPaths.Outputs, snapshot)
	if err != nil {
		return fail("publication_failed", err, lease, false)
	}
	err = c.store.CommitProducts(ctx, run.ID, data.ProjectID, completed.CandidateSDKSessionID, publication.Records)
	releaseErr := publication.Release()
	if err != nil {
		return fail("products_failed", errors.Join(err, releaseErr), lease, false)
	}
	// Products have committed: from this point retry commit/HEAD/release only.
	cleanupCtx, done := cleanupContext()
	defer done()
	current, err = c.store.Read(cleanupCtx, run.ID)
	if err != nil {
		return errors.Join(err, releaseErr)
	}
	return errors.Join(releaseErr, NewReconciler(c.store, c.binding, c.materializer).finishLease(cleanupCtx, current, lease))
}

func buildRequest(run Run, data RunContext, snapshot profiles.Snapshot, paths agentexec.Paths) agentexec.ExecuteRequest {
	var source *agentexec.SessionID
	if run.SourceSDKSessionID != nil {
		value := agentexec.SessionID(*run.SourceSDKSessionID)
		source = &value
	}
	return agentexec.ExecuteRequest{Version: "1", RunID: run.ID, ProjectID: data.ProjectID, ConversationID: run.ConversationID, Prompt: data.Prompt, SourceSDKSessionID: source, Paths: paths,
		Profile: agentexec.Profile{ID: snapshot.ID, Version: snapshot.Version, Digest: snapshot.Digest, Config: map[string]any{
			"system_prompt": snapshot.SystemPrompt,
			"tools":         map[string]any{"allowed": append([]string{}, snapshot.AllowedTools...), "disallowed": append([]string{}, snapshot.DisallowedTools...), "permission_mode": snapshot.PermissionMode},
			"artifacts":     map[string]any{"manifest_schema_version": snapshot.Artifacts.ManifestSchemaVersion, "allowed_types": append([]string{}, snapshot.Artifacts.AllowedTypes...), "max_file_bytes": snapshot.Artifacts.MaxFileBytes, "max_total_bytes": snapshot.Artifacts.MaxTotalBytes},
			"inputs":        map[string]any{"accepted_media_types": append([]string{}, snapshot.AcceptedInputMediaTypes...)},
		}}, Limits: agentexec.Limits{MaxTurns: snapshot.Agent.MaxTurns, MaxBudgetUSD: snapshot.Agent.MaxBudgetUSD}}
}

// Both channels are drained even after terminal/protocol/storage errors. A
// terminal event is not permission to finalize while its producer is active.
func (c *Coordinator) pump(ctx context.Context, id uuid.UUID, events <-chan agentexec.Event, errs <-chan error) (contracts.AgentCompletedPayload, error) {
	var terminalSeen bool
	var completed contracts.AgentCompletedPayload
	var candidate []contracts.ArtifactCandidate
	var candidateSeen bool
	var failure error
	var previous uint64
	var sequenceSeen bool
	for events != nil || errs != nil {
		select {
		case e, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			raw, err := json.Marshal(e)
			if err != nil {
				failure = errors.Join(failure, agentexec.ErrInvalid)
				continue
			}
			e, err = contracts.ParseRuntimeEvent(raw)
			if err != nil || e.RunID != id.String() || (sequenceSeen && e.Sequence <= previous) {
				failure = errors.Join(failure, agentexec.ErrInvalid)
				continue
			}
			previous = e.Sequence
			sequenceSeen = true
			if _, unknown := e.Payload.(map[string]any); unknown {
				slog.Debug("ignored unknown Runtime event", "run_id", id, "type", e.Type)
				continue
			}
			if terminalSeen {
				failure = errors.Join(failure, agentexec.ErrInvalid)
				continue
			}
			switch p := e.Payload.(type) {
			case contracts.ArtifactCandidatePayload:
				candidate = p.Artifacts
				candidateSeen = true
			case contracts.AgentCompletedPayload:
				terminalSeen = true
				completed = p
				if !candidateSeen || !reflect.DeepEqual(candidate, p.Artifacts) {
					failure = errors.Join(failure, agentexec.ErrInvalid)
				}
			case contracts.AgentFailedPayload:
				terminalSeen = true
				failure = errors.Join(failure, errors.New("agent failed"))
			}
			eventCtx, eventDone := cleanupContext()
			if err := c.store.RecordRuntimeEvent(eventCtx, id, e); err != nil {
				failure = errors.Join(failure, err)
			}
			eventDone()
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			if err != nil {
				var runtimeError *agentexec.RuntimeError
				if errors.As(err, &runtimeError) && errors.Is(err, agentexec.ErrFinalized) && runtimeError.Decision == agentexec.Commit {
					failure = errors.Join(failure, ErrConsistency)
				}
				failure = errors.Join(failure, err)
			}
		}
	}
	if !terminalSeen {
		failure = errors.Join(failure, fmt.Errorf("missing terminal event: %w", agentexec.ErrOutcomeUnknown))
	}
	return completed, failure
}
func failureDetail(code string) json.RawMessage {
	data, _ := json.Marshal(map[string]any{"code": code})
	return data
}

func cleanupContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}

func (c *Coordinator) Cancel(ctx context.Context, run Run) (Run, error) {
	result, err := c.store.RecordFailure(ctx, run.ID, Cancelled, failureDetail("user_cancelled"))
	if err != nil {
		// A failed acknowledgement is not evidence that COMMIT failed. Confirm
		// independently of the browser request before stopping the worker.
		confirmationCtx, done := cleanupContext()
		defer done()
		confirmed, readErr := c.store.Read(confirmationCtx, run.ID)
		if readErr != nil || confirmed.Status != Cancelled {
			return Run{}, errors.Join(err, readErr)
		}
		result = confirmed
	}
	if result.Status != Cancelled {
		return Run{}, ErrConflict
	}
	c.mu.Lock()
	cancel := c.active[run.ID]
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return result, nil
}
