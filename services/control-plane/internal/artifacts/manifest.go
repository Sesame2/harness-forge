package artifacts

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"harness-forge.local/control-plane/internal/contracts"
	"harness-forge.local/control-plane/internal/profiles"
)

type File struct {
	Path string
	Data []byte
}
type ValidatedManifest struct {
	Manifest contracts.ArtifactManifest
	Files    []File
}

// ValidateManifest snapshots finished worker outputs before any upload. Rooted
// file access prevents symlink escapes, including between validation and reads.
func ValidateManifest(outputsRoot string, snapshot profiles.Snapshot) (ValidatedManifest, error) {
	policy := snapshot.Artifacts
	if policy.ManifestSchemaVersion != 1 || policy.MaxFileBytes <= 0 || policy.MaxTotalBytes <= 0 {
		return ValidatedManifest{}, errors.New("invalid artifact snapshot policy")
	}
	outputsRoot = filepath.Clean(outputsRoot)
	info, err := os.Lstat(outputsRoot)
	if err != nil {
		return ValidatedManifest{}, err
	}
	if !info.IsDir() {
		return ValidatedManifest{}, errors.New("outputs root must be a real directory, not a symlink")
	}
	root, err := os.OpenRoot(outputsRoot)
	if err != nil {
		return ValidatedManifest{}, err
	}
	defer root.Close()
	result := ValidatedManifest{Manifest: contracts.ArtifactManifest{SchemaVersion: 1, Artifacts: []contracts.ArtifactCandidate{}}, Files: []File{}}
	var manifestData []byte
	var total int64
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !ValidRelativePath(name) {
			return fmt.Errorf("unsafe output path %q", name)
		}
		info, err := root.Stat(name)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("output %q must be a regular file", name)
		}
		if info.Size() > policy.MaxFileBytes {
			return fmt.Errorf("output %q exceeds max_file_bytes", name)
		}
		if info.Size() > policy.MaxTotalBytes-total {
			return errors.New("outputs exceed max_total_bytes")
		}
		file, err := root.Open(name)
		if err != nil {
			return err
		}
		data, readErr := io.ReadAll(io.LimitReader(file, min(policy.MaxFileBytes, policy.MaxTotalBytes-total)+1))
		closeErr := file.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return err
		}
		if int64(len(data)) > policy.MaxFileBytes {
			return fmt.Errorf("output %q exceeds max_file_bytes", name)
		}
		if int64(len(data)) > policy.MaxTotalBytes-total {
			return errors.New("outputs exceed max_total_bytes")
		}
		total += int64(len(data))
		if name == "artifact-manifest.json" {
			manifestData = data
			return nil
		}
		result.Files = append(result.Files, File{Path: name, Data: data})
		return nil
	})
	if err != nil {
		return ValidatedManifest{}, err
	}
	if manifestData == nil {
		if len(result.Files) == 0 {
			return result, nil
		}
		return ValidatedManifest{}, errors.New("nonempty outputs require artifact-manifest.json")
	}
	result.Manifest, err = contracts.ParseArtifactManifest(manifestData)
	if err != nil {
		return ValidatedManifest{}, err
	}
	for _, candidate := range result.Manifest.Artifacts {
		if !slices.Contains(policy.AllowedTypes, candidate.Type) {
			return ValidatedManifest{}, fmt.Errorf("artifact type %q disallowed by snapshot", candidate.Type)
		}
		if !ValidRelativePath(candidate.Entry) || !slices.ContainsFunc(result.Files, func(file File) bool { return file.Path == candidate.Entry }) {
			return ValidatedManifest{}, fmt.Errorf("artifact entry %q is not an output file", candidate.Entry)
		}
	}
	return result, nil
}

// ValidRelativePath validates decoded output-relative paths in both publishing
// and the gateway. It never URL-decodes: the HTTP boundary does that once.
func ValidRelativePath(value string) bool {
	if !fs.ValidPath(value) || value == "." || path.Clean(value) != value || strings.ContainsAny(value, "\\:\x00\r\n") {
		return false
	}
	lower := strings.ToLower(value)
	if strings.Contains(lower, "%2e") || strings.Contains(lower, "%2f") || strings.Contains(lower, "%5c") {
		return false
	}
	return !(strings.HasPrefix(value, "projects/") && strings.Contains(value, "/artifacts/"))
}

// ContentType is explicit and independent of host MIME databases or sniffing.
func ContentType(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json":
		return "application/json"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".svg":
		return "image/svg+xml"
	case ".webp":
		return "image/webp"
	case ".ico":
		return "image/vnd.microsoft.icon"
	case ".avif":
		return "image/avif"
	case ".bmp":
		return "image/bmp"
	case ".tif", ".tiff":
		return "image/tiff"
	default:
		return "application/octet-stream"
	}
}
