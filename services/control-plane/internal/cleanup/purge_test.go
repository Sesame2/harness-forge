package cleanup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"harness-forge.local/control-plane/internal/agentexec"
	"harness-forge.local/control-plane/internal/objectstore"
	"harness-forge.local/control-plane/internal/sandbox"
)

func TestPurgeCleansExternalResourcesInOrderBeforeHardDelete(t *testing.T) {
	ctx := context.Background()
	runID := uuid.New()
	session := agentexec.SessionID("session-1")
	providerID, ref := "fake", "fake:"+runID.String()
	log := []string{}
	objects := &recordingObjects{Memory: objectstore.NewMemory(), log: &log}
	projectID := uuid.New()
	prefix := "projects/" + projectID.String() + "/artifacts/" + uuid.NewString() + "/"
	if err := objects.Put(ctx, prefix+"index.html", strings.NewReader("ok"), objectstore.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	objects.log = &log
	executor := &recordingExecutor{log: &log}
	lease := &recordingLease{log: &log, executor: executor, ref: ref}
	catalog := &recordingCatalog{log: &log}
	p := Purger{
		catalog: catalog,
		objects: objects,
		binding: sandbox.Binding{ID: sandbox.Fake, Provider: &recordingProvider{log: &log, lease: lease}},
		removeWorkspace: func(context.Context, uuid.UUID) error {
			log = append(log, "workspace")
			return nil
		},
	}
	root := purgeRoot{kind: conversationRoot, id: uuid.New(), projectID: projectID, prefixes: []string{prefix}, runs: []purgeRun{{id: runID, provider: &providerID, ref: &ref, candidate: &session}}}

	if err := p.purgeRoot(ctx, root); err != nil {
		t.Fatal(err)
	}
	want := []string{"prefix", "recover", "session", "workspace", "execution", "release", "hard-delete"}
	if strings.Join(log, ",") != strings.Join(want, ",") {
		t.Fatalf("cleanup order = %v, want %v", log, want)
	}
}

func TestPurgeProviderMismatchDoesNotDeleteAnythingInRoot(t *testing.T) {
	providerID, ref := "docker", "docker:agent-runtime"
	projectID := uuid.New()
	log := []string{}
	p := Purger{
		catalog: &recordingCatalog{log: &log},
		objects: &recordingObjects{Memory: objectstore.NewMemory(), log: &log},
		binding: sandbox.Binding{ID: sandbox.Fake, Provider: &recordingProvider{log: &log}},
		removeWorkspace: func(context.Context, uuid.UUID) error {
			log = append(log, "workspace")
			return nil
		},
	}
	root := purgeRoot{kind: projectRoot, id: projectID, projectID: projectID, prefixes: []string{"projects/" + projectID.String() + "/artifacts/" + uuid.NewString() + "/"}, runs: []purgeRun{{id: uuid.New(), provider: &providerID, ref: &ref}}}

	if err := p.purgeRoot(context.Background(), root); err == nil || !strings.Contains(err.Error(), "recorded provider") {
		t.Fatalf("purge mismatch error = %v", err)
	}
	if len(log) != 0 {
		t.Fatalf("provider mismatch performed cleanup: %v", log)
	}
}

func TestPurgeRejectsMalformedInputObjectKeyBeforeDeleting(t *testing.T) {
	projectID := uuid.New()
	log := []string{}
	p := Purger{catalog: &recordingCatalog{log: &log}, objects: &recordingObjects{Memory: objectstore.NewMemory(), log: &log}, binding: sandbox.Binding{ID: sandbox.Fake, Provider: &recordingProvider{}}, removeWorkspace: func(context.Context, uuid.UUID) error { return nil }}
	root := purgeRoot{kind: projectRoot, id: projectID, projectID: projectID, keys: []string{"projects/" + projectID.String() + "/inputs/not-a-uuid/shared.txt"}}
	if err := p.purgeRoot(context.Background(), root); err == nil || !strings.Contains(err.Error(), "unsafe input") {
		t.Fatalf("malformed input key error = %v", err)
	}
	if len(log) != 0 {
		t.Fatalf("malformed input key performed cleanup: %v", log)
	}
}

func TestConversationPurgeRetryNeverDeletesSharedProjectInput(t *testing.T) {
	ctx := context.Background()
	projectID, runID := uuid.New(), uuid.New()
	artifactPrefix := "projects/" + projectID.String() + "/artifacts/" + uuid.NewString() + "/"
	inputKey := "projects/" + projectID.String() + "/inputs/" + uuid.NewString()
	objects := objectstore.NewMemory()
	for key, body := range map[string]string{artifactPrefix + "index.html": "artifact", inputKey: "shared"} {
		if err := objects.Put(ctx, key, strings.NewReader(body), objectstore.PutOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	providerID, ref := "fake", "fake:"+runID.String()
	executor := &recordingExecutor{sessionErr: errors.New("runtime unavailable")}
	lease := &recordingLease{executor: executor, ref: ref}
	p := Purger{
		catalog: &recordingCatalog{}, objects: objects,
		binding:         sandbox.Binding{ID: sandbox.Fake, Provider: &recordingProvider{lease: lease}},
		removeWorkspace: func(context.Context, uuid.UUID) error { return nil },
	}
	session := agentexec.SessionID("session")
	root := purgeRoot{kind: conversationRoot, id: uuid.New(), projectID: projectID, prefixes: []string{artifactPrefix}, runs: []purgeRun{{id: runID, provider: &providerID, ref: &ref, candidate: &session}}}
	if err := p.purgeRoot(ctx, root); err == nil {
		t.Fatal("first purge succeeded despite runtime failure")
	}
	executor.sessionErr = nil
	if err := p.purgeRoot(ctx, root); err != nil {
		t.Fatal(err)
	}
	reader, err := objects.Open(ctx, inputKey)
	if err != nil {
		t.Fatalf("shared input removed: %v", err)
	}
	reader.Close()
}

func TestPurgeDoesNotReleaseLeaseAfterRuntimeCleanupFailure(t *testing.T) {
	projectID, runID := uuid.New(), uuid.New()
	providerID, ref := "fake", "fake:"+runID.String()
	session := agentexec.SessionID("session")
	log := []string{}
	p := Purger{
		catalog:         &recordingCatalog{},
		objects:         objectstore.NewMemory(),
		binding:         sandbox.Binding{ID: sandbox.Fake, Provider: &recordingProvider{lease: &recordingLease{log: &log, executor: &recordingExecutor{log: &log, sessionErr: errors.New("unavailable")}, ref: ref}}},
		removeWorkspace: func(context.Context, uuid.UUID) error { log = append(log, "workspace"); return nil },
	}
	root := purgeRoot{kind: conversationRoot, id: uuid.New(), projectID: projectID, runs: []purgeRun{{id: runID, provider: &providerID, ref: &ref, candidate: &session}}}
	if err := p.purgeRoot(context.Background(), root); err == nil {
		t.Fatal("purge succeeded despite session cleanup failure")
	}
	if strings.Join(log, ",") != "session" {
		t.Fatalf("cleanup continued after failure: %v", log)
	}
}

func TestPurgeTreatsTypedNotFoundAsIdempotentAbsence(t *testing.T) {
	for _, failure := range []string{"session", "execution", "release"} {
		t.Run(failure, func(t *testing.T) {
			projectID, runID := uuid.New(), uuid.New()
			providerID, ref := "fake", "fake:"+runID.String()
			session := agentexec.SessionID("session")
			log := []string{}
			executor := &recordingExecutor{log: &log}
			lease := &recordingLease{log: &log, executor: executor, ref: ref}
			switch failure {
			case "session":
				executor.sessionErr = &agentexec.RuntimeError{Operation: "delete session", RunID: runID, Kind: agentexec.ErrNotFound}
			case "execution":
				executor.executionErr = &agentexec.RuntimeError{Operation: "delete execution", RunID: runID, Kind: agentexec.ErrNotFound}
			case "release":
				lease.err = &sandbox.Error{Operation: "release", Provider: sandbox.Fake, RunID: runID, Kind: sandbox.ErrNotFound}
			}
			p := Purger{
				catalog:         &recordingCatalog{log: &log},
				objects:         objectstore.NewMemory(),
				binding:         sandbox.Binding{ID: sandbox.Fake, Provider: &recordingProvider{log: &log, lease: lease}},
				removeWorkspace: func(context.Context, uuid.UUID) error { log = append(log, "workspace"); return nil },
			}
			root := purgeRoot{kind: conversationRoot, id: uuid.New(), projectID: projectID, runs: []purgeRun{{id: runID, provider: &providerID, ref: &ref, candidate: &session}}}

			if err := p.purgeRoot(context.Background(), root); err != nil {
				t.Fatal(err)
			}
			want := "recover,session,workspace,execution,release,hard-delete"
			if got := strings.Join(log, ","); got != want {
				t.Fatalf("cleanup order = %s, want %s", got, want)
			}
		})
	}
}

func TestPurgeSessionOwnershipIgnoresQueuedSourceReference(t *testing.T) {
	projectID, firstRun, queuedRun := uuid.New(), uuid.New(), uuid.New()
	providerID, ref := "fake", "fake:"+firstRun.String()
	session := agentexec.SessionID("owned-session")
	executor := &recordingExecutor{}
	p := Purger{
		catalog: &recordingCatalog{}, objects: objectstore.NewMemory(),
		binding:         sandbox.Binding{ID: sandbox.Fake, Provider: &recordingProvider{lease: &recordingLease{executor: executor, ref: ref}}},
		removeWorkspace: func(context.Context, uuid.UUID) error { return nil },
	}
	root := purgeRoot{kind: conversationRoot, id: uuid.New(), projectID: projectID, runs: []purgeRun{
		{id: firstRun, provider: &providerID, ref: &ref, candidate: &session},
		{id: queuedRun}, // A queued cancellation may reference session as source in DB, but owns nothing.
	}}
	if err := p.purgeRoot(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if executor.deletedSessions != 1 {
		t.Fatalf("deleted sessions = %d, want 1", executor.deletedSessions)
	}
}

func TestMaintenanceProviderMismatchPreflightRunsBeforeOrphanScanner(t *testing.T) {
	projectID, runID := uuid.New(), uuid.New()
	providerID, ref := "docker", "docker:agent-runtime"
	root := purgeRoot{kind: projectRoot, id: projectID, projectID: projectID, runs: []purgeRun{{id: runID, provider: &providerID, ref: &ref}}}
	objects := objectstore.NewMemory()
	orphan := "projects/" + projectID.String() + "/artifacts/" + uuid.NewString() + "/"
	if err := objects.Put(context.Background(), orphan+"index.html", strings.NewReader("ok"), objectstore.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	purger := &Purger{catalog: &recordingCatalog{rootsValue: []purgeRoot{root}}, objects: objects, binding: sandbox.Binding{ID: sandbox.Fake, Provider: &recordingProvider{}}, removeWorkspace: func(context.Context, uuid.UUID) error { return nil }}
	scannerEntered := false
	scanner := &OrphanScanner{objects: objects, acquire: func(context.Context) (func() error, error) {
		scannerEntered = true
		return func() error { return nil }, nil
	}, hasMetadata: func(context.Context, uuid.UUID, string) (bool, error) { return false, nil }}

	if _, err := RunMaintenance(context.Background(), purger, scanner, true); err == nil {
		t.Fatal("maintenance succeeded despite provider mismatch")
	}
	if scannerEntered {
		t.Fatal("orphan scanner entered before provider preflight")
	}
	if _, err := objects.Stat(context.Background(), orphan+"index.html"); err != nil {
		t.Fatalf("provider mismatch deleted orphan data: %v", err)
	}
}

func TestWorkspaceRemovalUnsealsInputsAndDoesNotFollowSymlinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	runID := uuid.New()
	runRoot := filepath.Join(root, runID.String())
	inputs := filepath.Join(runRoot, "inputs")
	workspace := filepath.Join(runRoot, "workspace")
	if err := os.MkdirAll(inputs, 0770); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workspace, 0770); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inputs, "shared.txt"), []byte("x"), 0440); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(inputs, 0550); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(outside, "keep.txt")
	if err := os.WriteFile(outsideFile, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace, "outside")); err != nil {
		t.Fatal(err)
	}
	removal := &workspaceRemoval{root: root}
	if err := removal.Remove(context.Background(), runID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(runRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("run workspace remains: %v", err)
	}
	if _, err := os.Stat(outsideFile); err != nil {
		t.Fatalf("workspace removal followed symlink: %v", err)
	}
	if err := removal.Remove(context.Background(), runID); err != nil {
		t.Fatalf("idempotent workspace removal: %v", err)
	}
}

func TestWorkspaceRemovalRejectsSymlinkRunRoot(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	runID := uuid.New()
	if err := os.Symlink(outside, filepath.Join(root, runID.String())); err != nil {
		t.Fatal(err)
	}
	if err := (&workspaceRemoval{root: root}).Remove(context.Background(), runID); err == nil {
		t.Fatal("symlink run root was removed")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("symlink target changed: %v", err)
	}
}

type recordingCatalog struct {
	log        *[]string
	hardErr    error
	hardCount  int
	rootsValue []purgeRoot
}

func (c *recordingCatalog) roots(context.Context) ([]purgeRoot, error) { return c.rootsValue, nil }
func (c *recordingCatalog) hardDelete(_ context.Context, _ purgeRoot) error {
	c.hardCount++
	if c.log != nil {
		*c.log = append(*c.log, "hard-delete")
	}
	return c.hardErr
}

type recordingObjects struct {
	*objectstore.Memory
	log *[]string
}

func (s *recordingObjects) DeletePrefix(ctx context.Context, prefix string) error {
	if s.log != nil {
		*s.log = append(*s.log, "prefix")
	}
	return s.Memory.DeletePrefix(ctx, prefix)
}

type recordingProvider struct {
	log   *[]string
	lease sandbox.Lease
	err   error
}

func (p *recordingProvider) Acquire(context.Context, sandbox.AcquireRequest) (sandbox.Lease, error) {
	panic("unexpected Acquire")
}
func (p *recordingProvider) Recover(context.Context, sandbox.RecoverRequest) (sandbox.Lease, error) {
	if p.log != nil {
		*p.log = append(*p.log, "recover")
	}
	return p.lease, p.err
}
func (p *recordingProvider) List(context.Context) ([]sandbox.LeaseInfo, error) { return nil, nil }

type recordingLease struct {
	log      *[]string
	executor agentexec.Executor
	ref      string
	err      error
}

func (l *recordingLease) Ref() string                    { return l.ref }
func (l *recordingLease) Runtime() agentexec.Executor    { return l.executor }
func (l *recordingLease) Paths() agentexec.Paths         { return agentexec.Paths{} }
func (l *recordingLease) SyncBack(context.Context) error { return nil }
func (l *recordingLease) Release(context.Context) error {
	if l.log != nil {
		*l.log = append(*l.log, "release")
	}
	return l.err
}

type recordingExecutor struct {
	log             *[]string
	sessionErr      error
	executionErr    error
	deletedSessions int
}

func (*recordingExecutor) Execute(context.Context, agentexec.ExecuteRequest) (<-chan agentexec.Event, <-chan error) {
	panic("unexpected Execute")
}
func (*recordingExecutor) Cancel(context.Context, agentexec.RunID) error { panic("unexpected Cancel") }
func (*recordingExecutor) Finalize(context.Context, agentexec.RunID, agentexec.Decision) error {
	panic("unexpected Finalize")
}
func (*recordingExecutor) ListExecutions(context.Context) ([]agentexec.Execution, error) {
	panic("unexpected ListExecutions")
}
func (*recordingExecutor) SessionExists(context.Context, agentexec.SessionID) (bool, error) {
	panic("unexpected SessionExists")
}
func (e *recordingExecutor) DeleteExecution(context.Context, agentexec.RunID) error {
	if e.log != nil {
		*e.log = append(*e.log, "execution")
	}
	return e.executionErr
}
func (e *recordingExecutor) DeleteSession(context.Context, agentexec.SessionID) error {
	e.deletedSessions++
	if e.log != nil {
		*e.log = append(*e.log, "session")
	}
	return e.sessionErr
}

var _ objectstore.Store = (*recordingObjects)(nil)
