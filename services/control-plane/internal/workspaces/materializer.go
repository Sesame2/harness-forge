package workspaces

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/google/uuid"

	"harness-forge.local/control-plane/internal/agentexec"
	"harness-forge.local/control-plane/internal/objectstore"
	"harness-forge.local/control-plane/internal/profiles"
	"harness-forge.local/control-plane/internal/projects"
)

type Materializer struct {
	root    string
	objects objectstore.Store
}

func NewMaterializer(root string, objects objectstore.Store) *Materializer {
	return &Materializer{root, objects}
}
func (m *Materializer) Paths(id uuid.UUID) agentexec.Paths {
	root := filepath.Join(m.root, id.String())
	return agentexec.Paths{Inputs: filepath.Join(root, "inputs"), Workspace: filepath.Join(root, "workspace"), Outputs: filepath.Join(root, "outputs")}
}

// Prepare never removes partial results. An existing run directory is evidence
// for recovery, not permission to overwrite it or rerun an Agent.
func (m *Materializer) Prepare(ctx context.Context, id uuid.UUID, snapshot profiles.Snapshot, inputs []projects.InputFile) (paths agentexec.Paths, err error) {
	paths = m.Paths(id)
	if id == uuid.Nil || !filepath.IsAbs(m.root) {
		return paths, errors.New("invalid workspace identity or root")
	}
	rootInfo, err := os.Lstat(m.root)
	if err != nil {
		return paths, fmt.Errorf("workspace root: %w", err)
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return paths, errors.New("workspace root must be a directory, not a symlink")
	}
	runRoot := filepath.Dir(paths.Inputs)
	if err := os.Mkdir(runRoot, 0770); err != nil {
		return paths, fmt.Errorf("create run workspace: %w", err)
	}
	if err := os.Chmod(runRoot, 0770); err != nil {
		return paths, fmt.Errorf("chmod run workspace: %w", err)
	}
	for _, path := range []string{paths.Inputs, paths.Workspace, paths.Outputs} {
		if err := os.Mkdir(path, 0770); err != nil {
			return paths, fmt.Errorf("create workspace directory: %w", err)
		}
		if err := os.Chmod(path, 0770); err != nil {
			return paths, fmt.Errorf("chmod workspace directory: %w", err)
		}
	}
	for _, file := range snapshot.WorkspaceTemplate {
		if !fs.ValidPath(file.Path) || strings.Contains(file.Path, "\\") || file.Path == "." {
			return paths, fmt.Errorf("unsafe template path %q", file.Path)
		}
		if err := ctx.Err(); err != nil {
			return paths, err
		}
		target := filepath.Join(paths.Workspace, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(target), 0770); err != nil {
			return paths, fmt.Errorf("template directory %q: %w", file.Path, err)
		}
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0660)
		if err != nil {
			return paths, fmt.Errorf("create template %q: %w", file.Path, err)
		}
		_, writeErr := out.Write(file.Content)
		err = errors.Join(writeErr, out.Chmod(0660), out.Close())
		if err != nil {
			return paths, fmt.Errorf("copy template %q: %w", file.Path, err)
		}
	}
	var files []string
	for _, input := range inputs {
		if err := ctx.Err(); err != nil {
			return paths, err
		}
		if input.ID == uuid.Nil {
			return paths, errors.New("input ID is required")
		}
		target := filepath.Join(paths.Inputs, input.ID.String()+"-"+sanitizeName(input.DisplayName))
		if err := m.copyInput(ctx, input, target); err != nil {
			return paths, fmt.Errorf("copy input %s: %w", input.ID, err)
		}
		files = append(files, target)
	}
	for _, file := range files {
		if err := os.Chmod(file, 0440); err != nil {
			return paths, fmt.Errorf("seal input: %w", err)
		}
	}
	if err := os.Chmod(paths.Inputs, 0550); err != nil {
		return paths, fmt.Errorf("seal input directory: %w", err)
	}
	return paths, nil
}
func (m *Materializer) copyInput(ctx context.Context, input projects.InputFile, target string) error {
	in, err := m.objects.Open(ctx, input.ObjectKey)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0660)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	return errors.Join(copyErr, out.Close())
}
func sanitizeName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, name)
	if name == "" || name == "." || name == ".." {
		return "input"
	}
	if len(name) > 180 {
		name = name[:180]
	}
	return name
}
