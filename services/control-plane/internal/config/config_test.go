package config

import (
	"strings"
	"testing"
)

func TestConfigFromEnvMapsCompleteConfiguration(t *testing.T) {
	env := map[string]string{
		"HTTP_ADDR":        ":9000",
		"ARTIFACT_ADDR":    ":9001",
		"DATABASE_URL":     "postgres://user:pass@postgres/db",
		"MINIO_ENDPOINT":   "minio:9000",
		"MINIO_ACCESS_KEY": "access",
		"MINIO_SECRET_KEY": "secret",
		"MINIO_BUCKET":     "artifacts",
		"SANDBOX_PROVIDER": "fake",
		"RUNTIME_URL":      "http://agent-runtime:8090",
		"WORKSPACE_ROOT":   "/tmp/workspaces",
		"WEB_ORIGIN":       "http://localhost:5173",
	}

	got, err := ConfigFromEnv(func(key string) string { return env[key] })
	if err != nil {
		t.Fatalf("ConfigFromEnv() error = %v", err)
	}

	want := Config{
		HTTPAddr:        ":9000",
		ArtifactAddr:    ":9001",
		DatabaseURL:     "postgres://user:pass@postgres/db",
		MinIOEndpoint:   "minio:9000",
		MinIOAccessKey:  "access",
		MinIOSecretKey:  "secret",
		MinIOBucket:     "artifacts",
		SandboxProvider: "fake",
		RuntimeURL:      "",
		WorkspaceRoot:   "/tmp/workspaces",
		WebOrigin:       "http://localhost:5173",
	}
	if got != want {
		t.Fatalf("ConfigFromEnv() = %#v, want %#v", got, want)
	}
}

func TestConfigFromEnvDefaults(t *testing.T) {
	env := validEnvironment()
	delete(env, "HTTP_ADDR")
	delete(env, "ARTIFACT_ADDR")
	delete(env, "SANDBOX_PROVIDER")
	delete(env, "WORKSPACE_ROOT")

	got, err := ConfigFromEnv(func(key string) string { return env[key] })
	if err != nil {
		t.Fatalf("ConfigFromEnv() error = %v", err)
	}

	if got.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want %q", got.HTTPAddr, ":8080")
	}
	if got.ArtifactAddr != ":8081" {
		t.Errorf("ArtifactAddr = %q, want %q", got.ArtifactAddr, ":8081")
	}
	if got.SandboxProvider != "docker" {
		t.Errorf("SandboxProvider = %q, want %q", got.SandboxProvider, "docker")
	}
	if got.WorkspaceRoot != "/workspaces" {
		t.Errorf("WorkspaceRoot = %q, want %q", got.WorkspaceRoot, "/workspaces")
	}
	if got.RuntimeURL != "http://agent-runtime:8090" {
		t.Errorf("RuntimeURL = %q, want %q", got.RuntimeURL, "http://agent-runtime:8090")
	}
}

func TestConfigFromEnvRequiresRuntimeURLForDocker(t *testing.T) {
	env := validEnvironment()
	delete(env, "RUNTIME_URL")

	_, err := ConfigFromEnv(func(key string) string { return env[key] })
	assertNamedError(t, err, "RUNTIME_URL")
}

func TestConfigFromEnvRequiresSharedInfrastructure(t *testing.T) {
	for _, key := range []string{
		"DATABASE_URL",
		"MINIO_ENDPOINT",
		"MINIO_ACCESS_KEY",
		"MINIO_SECRET_KEY",
		"MINIO_BUCKET",
	} {
		t.Run(key, func(t *testing.T) {
			env := validEnvironment()
			delete(env, key)

			_, err := ConfigFromEnv(func(name string) string { return env[name] })
			assertNamedError(t, err, key)
		})
	}
}

func TestConfigFromEnvRejectsInvalidWebOrigin(t *testing.T) {
	env := validEnvironment()
	env["WEB_ORIGIN"] = "://not-a-url"

	_, err := ConfigFromEnv(func(key string) string { return env[key] })
	assertNamedError(t, err, "WEB_ORIGIN")
}

func TestConfigFromEnvRejectsUnsupportedSandboxProvider(t *testing.T) {
	env := validEnvironment()
	env["SANDBOX_PROVIDER"] = "e2b"

	_, err := ConfigFromEnv(func(key string) string { return env[key] })
	assertNamedError(t, err, "SANDBOX_PROVIDER")
}

func validEnvironment() map[string]string {
	return map[string]string{
		"DATABASE_URL":     "postgres://user:pass@postgres/db",
		"MINIO_ENDPOINT":   "minio:9000",
		"MINIO_ACCESS_KEY": "access",
		"MINIO_SECRET_KEY": "secret",
		"MINIO_BUCKET":     "artifacts",
		"RUNTIME_URL":      "http://agent-runtime:8090",
		"WEB_ORIGIN":       "http://localhost:5173",
	}
}

func assertNamedError(t *testing.T, err error, name string) {
	t.Helper()
	if err == nil {
		t.Fatalf("ConfigFromEnv() error = nil, want error naming %s", name)
	}
	if !strings.Contains(err.Error(), name) {
		t.Fatalf("ConfigFromEnv() error = %q, want it to name %s", err, name)
	}
}
