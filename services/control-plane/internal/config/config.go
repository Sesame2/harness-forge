package config

import (
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strings"
)

const (
	DockerSandboxProvider = "docker"
	FakeSandboxProvider   = "fake"
)

type Config struct {
	ArtifactPublicOrigin string
	HTTPAddr             string
	ArtifactAddr         string
	DatabaseURL          string
	MinIOEndpoint        string
	MinIOAccessKey       string
	MinIOSecretKey       string
	MinIOBucket          string
	ProfileRoot          string
	SandboxProvider      string
	RuntimeURL           string
	WorkspaceRoot        string
	WebOrigin            string
	// FakeFixtureRoot is injectable for local tests; deployed fixtures have a fixed /app root.
	FakeFixtureRoot string
}

func ConfigFromEnv(getenv func(string) string) (Config, error) {
	config := Config{
		ArtifactPublicOrigin: getenv("ARTIFACT_PUBLIC_ORIGIN"),
		HTTPAddr:             valueOrDefault(getenv("HTTP_ADDR"), ":8080"),
		ArtifactAddr:         valueOrDefault(getenv("ARTIFACT_ADDR"), ":8081"),
		DatabaseURL:          getenv("DATABASE_URL"),
		MinIOEndpoint:        getenv("MINIO_ENDPOINT"),
		MinIOAccessKey:       getenv("MINIO_ACCESS_KEY"),
		MinIOSecretKey:       getenv("MINIO_SECRET_KEY"),
		MinIOBucket:          getenv("MINIO_BUCKET"),
		ProfileRoot:          getenv("PROFILE_ROOT"),
		SandboxProvider:      valueOrDefault(getenv("SANDBOX_PROVIDER"), DockerSandboxProvider),
		WorkspaceRoot:        valueOrDefault(getenv("WORKSPACE_ROOT"), "/workspaces"),
		WebOrigin:            getenv("WEB_ORIGIN"),
	}

	for name, value := range map[string]string{
		"ARTIFACT_PUBLIC_ORIGIN": config.ArtifactPublicOrigin,
		"DATABASE_URL":           config.DatabaseURL,
		"MINIO_ENDPOINT":         config.MinIOEndpoint,
		"MINIO_ACCESS_KEY":       config.MinIOAccessKey,
		"MINIO_SECRET_KEY":       config.MinIOSecretKey,
		"MINIO_BUCKET":           config.MinIOBucket,
		"PROFILE_ROOT":           config.ProfileRoot,
	} {
		if strings.TrimSpace(value) == "" {
			return Config{}, fmt.Errorf("%s is required", name)
		}
	}
	if !filepath.IsAbs(config.ProfileRoot) {
		return Config{}, fmt.Errorf("PROFILE_ROOT must be an absolute path")
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

	for name, origin := range map[string]string{"ARTIFACT_PUBLIC_ORIGIN": config.ArtifactPublicOrigin, "WEB_ORIGIN": config.WebOrigin} {
		if origin == "" && name == "WEB_ORIGIN" {
			continue
		}
		if !validOrigin(origin) {
			return Config{}, fmt.Errorf("%s must be an absolute HTTP(S) origin without path, userinfo, query or fragment", name)
		}
	}

	return config, nil
}

func validOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Path != "" || u.Opaque != "" || strings.ContainsAny(origin, "?#") {
		return false
	}
	if net.ParseIP(u.Hostname()) == nil {
		for _, ch := range u.Hostname() {
			if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '.' || ch == '-') {
				return false
			}
		}
	}
	return true
}

func valueOrDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
