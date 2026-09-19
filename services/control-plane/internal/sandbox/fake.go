package sandbox

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"harness-forge.local/control-plane/internal/agentexec"
	"harness-forge.local/control-plane/internal/contracts"
)

// FakeProvider owns fixture playback and in-process execution records. It is not a remote persistence substitute.
type FakeProvider struct {
	mu         sync.Mutex
	root       string
	wait       func(context.Context, time.Duration) error
	leases     map[agentexec.RunID]*fakeLease
	executions map[agentexec.RunID]*fakeExecution
	sessions   map[agentexec.SessionID]bool
}
type fakeScenario struct {
	Version         uint    `json:"version"`
	BaseFixture     string  `json:"base_fixture"`
	EventDelayMS    uint    `json:"event_delay_ms"`
	BlockBeforeType *string `json:"block_before_type"`
	Release         string  `json:"release"`
	events          []agentexec.Event
}

func fixtureName(name string) bool {
	switch name {
	case "geo-report", "success-v2", "agent-failure", "invalid-manifest", "delayed-success", "blocking":
		return true
	}
	return false
}

func waitFakeEvent(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

type fakeExecution struct {
	record   agentexec.Execution
	decision agentexec.Decision
	cancel   context.CancelFunc
	done     chan struct{}
}
type fakeExecutor struct{ provider *FakeProvider }
type fakeLease struct {
	*localLease
	provider *FakeProvider
	released bool
}

func (l *fakeLease) Release(context.Context) error {
	l.provider.mu.Lock()
	defer l.provider.mu.Unlock()
	l.released = true
	return nil
}

func NewFakeProvider(root string) (*FakeProvider, error) {
	p := &FakeProvider{root: root, wait: waitFakeEvent, leases: map[agentexec.RunID]*fakeLease{}, executions: map[agentexec.RunID]*fakeExecution{}, sessions: map[agentexec.SessionID]bool{}}
	if _, err := p.scenario("geo-report"); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *FakeProvider) scenario(name string) (*fakeScenario, error) {
	var scenario fakeScenario
	data, err := os.ReadFile(filepath.Join(p.root, name, "scenario.json"))
	if err != nil {
		return nil, &Error{Operation: "configure", Provider: Fake, Kind: ErrUnavailable}
	}
	if json.Unmarshal(data, &scenario) != nil || scenario.Version != 1 || !fixtureName(scenario.BaseFixture) || scenario.EventDelayMS > uint((1<<63-1)/int64(time.Millisecond)) ||
		!((scenario.Release == "none" && scenario.BlockBeforeType == nil) || (scenario.Release == "context_cancel" && scenario.BlockBeforeType != nil && *scenario.BlockBeforeType == "agent.completed")) {
		return nil, &Error{Operation: "configure", Provider: Fake, Kind: ErrConflict}
	}
	file, err := os.Open(filepath.Join(p.root, scenario.BaseFixture, "events.ndjson"))
	if err != nil {
		return nil, &Error{Operation: "configure", Provider: Fake, Kind: ErrUnavailable}
	}
	defer file.Close()
	var events []agentexec.Event
	scanner := bufio.NewScanner(file)
	terminal := false
	for scanner.Scan() {
		event, err := contracts.ParseRuntimeEvent(scanner.Bytes())
		if err != nil || terminal {
			return nil, &Error{Operation: "configure", Provider: Fake, Kind: ErrConflict}
		}
		terminal = contracts.IsTerminalEvent(event)
		events = append(events, event)
	}
	if scanner.Err() != nil || !terminal || contracts.ValidateRuntimeEventSequence(events) != nil {
		return nil, &Error{Operation: "configure", Provider: Fake, Kind: ErrConflict}
	}
	scenario.events = events
	return &scenario, nil
}
func (p *FakeProvider) Acquire(ctx context.Context, r AcquireRequest) (Lease, error) {
	if err := ctx.Err(); err != nil {
		return nil, &Error{Operation: "acquire", Provider: Fake, RunID: r.RunID, Kind: ErrUnavailable}
	}
	if !r.Paths.Valid() || r.RunID == (agentexec.RunID{}) {
		return nil, &Error{Operation: "acquire", Provider: Fake, RunID: r.RunID, Kind: ErrConflict}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if lease, ok := p.leases[r.RunID]; ok {
		if lease.paths != r.Paths {
			return nil, &Error{Operation: "acquire", Provider: Fake, RunID: r.RunID, Kind: ErrConflict}
		}
		return lease, nil
	}
	lease := &fakeLease{localLease: &localLease{ref: "fake:" + r.RunID.String(), paths: r.Paths, runtime: &fakeExecutor{provider: p}}, provider: p}
	p.leases[r.RunID] = lease
	return lease, nil
}
func (p *FakeProvider) Recover(ctx context.Context, r RecoverRequest) (Lease, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	lease, ok := p.leases[r.RunID]
	if !ok || lease.ref != r.Ref {
		return nil, &Error{Operation: "recover", Provider: Fake, RunID: r.RunID, Kind: ErrNotFound}
	}
	if lease.paths != r.Paths {
		return nil, &Error{Operation: "recover", Provider: Fake, RunID: r.RunID, Kind: ErrConflict}
	}
	return lease, nil
}
func (p *FakeProvider) List(ctx context.Context) ([]LeaseInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	leases := make([]LeaseInfo, 0, len(p.leases))
	for id, lease := range p.leases {
		if !lease.released {
			leases = append(leases, LeaseInfo{RunID: id, Ref: lease.ref})
		}
	}
	sort.Slice(leases, func(i, j int) bool { return leases[i].RunID.String() < leases[j].RunID.String() })
	return leases, nil
}

func (e *fakeExecutor) Execute(ctx context.Context, r agentexec.ExecuteRequest) (<-chan agentexec.Event, <-chan error) {
	events, errs := make(chan agentexec.Event), make(chan error, 1)
	p := e.provider
	go func() {
		defer close(events)
		defer close(errs)
		failure := func(kind error) { errs <- &agentexec.RuntimeError{Operation: "execute", RunID: r.RunID, Kind: kind} }
		if err := r.Validate(); err != nil {
			failure(err)
			return
		}
		name := "geo-report"
		if strings.HasPrefix(r.Prompt, "[fixture:") {
			var found bool
			name, _, found = strings.Cut(strings.TrimPrefix(r.Prompt, "[fixture:"), "]")
			if !found || !fixtureName(name) {
				failure(agentexec.ErrInvalid)
				return
			}
		}
		scenario, err := p.scenario(name)
		if err != nil {
			failure(agentexec.ErrUnavailable)
			return
		}
		p.mu.Lock()
		if old := p.executions[r.RunID]; old != nil {
			decision := old.decision
			p.mu.Unlock()
			kind := agentexec.ErrConflict
			if decision != "" {
				kind = agentexec.ErrFinalized
			}
			errs <- &agentexec.RuntimeError{Operation: "execute", RunID: r.RunID, Kind: kind, Decision: decision}
			return
		}
		lease := p.leases[r.RunID]
		if lease == nil || lease.released || lease.paths != r.Paths {
			p.mu.Unlock()
			failure(agentexec.ErrInvalid)
			return
		}
		for _, execution := range p.executions {
			if execution.decision == "" {
				p.mu.Unlock()
				failure(agentexec.ErrConflict)
				return
			}
		}
		if r.SourceSDKSessionID != nil && !p.sessions[*r.SourceSDKSessionID] {
			p.mu.Unlock()
			failure(agentexec.ErrNotFound)
			return
		}
		executionCtx, cancel := context.WithCancel(ctx)
		record := &fakeExecution{record: agentexec.Execution{RunID: r.RunID, Lifecycle: agentexec.Running}, cancel: cancel, done: make(chan struct{})}
		p.executions[r.RunID] = record
		p.mu.Unlock()
		defer func() {
			cancel()
			p.mu.Lock()
			record.record.Lifecycle = agentexec.AwaitingFinalize
			close(record.done)
			p.mu.Unlock()
		}()
		for i, fixtureEvent := range scenario.events {
			if i > 0 && scenario.EventDelayMS > 0 {
				if err := p.wait(executionCtx, time.Duration(scenario.EventDelayMS)*time.Millisecond); err != nil {
					failure(agentexec.ErrOutcomeUnknown)
					return
				}
			}
			if scenario.BlockBeforeType != nil && fixtureEvent.Type == *scenario.BlockBeforeType {
				<-executionCtx.Done()
			}
			if executionCtx.Err() != nil {
				failure(agentexec.ErrOutcomeUnknown)
				return
			}
			event := fixtureEvent
			event.RunID = r.RunID.String()
			event.OccurredAt = time.Now().UTC()
			if event.Type == "agent.completed" {
				if err := os.CopyFS(r.Paths.Outputs, os.DirFS(filepath.Join(p.root, scenario.BaseFixture, "outputs"))); err != nil {
					failure(agentexec.ErrUnavailable)
					return
				}
				payload := event.Payload.(contracts.AgentCompletedPayload)
				session := agentexec.SessionID("fake-session:" + r.RunID.String())
				payload.CandidateSDKSessionID = string(session)
				event.Payload = payload
				p.mu.Lock()
				record.record.CandidateSDKSessionID = &session
				p.mu.Unlock()
			}
			select {
			case events <- event:
			case <-executionCtx.Done():
				failure(agentexec.ErrOutcomeUnknown)
				return
			}
		}
	}()
	return events, errs
}
func (e *fakeExecutor) Cancel(ctx context.Context, id agentexec.RunID) error {
	p := e.provider
	p.mu.Lock()
	execution := p.executions[id]
	if execution == nil {
		p.mu.Unlock()
		return agentexec.ErrNotFound
	}
	execution.cancel()
	done := execution.done
	p.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return agentexec.ErrOutcomeUnknown
	}
}
func (e *fakeExecutor) Finalize(ctx context.Context, id agentexec.RunID, decision agentexec.Decision) error {
	if decision != agentexec.Commit && decision != agentexec.Abort {
		return agentexec.ErrInvalid
	}
	p := e.provider
	p.mu.Lock()
	defer p.mu.Unlock()
	execution := p.executions[id]
	if execution == nil {
		return agentexec.ErrNotFound
	}
	if execution.decision != "" {
		if execution.decision == decision {
			return nil
		}
		return agentexec.ErrConflict
	}
	if execution.record.Lifecycle != agentexec.AwaitingFinalize {
		return agentexec.ErrConflict
	}
	if decision == agentexec.Commit && execution.record.CandidateSDKSessionID == nil {
		return agentexec.ErrConflict
	}
	execution.decision = decision
	if execution.record.CandidateSDKSessionID != nil {
		session := *execution.record.CandidateSDKSessionID
		if decision == agentexec.Commit {
			p.sessions[session] = true
		} else {
			delete(p.sessions, session)
		}
	}
	return nil
}
func (e *fakeExecutor) ListExecutions(context.Context) ([]agentexec.Execution, error) {
	p := e.provider
	p.mu.Lock()
	defer p.mu.Unlock()
	records := []agentexec.Execution{}
	for _, execution := range p.executions {
		if execution.decision == "" {
			record := execution.record
			if record.CandidateSDKSessionID != nil {
				session := *record.CandidateSDKSessionID
				record.CandidateSDKSessionID = &session
			}
			records = append(records, record)
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].RunID.String() < records[j].RunID.String() })
	return records, nil
}
func (e *fakeExecutor) SessionExists(ctx context.Context, id agentexec.SessionID) (bool, error) {
	e.provider.mu.Lock()
	defer e.provider.mu.Unlock()
	return e.provider.sessions[id], nil
}
func (e *fakeExecutor) DeleteSession(ctx context.Context, id agentexec.SessionID) error {
	e.provider.mu.Lock()
	defer e.provider.mu.Unlock()
	delete(e.provider.sessions, id)
	return nil
}
func (e *fakeExecutor) DeleteExecution(ctx context.Context, id agentexec.RunID) error {
	p := e.provider
	p.mu.Lock()
	defer p.mu.Unlock()
	if execution := p.executions[id]; execution != nil && execution.decision == "" {
		return agentexec.ErrConflict
	}
	delete(p.executions, id)
	delete(p.leases, id)
	return nil
}

var _ agentexec.Executor = (*fakeExecutor)(nil)
