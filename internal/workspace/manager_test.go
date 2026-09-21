package workspace

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAMissingFileNamesThePathItLookedFor: a workspace-relative name that is
// not there has to come back with the absolute path that was looked at. The
// workspace is a level below the work directory the caller named, which is not
// where an agent naturally puts a file; "openat x: no such file or directory"
// sent a real agent off inventing a directory (voice-scribe, 2026-09-14). The
// scribes and image-forge were fixed then; this server was not.
func TestAMissingFileNamesThePathItLookedFor(t *testing.T) {
	workDir := t.TempDir()
	var m Manager
	w, err := m.EnsureUnder(workDir, "deck")
	if err != nil {
		t.Fatal(err)
	}
	for name, call := range map[string]func() error{
		"ReadFile":      func() error { _, err := w.ReadFile("manifest.json"); return err },
		"Stat":          func() error { _, err := w.Stat("manifest.json"); return err },
		"VerifyRegular": func() error { return w.VerifyRegular("manifest.json") },
	} {
		err := call()
		if err == nil {
			t.Fatalf("%s: a file that is not there was found", name)
		}
		if !strings.Contains(err.Error(), w.Path("manifest.json")) {
			t.Errorf("%s: the error does not name the absolute path it looked for: %q", name, err)
		}
		if !strings.Contains(err.Error(), "<work_dir>/<workspace_id>/") {
			t.Errorf("%s: the error does not say what the name is relative to: %q", name, err)
		}
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s: the error stopped being fs.ErrNotExist: %v", name, err)
		}
	}
}

// TestEnsureRefusesLinkedWorkspaceDir pins the containment property that
// os.Root alone cannot give: <work_dir>/<id> itself must be a real directory.
// os.Root contains operations within a root but resolves the root path
// normally, so a link pre-planted at <work_dir>/<id> — by any other tool with
// write access to work_dir — made os.OpenRoot(w.BaseDir) anchor on the link's
// target, and every read and write then landed outside work_dir while
// reporting success.
func TestEnsureRefusesLinkedWorkspaceDir(t *testing.T) {
	// EvalSymlinks first: on macOS t.TempDir() sits under /var, which is
	// itself a link to /private/var, so a raw comparison would never match.
	workDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(workDir, "deck")); err != nil {
		t.Fatal(err)
	}

	w, err := NewManager().EnsureUnder(workDir, "deck")
	if err == nil {
		t.Fatalf("a linked workspace dir was accepted: base=%s", w.BaseDir)
	}
	if !strings.Contains(err.Error(), "deck") {
		t.Errorf("the refusal does not name the workspace id: %q", err)
	}
	if !strings.Contains(err.Error(), outside) {
		t.Errorf("the refusal does not name what the id resolved to: %q", err)
	}

	// Nothing may have been created or written through the link.
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("the link's target was written through: %v", names)
	}
}
