package workspaces

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"harness-forge.local/control-plane/internal/objectstore"
	"harness-forge.local/control-plane/internal/profiles"
	"harness-forge.local/control-plane/internal/projects"
)

func TestMaterializeCopiesSnapshotAndSealsInputs(t *testing.T) {
	ctx := context.Background()
	objects := objectstore.NewMemory()
	if err := objects.Put(ctx, "input", strings.NewReader("data"), objectstore.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	input := projects.InputFile{ID: uuid.New(), DisplayName: "../../data.csv", ObjectKey: "input", SizeBytes: 4}
	paths, err := NewMaterializer(t.TempDir(), objects).Prepare(ctx, id, profiles.Snapshot{WorkspaceTemplate: []profiles.WorkspaceFile{{Path: "nested/README.md", Content: []byte("template")}}}, []projects.InputFile{input})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(paths.Inputs, 0770) })
	for path, mode := range map[string]os.FileMode{paths.Inputs: 0550, paths.Workspace: 0770, paths.Outputs: 0770, filepath.Join(paths.Inputs, input.ID.String()+"-data.csv"): 0440} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("%s mode=%v err=%v", path, info, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(paths.Workspace, "nested/README.md"))
	if err != nil || string(data) != "template" {
		t.Fatalf("template %q %v", data, err)
	}
}

type openingStore struct {
	objectstore.Store
	open func() (io.ReadCloser, error)
}

func (s openingStore) Open(context.Context, string) (io.ReadCloser, error) { return s.open() }

type brokenStream struct{}

func (brokenStream) Read(p []byte) (int, error) {
	copy(p, "partial")
	return min(len(p), 7), errors.New("copy interrupted")
}
func (brokenStream) Close() error { return nil }

type closingStream struct {
	io.Reader
	close func() error
}

func (s closingStream) Close() error { return s.close() }

func TestMaterializeCopyAndChmodFailuresKeepDiagnostics(t *testing.T) {
	for _, kind := range []string{"copy", "chmod"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			id := uuid.New()
			inputID := uuid.New()
			input := projects.InputFile{ID: inputID, DisplayName: "data.csv", ObjectKey: "key"}
			store := openingStore{Store: objectstore.NewMemory()}
			store.open = func() (io.ReadCloser, error) {
				if kind == "copy" {
					return brokenStream{}, nil
				}
				return closingStream{Reader: strings.NewReader("data"), close: func() error {
					return os.Remove(filepath.Join(root, id.String(), "inputs", inputID.String()+"-data.csv"))
				}}, nil
			}
			paths, err := NewMaterializer(root, store).Prepare(context.Background(), id, profiles.Snapshot{WorkspaceTemplate: []profiles.WorkspaceFile{{Path: "diagnostic.txt", Content: []byte("keep")}}}, []projects.InputFile{input})
			if err == nil {
				t.Fatal("preparation unexpectedly succeeded")
			}
			if kind == "copy" && !strings.Contains(err.Error(), inputID.String()) {
				t.Fatal("missing input context", err)
			}
			if kind == "chmod" && !strings.Contains(err.Error(), "seal input") {
				t.Fatal(err)
			}
			if data, err := os.ReadFile(filepath.Join(paths.Workspace, "diagnostic.txt")); err != nil || string(data) != "keep" {
				t.Fatal("lost partial workspace", err)
			}
		})
	}
}

func TestMaterializeFailurePreservesPartialWorkspace(t *testing.T) {
	root := t.TempDir()
	id := uuid.New()
	m := NewMaterializer(root, objectstore.NewMemory())
	paths, err := m.Prepare(context.Background(), id, profiles.Snapshot{WorkspaceTemplate: []profiles.WorkspaceFile{{Path: "README.md", Content: []byte("keep")}}}, []projects.InputFile{{ID: uuid.New(), DisplayName: "bad.csv", ObjectKey: "missing"}})
	if err == nil || !strings.Contains(err.Error(), "input") {
		t.Fatalf("error %v", err)
	}
	if data, e := os.ReadFile(filepath.Join(paths.Workspace, "README.md")); e != nil || string(data) != "keep" {
		t.Fatalf("partial missing: %q %v", data, e)
	}
}

func TestMaterializeRejectsEscapeAndExistingSymlink(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", "a/../../escape", "a\\..\\escape"} {
		t.Run(name, func(t *testing.T) {
			_, err := NewMaterializer(t.TempDir(), objectstore.NewMemory()).Prepare(context.Background(), uuid.New(), profiles.Snapshot{WorkspaceTemplate: []profiles.WorkspaceFile{{Path: name}}}, nil)
			if err == nil {
				t.Fatal("unsafe template accepted")
			}
		})
	}
	root := t.TempDir()
	id := uuid.New()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, id.String())); err != nil {
		t.Fatal(err)
	}
	if _, err := NewMaterializer(root, objectstore.NewMemory()).Prepare(context.Background(), id, profiles.Snapshot{}, nil); err == nil {
		t.Fatal("symlink accepted")
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatal("wrote through symlink")
	}
}
