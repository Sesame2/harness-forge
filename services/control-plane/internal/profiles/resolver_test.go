package profiles

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestProfileResolverLoadsImmutableSnapshot(t *testing.T) {
	root := writeProfileFixture(t, "geo-analysis", validProfileYAML("geo-analysis", "1"), "You are a geospatial analyst.\n", map[string]string{"README.md": "# Workspace\n"})
	resolver, err := NewResolver(root)
	if err != nil {
		t.Fatalf("NewResolver() error = %v", err)
	}

	got, err := resolver.Resolve("geo-analysis")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.ID != "geo-analysis" || got.Version != "1" || got.DisplayName != "Geo Analysis" {
		t.Fatalf("identity = %#v", got)
	}
	if got.SystemPrompt != "You are a geospatial analyst.\n" {
		t.Errorf("SystemPrompt = %q", got.SystemPrompt)
	}
	if !reflect.DeepEqual(got.AllowedTools, []string{"Read", "Write", "Bash"}) || got.PermissionMode != "acceptEdits" {
		t.Errorf("tool policy = %#v/%q", got.AllowedTools, got.PermissionMode)
	}
	if got.Agent.MaxTurns != 20 || got.Agent.MaxBudgetUSD != 5 {
		t.Errorf("agent limits = %#v", got.Agent)
	}
	wantMedia := []string{"text/csv", "application/geo+json"}
	if !reflect.DeepEqual(got.AcceptedInputMediaTypes, wantMedia) {
		t.Errorf("AcceptedInputMediaTypes = %#v, want %#v", got.AcceptedInputMediaTypes, wantMedia)
	}
	if got.Artifacts.ManifestSchemaVersion != 1 || !reflect.DeepEqual(got.Artifacts.AllowedTypes, []string{"html", "markdown", "image", "data"}) {
		t.Errorf("artifact policy = %#v", got.Artifacts)
	}
	if got.Artifacts.MaxFileBytes != 10_485_760 || got.Artifacts.MaxTotalBytes != 52_428_800 {
		t.Errorf("artifact byte limits = %#v", got.Artifacts)
	}
	if len(got.WorkspaceTemplate) != 1 || got.WorkspaceTemplate[0].Path != "README.md" || string(got.WorkspaceTemplate[0].Content) != "# Workspace\n" {
		t.Errorf("WorkspaceTemplate = %#v", got.WorkspaceTemplate)
	}
	if len(got.Digest) != 64 {
		t.Errorf("Digest length = %d, want 64", len(got.Digest))
	}

	// The snapshot stays immutable after startup.
	if err := os.WriteFile(filepath.Join(root, "geo-analysis", "system-prompt.md"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	again, err := resolver.ResolveVersion("geo-analysis", "1")
	if err != nil || again.Digest != got.Digest || again.SystemPrompt != got.SystemPrompt {
		t.Fatalf("immutable ResolveVersion() = %#v, %v", again, err)
	}
}

func TestProfileResolverRejectsInvalidProfiles(t *testing.T) {
	tests := []struct {
		name      string
		directory string
		yaml      string
		prompt    *string
		template  map[string]string
	}{
		{name: "id mismatch", directory: "geo-analysis", yaml: validProfileYAML("other", "1"), prompt: ptr("prompt"), template: map[string]string{"README.md": "template"}},
		{name: "missing required", directory: "geo-analysis", yaml: "id: geo-analysis\nversion: '1'\n", prompt: ptr("prompt"), template: map[string]string{"README.md": "template"}},
		{name: "missing prompt", directory: "geo-analysis", yaml: validProfileYAML("geo-analysis", "1"), template: map[string]string{"README.md": "template"}},
		{name: "missing template", directory: "geo-analysis", yaml: validProfileYAML("geo-analysis", "1"), prompt: ptr("prompt")},
		{name: "zero max file", directory: "geo-analysis", yaml: strings.Replace(validProfileYAML("geo-analysis", "1"), "max_file_bytes: 10485760", "max_file_bytes: 0", 1), prompt: ptr("prompt"), template: map[string]string{"README.md": "template"}},
		{name: "zero max total", directory: "geo-analysis", yaml: strings.Replace(validProfileYAML("geo-analysis", "1"), "max_total_bytes: 52428800", "max_total_bytes: 0", 1), prompt: ptr("prompt"), template: map[string]string{"README.md": "template"}},
		{name: "negative budget", directory: "geo-analysis", yaml: strings.Replace(validProfileYAML("geo-analysis", "1"), "max_budget_usd: 5", "max_budget_usd: -1", 1), prompt: ptr("prompt"), template: map[string]string{"README.md": "template"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, tt.directory)
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "profile.yaml"), []byte(tt.yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			if tt.prompt != nil {
				if err := os.WriteFile(filepath.Join(dir, "system-prompt.md"), []byte(*tt.prompt), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			for name, content := range tt.template {
				path := filepath.Join(dir, "workspace-template", name)
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := NewResolver(root); err == nil {
				t.Fatal("NewResolver() error = nil")
			}
		})
	}
}

func TestProfileResolverAllowsOmittedOrZeroBudget(t *testing.T) {
	for _, tt := range []struct {
		name string
		yaml string
	}{
		{name: "omitted", yaml: strings.Replace(validProfileYAML("geo-analysis", "1"), "  max_budget_usd: 5\n", "", 1)},
		{name: "zero", yaml: strings.Replace(validProfileYAML("geo-analysis", "1"), "max_budget_usd: 5", "max_budget_usd: 0", 1)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := writeProfileFixture(t, "geo-analysis", tt.yaml, "prompt", map[string]string{"README.md": "template"})
			resolver, err := NewResolver(root)
			if err != nil {
				t.Fatalf("NewResolver() error = %v", err)
			}
			snapshot, err := resolver.Resolve("geo-analysis")
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if snapshot.Agent.MaxBudgetUSD != 0 {
				t.Fatalf("MaxBudgetUSD = %v, want 0", snapshot.Agent.MaxBudgetUSD)
			}
		})
	}
}

func TestProfileDigestChangesWithEverySourceAndIgnoresMtime(t *testing.T) {
	root := writeProfileFixture(t, "geo-analysis", validProfileYAML("geo-analysis", "1"), "prompt", map[string]string{"b.txt": "b", "a.txt": "a"})
	loadDigest := func() string {
		t.Helper()
		r, err := NewResolver(root)
		if err != nil {
			t.Fatal(err)
		}
		s, err := r.Resolve("geo-analysis")
		if err != nil {
			t.Fatal(err)
		}
		return s.Digest
	}
	base := loadDigest()
	now := filepath.Join(root, "geo-analysis", "profile.yaml")
	mtime := time.Unix(1_700_000_000, 0)
	if err := os.Chtimes(now, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	if got := loadDigest(); got != base {
		t.Fatalf("mtime changed digest: %s != %s", got, base)
	}

	for _, path := range []string{"profile.yaml", "system-prompt.md", filepath.Join("workspace-template", "a.txt")} {
		t.Run(path, func(t *testing.T) {
			full := filepath.Join(root, "geo-analysis", path)
			original, err := os.ReadFile(full)
			if err != nil {
				t.Fatal(err)
			}
			changed := append(append([]byte(nil), original...), 'x')
			if path == "profile.yaml" {
				changed = []byte(strings.Replace(string(original), "display_name: Geo Analysis", "display_name: Geo Analysis X", 1))
			}
			if err := os.WriteFile(full, changed, 0o600); err != nil {
				t.Fatal(err)
			}
			if got := loadDigest(); got == base {
				t.Fatalf("digest unchanged after %s changed", path)
			}
			if err := os.WriteFile(full, original, 0o600); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestProfileResolverFailsClosedForUnknownOrWrongVersion(t *testing.T) {
	root := writeProfileFixture(t, "geo-analysis", validProfileYAML("geo-analysis", "2026-07"), "prompt", map[string]string{"README.md": "template"})
	r, err := NewResolver(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve("missing"); err == nil {
		t.Fatal("Resolve(missing) error = nil")
	}
	if _, err := r.ResolveVersion("geo-analysis", "latest"); err == nil {
		t.Fatal("ResolveVersion(wrong) error = nil")
	}
	if _, err := r.ResolveVersion("geo-analysis", "2026-07"); err != nil {
		t.Fatalf("ResolveVersion(exact) error = %v", err)
	}
}

func writeProfileFixture(t *testing.T, directory, yaml, prompt string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, directory)
	if err := os.MkdirAll(filepath.Join(dir, "workspace-template"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "profile.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "system-prompt.md"), []byte(prompt), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		path := filepath.Join(dir, "workspace-template", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func validProfileYAML(id, version string) string {
	return "id: " + id + "\nversion: '" + version + "'\ndisplay_name: Geo Analysis\nsystem_prompt: system-prompt.md\nworkspace_template: workspace-template\ntools:\n  allowed: [Read, Write, Bash]\n  permission_mode: acceptEdits\nagent:\n  max_turns: 20\n  max_budget_usd: 5\ninputs:\n  accepted_media_types: [text/csv, application/geo+json]\nartifacts:\n  manifest_schema_version: 1\n  allowed_types: [html, markdown, image, data]\n  max_file_bytes: 10485760\n  max_total_bytes: 52428800\n"
}

func ptr(s string) *string { return &s }
