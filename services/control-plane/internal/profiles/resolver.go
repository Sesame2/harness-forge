package profiles

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

var ErrNotFound = errors.New("profile not found")

type Resolver struct {
	snapshots map[string]Snapshot
}

type profileConfig struct {
	ID                string `yaml:"id"`
	Version           string `yaml:"version"`
	DisplayName       string `yaml:"display_name"`
	SystemPrompt      string `yaml:"system_prompt"`
	WorkspaceTemplate string `yaml:"workspace_template"`
	Tools             struct {
		Allowed        []string `yaml:"allowed"`
		PermissionMode string   `yaml:"permission_mode"`
	} `yaml:"tools"`
	Agent struct {
		MaxTurns     int     `yaml:"max_turns"`
		MaxBudgetUSD float64 `yaml:"max_budget_usd"`
	} `yaml:"agent"`
	Inputs struct {
		AcceptedMediaTypes []string `yaml:"accepted_media_types"`
	} `yaml:"inputs"`
	Artifacts struct {
		ManifestSchemaVersion int      `yaml:"manifest_schema_version"`
		AllowedTypes          []string `yaml:"allowed_types"`
		MaxFileBytes          int64    `yaml:"max_file_bytes"`
		MaxTotalBytes         int64    `yaml:"max_total_bytes"`
	} `yaml:"artifacts"`
}

func NewResolver(root string) (*Resolver, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read profile root: %w", err)
	}
	resolver := &Resolver{snapshots: make(map[string]Snapshot)}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		snapshot, err := loadSnapshot(root, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("load profile %q: %w", entry.Name(), err)
		}
		resolver.snapshots[entry.Name()] = snapshot
	}
	return resolver, nil
}

func (r *Resolver) Resolve(id string) (Snapshot, error) {
	snapshot, ok := r.snapshots[id]
	if !ok {
		return Snapshot{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return clone(snapshot), nil
}

func (r *Resolver) ResolveVersion(id, version string) (Snapshot, error) {
	snapshot, err := r.Resolve(id)
	if err != nil {
		return Snapshot{}, err
	}
	if snapshot.Version != version {
		return Snapshot{}, fmt.Errorf("%w: %s version %s", ErrNotFound, id, version)
	}
	return snapshot, nil
}

func loadSnapshot(root, directory string) (Snapshot, error) {
	dir := filepath.Join(root, directory)
	configBytes, err := os.ReadFile(filepath.Join(dir, "profile.yaml"))
	if err != nil {
		return Snapshot{}, fmt.Errorf("read profile.yaml: %w", err)
	}
	var config profileConfig
	if err := yaml.Unmarshal(configBytes, &config); err != nil {
		return Snapshot{}, fmt.Errorf("parse profile.yaml: %w", err)
	}
	if config.ID != directory {
		return Snapshot{}, fmt.Errorf("profile id %q must match directory %q", config.ID, directory)
	}
	if strings.TrimSpace(config.Version) == "" || strings.TrimSpace(config.DisplayName) == "" || config.SystemPrompt == "" || config.WorkspaceTemplate == "" ||
		len(config.Tools.Allowed) == 0 || config.Tools.PermissionMode == "" || config.Agent.MaxTurns <= 0 || config.Agent.MaxBudgetUSD < 0 ||
		len(config.Inputs.AcceptedMediaTypes) == 0 || config.Artifacts.ManifestSchemaVersion <= 0 || len(config.Artifacts.AllowedTypes) == 0 ||
		config.Artifacts.MaxFileBytes <= 0 || config.Artifacts.MaxTotalBytes <= 0 {
		return Snapshot{}, errors.New("profile.yaml has missing or invalid required fields")
	}
	promptPath, err := childPath(dir, config.SystemPrompt)
	if err != nil {
		return Snapshot{}, err
	}
	prompt, err := os.ReadFile(promptPath)
	if err != nil {
		return Snapshot{}, fmt.Errorf("read system prompt: %w", err)
	}
	templateRoot, err := childPath(dir, config.WorkspaceTemplate)
	if err != nil {
		return Snapshot{}, err
	}
	files, err := readWorkspace(templateRoot)
	if err != nil {
		return Snapshot{}, err
	}
	if len(files) == 0 {
		return Snapshot{}, errors.New("workspace template is empty")
	}

	hash := sha256.New()
	writeDigestPart(hash, "profile.yaml", configBytes)
	writeDigestPart(hash, config.SystemPrompt, prompt)
	for _, file := range files {
		writeDigestPart(hash, filepath.ToSlash(filepath.Join(config.WorkspaceTemplate, file.Path)), file.Content)
	}
	return Snapshot{
		ID: config.ID, Version: config.Version, DisplayName: config.DisplayName, SystemPrompt: string(prompt),
		AllowedTools: append([]string(nil), config.Tools.Allowed...), PermissionMode: config.Tools.PermissionMode,
		Agent:                   AgentPolicy{MaxTurns: config.Agent.MaxTurns, MaxBudgetUSD: config.Agent.MaxBudgetUSD},
		AcceptedInputMediaTypes: append([]string(nil), config.Inputs.AcceptedMediaTypes...),
		Artifacts:               ArtifactPolicy{ManifestSchemaVersion: config.Artifacts.ManifestSchemaVersion, AllowedTypes: append([]string(nil), config.Artifacts.AllowedTypes...), MaxFileBytes: config.Artifacts.MaxFileBytes, MaxTotalBytes: config.Artifacts.MaxTotalBytes},
		WorkspaceTemplate:       files, Digest: hex.EncodeToString(hash.Sum(nil)),
	}, nil
}

func childPath(root, name string) (string, error) {
	if filepath.IsAbs(name) || name == "." || strings.HasPrefix(filepath.Clean(name), ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("profile path %q must stay inside profile directory", name)
	}
	return filepath.Join(root, name), nil
}

func readWorkspace(root string) ([]WorkspaceFile, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("workspace template symlink is not allowed: %s", path)
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read workspace template: %w", err)
	}
	sort.Strings(paths)
	files := make([]WorkspaceFile, 0, len(paths))
	for _, path := range paths {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			return nil, fmt.Errorf("read workspace file %q: %w", path, err)
		}
		files = append(files, WorkspaceFile{Path: path, Content: content})
	}
	return files, nil
}

func writeDigestPart(hash interface{ Write([]byte) (int, error) }, name string, content []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(name)))
	_, _ = hash.Write(length[:])
	_, _ = hash.Write([]byte(name))
	binary.BigEndian.PutUint64(length[:], uint64(len(content)))
	_, _ = hash.Write(length[:])
	_, _ = hash.Write(content)
}

func clone(snapshot Snapshot) Snapshot {
	snapshot.AllowedTools = append([]string(nil), snapshot.AllowedTools...)
	snapshot.AcceptedInputMediaTypes = append([]string(nil), snapshot.AcceptedInputMediaTypes...)
	snapshot.Artifacts.AllowedTypes = append([]string(nil), snapshot.Artifacts.AllowedTypes...)
	snapshot.WorkspaceTemplate = append([]WorkspaceFile(nil), snapshot.WorkspaceTemplate...)
	for index := range snapshot.WorkspaceTemplate {
		snapshot.WorkspaceTemplate[index].Content = append([]byte(nil), snapshot.WorkspaceTemplate[index].Content...)
	}
	return snapshot
}
