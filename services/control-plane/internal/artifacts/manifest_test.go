package artifacts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"harness-forge.local/control-plane/internal/contracts"
	"harness-forge.local/control-plane/internal/profiles"
)

func snapshot() profiles.Snapshot {
	return profiles.Snapshot{Artifacts: profiles.ArtifactPolicy{ManifestSchemaVersion: 1, AllowedTypes: []string{"html", "image", "data", "markdown"}, MaxFileBytes: 10485760, MaxTotalBytes: 52428800}}
}

func outputFixture(t *testing.T, entries ...contracts.ArtifactCandidate) string {
	t.Helper()
	root := t.TempDir()
	if len(entries) == 0 {
		entries = []contracts.ArtifactCandidate{{Name: "report", Title: "Report", Type: "html", Entry: "report/index.html", Primary: true}}
	}
	data, err := json.Marshal(contracts.ArtifactManifest{SchemaVersion: 1, Artifacts: entries})
	if err != nil {
		t.Fatal(err)
	}
	writeOutput(t, root, "artifact-manifest.json", string(data))
	writeOutput(t, root, "report/index.html", "<script src=\"assets/chart.js\"></script>")
	writeOutput(t, root, "report/assets/chart.js", "window.chart = 1;")
	writeOutput(t, root, "report/style.css", "body { color: blue; }")
	writeOutput(t, root, "shared/data.json", "{\"ok\":true}")
	writeOutput(t, root, "report/photo.png", "PNG")
	return root
}

func writeOutput(t *testing.T, root, name, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestManifestValidation(t *testing.T) {
	for _, tc := range []struct{ name, manifest string }{
		{"schema", `{"schema_version":2,"artifacts":[]}`},
		{"unknown", `{"schema_version":1,"artifacts":[],"extra":true}`},
		{"missing fields", `{"schema_version":1,"artifacts":[{"name":"a"}]}`},
		{"duplicate", `{"schema_version":1,"artifacts":[{"name":"a","title":"a","type":"html","entry":"report/index.html","primary":false},{"name":"a","title":"b","type":"html","entry":"report/index.html","primary":false}]}`},
		{"multiple primary", `{"schema_version":1,"artifacts":[{"name":"a","title":"a","type":"html","entry":"report/index.html","primary":true},{"name":"b","title":"b","type":"html","entry":"report/index.html","primary":true}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := outputFixture(t)
			writeOutput(t, root, "artifact-manifest.json", tc.manifest)
			if _, err := ValidateManifest(root, snapshot()); err == nil {
				t.Fatal("invalid manifest accepted")
			}
		})
	}
	for _, entry := range []string{"missing.html", "/etc/passwd", "../outside", "report/../index.html", `report\index.html`} {
		t.Run(entry, func(t *testing.T) {
			root := outputFixture(t, contracts.ArtifactCandidate{Name: "r", Title: "R", Type: "html", Entry: entry})
			if _, err := ValidateManifest(root, snapshot()); err == nil {
				t.Fatal("invalid entry accepted")
			}
		})
	}
	root := outputFixture(t)
	got, err := ValidateManifest(root, snapshot())
	if err != nil || len(got.Manifest.Artifacts) != 1 || len(got.Files) != 5 {
		t.Fatalf("validated = %#v, %v", got, err)
	}
}

func TestManifestUsesSnapshotLimitsAndAllowedTypes(t *testing.T) {
	for _, kind := range []string{"file", "total", "type", "schema"} {
		t.Run(kind, func(t *testing.T) {
			root, profile := outputFixture(t), snapshot()
			switch kind {
			case "file":
				writeOutput(t, root, "large.bin", strings.Repeat("x", int(profile.Artifacts.MaxFileBytes)+1))
			case "total":
				for i := 0; i < 6; i++ {
					writeOutput(t, root, string(rune('a'+i))+".bin", strings.Repeat("x", int(profile.Artifacts.MaxFileBytes)))
				}
			case "type":
				profile.Artifacts.AllowedTypes = []string{"data"}
			case "schema":
				profile.Artifacts.ManifestSchemaVersion = 2
			}
			if _, err := ValidateManifest(root, profile); err == nil {
				t.Fatal("profile policy ignored")
			}
		})
	}
	root, profile := outputFixture(t), snapshot()
	profile.Artifacts.MaxFileBytes = 1024
	profile.Artifacts.MaxTotalBytes = 2048
	writeOutput(t, root, "profile-specific.bin", strings.Repeat("x", 1025))
	if _, err := ValidateManifest(root, profile); err == nil {
		t.Fatal("used independent hardcoded policy")
	}
}

func TestManifestRejectsEscapingSymlinks(t *testing.T) {
	for _, entry := range []bool{true, false} {
		root := outputFixture(t)
		outside := filepath.Join(t.TempDir(), "secret")
		if err := os.WriteFile(outside, []byte("private"), 0600); err != nil {
			t.Fatal(err)
		}
		name := "other"
		if entry {
			name = "report/index.html"
			if err := os.Remove(filepath.Join(root, name)); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Symlink(outside, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
		if _, err := ValidateManifest(root, snapshot()); err == nil {
			t.Fatal("escaping symlink accepted")
		}
	}
}

func TestManifestEmptyOutputsAllowedButNonemptyRequiresManifest(t *testing.T) {
	root := t.TempDir()
	got, err := ValidateManifest(root, snapshot())
	if err != nil || len(got.Manifest.Artifacts) != 0 || len(got.Files) != 0 {
		t.Fatalf("empty outputs: %#v %v", got, err)
	}
	writeOutput(t, root, "undeclared.txt", "output")
	if _, err := ValidateManifest(root, snapshot()); err == nil {
		t.Fatal("nonempty outputs accepted without manifest")
	}
}

func TestManifestRejectsSymlinkOutputRoot(t *testing.T) {
	outside := outputFixture(t)
	link := filepath.Join(t.TempDir(), "outputs")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateManifest(link, snapshot()); err == nil {
		t.Fatal("symlink output root escaped the trusted run layout")
	}
}

func TestManifestRejectsPathsGatewayCannotServe(t *testing.T) {
	for _, name := range []string{"report/%2e%2e.html", "report/line\nbreak.html", "projects/other/artifacts/other/index.html"} {
		root := outputFixture(t)
		writeOutput(t, root, name, "output")
		if _, err := ValidateManifest(root, snapshot()); err == nil {
			t.Fatalf("unservable output path accepted: %q", name)
		}
	}
}

func TestManifestAllowsSafeInternalSymlinkAndExactSnapshotBounds(t *testing.T) {
	root := outputFixture(t)
	if err := os.Symlink("index.html", filepath.Join(root, "report", "alias.html")); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateManifest(root, snapshot()); err != nil {
		t.Fatalf("safe internal symlink: %v", err)
	}
	root = t.TempDir()
	writeOutput(t, root, "artifact-manifest.json", `{"schema_version":1,"artifacts":[{"name":"data","title":"Data","type":"data","entry":"data.bin","primary":true}]}`)
	writeOutput(t, root, "data.bin", strings.Repeat("x", 1024))
	manifestInfo, err := os.Stat(filepath.Join(root, "artifact-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	profile := snapshot()
	profile.Artifacts.MaxFileBytes = 1024
	profile.Artifacts.MaxTotalBytes = 1024 + manifestInfo.Size()
	if _, err := ValidateManifest(root, profile); err != nil {
		t.Fatalf("exact bounds: %v", err)
	}
	profile.Artifacts.MaxTotalBytes--
	if _, err := ValidateManifest(root, profile); err == nil {
		t.Fatal("manifest excluded from total output size")
	}
}

func TestContentTypeExplicitAndPortable(t *testing.T) {
	for name, want := range map[string]string{"index.html": "text/html; charset=utf-8", "chart.js": "text/javascript; charset=utf-8", "style.css": "text/css; charset=utf-8", "data.json": "application/json", "a.png": "image/png", "a.jpg": "image/jpeg", "a.jpeg": "image/jpeg", "a.gif": "image/gif", "a.svg": "image/svg+xml", "a.webp": "image/webp", "a.ico": "image/vnd.microsoft.icon", "a.avif": "image/avif", "a.bin": "application/octet-stream", "README.md": "application/octet-stream", "index.HTML": "text/html; charset=utf-8"} {
		if got := ContentType(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}
