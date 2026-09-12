package sandbox

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"harness-forge.local/control-plane/internal/agentexec"
)

const dockerRef = "docker:agent-runtime"

type DockerProvider struct {
	runtime   *agentexec.HTTPExecutor
	url, root string
	client    *http.Client
}

func NewDockerProvider(runtimeURL, root string, client *http.Client) (*DockerProvider, error) {
	if !filepath.IsAbs(root) {
		return nil, &Error{Operation: "configure", Provider: Docker, Kind: ErrConflict}
	}
	runtime, err := agentexec.NewHTTPExecutor(runtimeURL, client)
	if err != nil {
		return nil, &Error{Operation: "configure", Provider: Docker, Kind: ErrConflict}
	}
	if client == nil {
		client = http.DefaultClient
	}
	healthClient := *client
	healthClient.Timeout = 10 * time.Second
	healthClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &DockerProvider{runtime: runtime, url: strings.TrimRight(runtimeURL, "/"), root: filepath.Clean(root), client: &healthClient}, nil
}
func (p *DockerProvider) Acquire(ctx context.Context, r AcquireRequest) (Lease, error) {
	return p.open(ctx, "acquire", r.RunID, r.Paths)
}
func (p *DockerProvider) Recover(ctx context.Context, r RecoverRequest) (Lease, error) {
	if r.Ref != dockerRef {
		return nil, &Error{Operation: "recover", Provider: Docker, RunID: r.RunID, Kind: ErrNotFound}
	}
	// A fixed Compose Runtime exists independently of any execution record.
	return p.open(ctx, "recover", r.RunID, r.Paths)
}
func (p *DockerProvider) open(ctx context.Context, operation string, id agentexec.RunID, paths agentexec.Paths) (Lease, error) {
	failure := func(kind error) (Lease, error) {
		return nil, &Error{Operation: operation, Provider: Docker, RunID: id, Kind: kind}
	}
	base := filepath.Join(p.root, id.String())
	expected := agentexec.Paths{Inputs: filepath.Join(base, "inputs"), Workspace: filepath.Join(base, "workspace"), Outputs: filepath.Join(base, "outputs")}
	if id == (agentexec.RunID{}) || paths != expected {
		return failure(ErrConflict)
	}
	request, err := http.NewRequestWithContext(ctx, "GET", p.url+"/health", nil)
	if err != nil {
		return failure(ErrUnavailable)
	}
	response, err := p.client.Do(request)
	if err != nil {
		return failure(ErrUnavailable)
	}
	response.Body.Close()
	// Health never creates per-Run resources, so failure is definite, not outcome-unknown.
	if response.StatusCode != 200 {
		return failure(ErrUnavailable)
	}
	runtimeBase := "/workspaces/" + id.String()
	return &localLease{ref: dockerRef, runtime: p.runtime, paths: agentexec.Paths{Inputs: runtimeBase + "/inputs", Workspace: runtimeBase + "/workspace", Outputs: runtimeBase + "/outputs"}}, nil
}
func (p *DockerProvider) List(ctx context.Context) ([]LeaseInfo, error) {
	executions, err := p.runtime.ListExecutions(ctx)
	if err != nil {
		return nil, &Error{Operation: "list", Provider: Docker, Kind: ErrUnavailable}
	}
	leases := make([]LeaseInfo, 0, len(executions))
	for _, execution := range executions {
		leases = append(leases, LeaseInfo{RunID: execution.RunID, Ref: dockerRef})
	}
	return leases, nil
}
