package sandbox

import (
	"fmt"
	"harness-forge.local/control-plane/internal/config"
)

// NewProvider is the only configuration switch. Consumers receive the same binding.
func NewProvider(c config.Config) (Binding, error) {
	id := ProviderID(c.SandboxProvider)
	if id == "" {
		id = Docker
	}
	var provider Provider
	var err error
	switch id {
	case Docker:
		provider, err = NewDockerProvider(c.RuntimeURL, c.WorkspaceRoot, nil)
	case Fake:
		root := c.FakeFixtureRoot
		if root == "" {
			root = "/app/fixtures/fake-runtime"
		}
		provider, err = NewFakeProvider(root)
	default:
		return Binding{}, fmt.Errorf("unsupported sandbox provider %q", id)
	}
	if err != nil {
		return Binding{}, err
	}
	return Binding{ID: id, Provider: provider}, nil
}
