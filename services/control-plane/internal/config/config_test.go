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
		"PROFILE_ROOT":     "/tmp/profiles",
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
		ProfileRoot:     "/tmp/profiles",
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
		"PROFILE_ROOT",
	} {
		t.Run(key, func(t *testing.T) {
			env := validEnvironment()
			delete(env, key)

			_, err := ConfigFromEnv(func(name string) string { return env[name] })
			assertNamedError(t, err, key)
		})
	}
}

func TestConfigFromEnvRejectsBlankSharedInfrastructure(t *testing.T) {
	for _, key := range []string{
		"DATABASE_URL",
		"MINIO_ENDPOINT",
		"MINIO_ACCESS_KEY",
		"MINIO_SECRET_KEY",
		"MINIO_BUCKET",
		"PROFILE_ROOT",
	} {
		t.Run(key, func(t *testing.T) {
			env := validEnvironment()
			env[key] = " \t\n"

			_, err := ConfigFromEnv(func(name string) string { return env[name] })
			assertNamedError(t, err, key)
		})
	}
}

func TestConfigFromEnvRejectsBlankRuntimeURLForDocker(t *testing.T) {
	env := validEnvironment()
	env["RUNTIME_URL"] = " \t\n"

	_, err := ConfigFromEnv(func(key string) string { return env[key] })
	assertNamedError(t, err, "RUNTIME_URL")
}

func TestConfigFromEnvRejectsInvalidRuntimeURLForDocker(t *testing.T) {
	for name, runtimeURL := range map[string]string{
		"relative":     "/runtime",
		"no scheme":    "agent-runtime/runtime",
		"non-http":     "ftp://agent-runtime:8090",
		"missing host": "http:///runtime",
	} {
		t.Run(name, func(t *testing.T) {
			env := validEnvironment()
			env["RUNTIME_URL"] = runtimeURL

			_, err := ConfigFromEnv(func(key string) string { return env[key] })
			assertNamedError(t, err, "RUNTIME_URL")
		})
	}
}

func TestConfigFromEnvPreservesRequiredValues(t *testing.T) {
	env := validEnvironment()
	env["DATABASE_URL"] = " postgres://user:pass@postgres/db "
	env["MINIO_ENDPOINT"] = " minio:9000 "
	env["MINIO_ACCESS_KEY"] = " access "
	env["MINIO_SECRET_KEY"] = " secret "
	env["MINIO_BUCKET"] = " artifacts "
	env["RUNTIME_URL"] = "https://agent-runtime.example.test/run?mode=docker"

	got, err := ConfigFromEnv(func(key string) string { return env[key] })
	if err != nil {
		t.Fatalf("ConfigFromEnv() error = %v", err)
	}

	if got.DatabaseURL != env["DATABASE_URL"] {
		t.Errorf("DatabaseURL = %q, want unchanged %q", got.DatabaseURL, env["DATABASE_URL"])
	}
	if got.MinIOEndpoint != env["MINIO_ENDPOINT"] {
		t.Errorf("MinIOEndpoint = %q, want unchanged %q", got.MinIOEndpoint, env["MINIO_ENDPOINT"])
	}
	if got.MinIOAccessKey != env["MINIO_ACCESS_KEY"] {
		t.Errorf("MinIOAccessKey = %q, want unchanged %q", got.MinIOAccessKey, env["MINIO_ACCESS_KEY"])
	}
	if got.MinIOSecretKey != env["MINIO_SECRET_KEY"] {
		t.Errorf("MinIOSecretKey = %q, want unchanged %q", got.MinIOSecretKey, env["MINIO_SECRET_KEY"])
	}
	if got.MinIOBucket != env["MINIO_BUCKET"] {
		t.Errorf("MinIOBucket = %q, want unchanged %q", got.MinIOBucket, env["MINIO_BUCKET"])
	}
	if got.RuntimeURL != env["RUNTIME_URL"] {
		t.Errorf("RuntimeURL = %q, want unchanged %q", got.RuntimeURL, env["RUNTIME_URL"])
	}
}

func TestConfigFromEnvRejectsInvalidWebOrigin(t *testing.T) {
	env := validEnvironment()
	env["WEB_ORIGIN"] = "://not-a-url"

	_, err := ConfigFromEnv(func(key string) string { return env[key] })
	assertNamedError(t, err, "WEB_ORIGIN")
}

func TestConfigFromEnvRejectsRelativeProfileRoot(t *testing.T) {
	env := validEnvironment()
	env["PROFILE_ROOT"] = "profiles"

	_, err := ConfigFromEnv(func(key string) string { return env[key] })
	assertNamedError(t, err, "PROFILE_ROOT")
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
		"PROFILE_ROOT":     "/app/profiles",
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
