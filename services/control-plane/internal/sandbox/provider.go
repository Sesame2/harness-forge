package sandbox

import (
	"context"
	"harness-forge.local/control-plane/internal/agentexec"
)

type ProviderID string

const (
	Docker ProviderID = "docker"
	Fake   ProviderID = "fake"
)

type Binding struct {
	ID       ProviderID
	Provider Provider
}
type AcquireRequest struct {
	RunID agentexec.RunID
	Paths agentexec.Paths
}
type RecoverRequest struct {
	RunID agentexec.RunID
	Ref   string
	Paths agentexec.Paths
}
type LeaseInfo struct {
	RunID agentexec.RunID
	Ref   string
}
type Provider interface {
	Acquire(context.Context, AcquireRequest) (Lease, error)
	Recover(context.Context, RecoverRequest) (Lease, error)
	List(context.Context) ([]LeaseInfo, error)
}
type Lease interface {
	Ref() string
	Runtime() agentexec.Executor
	Paths() agentexec.Paths
	SyncBack(context.Context) error
	Release(context.Context) error
}

type localLease struct {
	ref     string
	runtime agentexec.Executor
	paths   agentexec.Paths
}

func (l *localLease) Ref() string                    { return l.ref }
func (l *localLease) Runtime() agentexec.Executor    { return l.runtime }
func (l *localLease) Paths() agentexec.Paths         { return l.paths }
func (l *localLease) SyncBack(context.Context) error { return nil }
func (l *localLease) Release(context.Context) error  { return nil }
