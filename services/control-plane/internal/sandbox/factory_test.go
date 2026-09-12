package sandbox

import (
	"harness-forge.local/control-plane/internal/config"
	"path/filepath"
	"strings"
	"testing"
)

func TestProviderFactory(t *testing.T) {
	for _, id := range []string{"", "docker", "fake", "e2b"} {
		t.Run(id, func(t *testing.T) {
			binding, err := NewProvider(config.Config{SandboxProvider: id, RuntimeURL: "http://runtime:8090", WorkspaceRoot: "/workspaces", FakeFixtureRoot: filepath.Join("..", "..", "..", "..", "tests", "fixtures", "fake-runtime")})
			if id == "e2b" {
				if err == nil || !strings.Contains(err.Error(), "unsupported sandbox provider") {
					t.Fatalf("%v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := id
			if want == "" {
				want = "docker"
			}
			if string(binding.ID) != want || binding.Provider == nil {
				t.Fatalf("binding=%#v", binding)
			}
		})
	}
	if _, err := NewProvider(config.Config{SandboxProvider: "docker", WorkspaceRoot: "/workspaces"}); err == nil {
		t.Fatal("missing runtime URL accepted")
	}
	if _, err := NewProvider(config.Config{SandboxProvider: "fake", FakeFixtureRoot: filepath.Join("..", "..", "..", "..", "tests", "fixtures", "fake-runtime")}); err != nil {
		t.Fatal(err)
	}
}
