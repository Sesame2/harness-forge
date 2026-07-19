package config

import (
	"fmt"
	"net/url"
	"strings"
)

const (
	DockerSandboxProvider = "docker"
	FakeSandboxProvider   = "fake"
)

type Config struct {
	HTTPAddr        string
	ArtifactAddr    string
	DatabaseURL     string
	MinIOEndpoint   string
	MinIOAccessKey  string
	MinIOSecretKey  string
	MinIOBucket     string
	SandboxProvider string
	RuntimeURL      string
	WorkspaceRoot   string
	WebOrigin       string
}

func ConfigFromEnv(getenv func(string) string) (Config, error) {
	config := Config{
		HTTPAddr:        valueOrDefault(getenv("HTTP_ADDR"), ":8080"),
		ArtifactAddr:    valueOrDefault(getenv("ARTIFACT_ADDR"), ":8081"),
		DatabaseURL:     getenv("DATABASE_URL"),
		MinIOEndpoint:   getenv("MINIO_ENDPOINT"),
		MinIOAccessKey:  getenv("MINIO_ACCESS_KEY"),
		MinIOSecretKey:  getenv("MINIO_SECRET_KEY"),
		MinIOBucket:     getenv("MINIO_BUCKET"),
		SandboxProvider: valueOrDefault(getenv("SANDBOX_PROVIDER"), DockerSandboxProvider),
		WorkspaceRoot:   valueOrDefault(getenv("WORKSPACE_ROOT"), "/workspaces"),
		WebOrigin:       getenv("WEB_ORIGIN"),
	}

	for name, value := range map[string]string{
		"DATABASE_URL":     config.DatabaseURL,
		"MINIO_ENDPOINT":   config.MinIOEndpoint,
		"MINIO_ACCESS_KEY": config.MinIOAccessKey,
		"MINIO_SECRET_KEY": config.MinIOSecretKey,
		"MINIO_BUCKET":     config.MinIOBucket,
	} {
		if strings.TrimSpace(value) == "" {
			return Config{}, fmt.Errorf("%s is required", name)
		}
	}

	switch config.SandboxProvider {
	case DockerSandboxProvider:
		config.RuntimeURL = getenv("RUNTIME_URL")
		if strings.TrimSpace(config.RuntimeURL) == "" {
			return Config{}, fmt.Errorf("RUNTIME_URL is required for SANDBOX_PROVIDER=docker")
		}
		parsedRuntimeURL, err := url.Parse(config.RuntimeURL)
		if err != nil || (parsedRuntimeURL.Scheme != "http" && parsedRuntimeURL.Scheme != "https") || parsedRuntimeURL.Host == "" {
			return Config{}, fmt.Errorf("RUNTIME_URL must be an absolute HTTP(S) URL")
		}
	case FakeSandboxProvider:
		// The fake provider runs in-process and does not use a runtime URL.
	default:
		return Config{}, fmt.Errorf("SANDBOX_PROVIDER must be docker or fake, got %q", config.SandboxProvider)
	}

	if config.WebOrigin != "" {
		parsed, err := url.Parse(config.WebOrigin)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return Config{}, fmt.Errorf("WEB_ORIGIN must be an absolute URL")
		}
	}

	return config, nil
}

func valueOrDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
