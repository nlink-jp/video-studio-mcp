package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nlink-jp/video-studio-mcp/internal/toolerr"
)

// TestAMissingFileNamesThePathItLookedFor: a workspace-relative name that is
// not there has to come back with the absolute path that was looked at. The
// workspace is a level below the work directory the caller named, which is not
// where an agent naturally puts a file; "openat x: no such file or directory"
// sent a real agent off inventing a directory (voice-scribe, 2026-09-14). The
// scribes and image-forge were fixed then; this server was not.
func TestAMissingFileNamesThePathItLookedFor(t *testing.T) {
	workDir := t.TempDir()
	m := NewManager(allowAll, noFloor)
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

	w, err := NewManager(allowAll, noFloor).EnsureUnder(workDir, "deck")
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

// allowAll stands for the server's check in tests of the manager's own
// mechanics; the check itself is workdir.Resolver.CheckBeneath's.
func allowAll(string) error { return nil }

// The directory actually used is judged before it is made: the check sees
// <work_dir>/<workspace_id>, and its refusal is returned as is, with nothing
// created. A Manager without a check refuses every workspace.
func TestEnsureUnderJudgesTheWorkspaceDirectoryBeforeMakingIt(t *testing.T) {
	work := t.TempDir()
	refusal := errors.New("refused")
	var seen string
	m := NewManager(func(dir string) error { seen = dir; return refusal }, noFloor)
	if _, err := m.EnsureUnder(work, "gh"); !errors.Is(err, refusal) {
		t.Fatalf("EnsureUnder = %v, want the check's refusal", err)
	}
	if want := filepath.Join(work, "gh"); seen != want {
		t.Errorf("the check saw %q, want %q", seen, want)
	}
	if _, err := os.Stat(filepath.Join(work, "gh")); !os.IsNotExist(err) {
		t.Errorf("a refused workspace was created (stat: %v)", err)
	}
	for name, m := range map[string]*Manager{"no check": NewManager(nil, noFloor), "zero": {}} {
		if _, err := m.EnsureUnder(work, "ws"); err == nil {
			t.Errorf("%s: EnsureUnder succeeded", name)
		}
	}
}

// noFloor stands for the server's floor in tests of the manager's own
// mechanics; the floor itself is workdir.Resolver.LocalPath's.
func noFloor(string, string) string { return "" }

// Every read through a workspace — ReadFile, Stat, VerifyRegular — and Judge
// itself refuse a path the floor refuses before anything is read or looked
// for, with the same refusal whether or not a file is there. A Manager without
// a floor refuses every workspace; a Workspace without one refuses every read.
func TestEveryReadIsJudgedBeforeItLooks(t *testing.T) {
	work := t.TempDir()
	floor := func(raw, _ string) string {
		if strings.HasSuffix(raw, ".secret") {
			return "a test floor refuses it"
		}
		return ""
	}
	w, err := NewManager(allowAll, floor).EnsureUnder(work, "ws")
	if err != nil {
		t.Fatal(err)
	}
	rel := filepath.Join(DirOutput, "1.secret")
	for name, read := range map[string]func() error{
		"ReadFile":      func() error { _, err := w.ReadFile(rel); return err },
		"Stat":          func() error { _, err := w.Stat(rel); return err },
		"VerifyRegular": func() error { return w.VerifyRegular(rel) },
		"Judge":         func() error { return w.Judge(rel) },
	} {
		if err := os.WriteFile(w.Path(rel), []byte("SECRET"), 0o600); err != nil {
			t.Fatal(err)
		}
		e := read()
		if err := os.Remove(w.Path(rel)); err != nil {
			t.Fatal(err)
		}
		m := read()
		if !errors.Is(e, toolerr.New(toolerr.CodePathNotAllowed, "")) || fmt.Sprint(e) != fmt.Sprint(m) {
			t.Errorf("%s: existing %v, missing %v; want the same path_not_allowed", name, e, m)
		}
	}
	if _, err := w.ReadFile(filepath.Join(DirOutput, "ok.png")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("an ordinary missing file: %v, want not found", err)
	}
	if _, err := NewManager(allowAll, nil).EnsureUnder(work, "ws2"); err == nil {
		t.Error("a Manager without a floor made a workspace")
	}
	if _, err := (&Workspace{ID: "x", BaseDir: w.BaseDir}).ReadFile("deck.jsonl"); !errors.Is(err, toolerr.New(toolerr.CodePathNotAllowed, "")) {
		t.Errorf("a Workspace without a floor read: %v", err)
	}
}

// PlaceFile writes through a temporary name next to the destination: a link a
// caller plants at that name, or at the destination, is replaced — the file it
// points at, outside the workspace, is never written.
func TestPlaceFileReplacesLinksItFinds(t *testing.T) {
	work := t.TempDir()
	w, err := NewManager(allowAll, noFloor).EnsureUnder(work, "deck")
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "rendered.mp4")
	if err := os.WriteFile(src, []byte("RENDERED"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(w.Path(DirOutput), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, planted := range []string{"deck.mp4.tmp", "deck.mp4"} {
		victim := filepath.Join(t.TempDir(), "victim")
		if err := os.WriteFile(victim, []byte("keep me"), 0o644); err != nil {
			t.Fatal(err)
		}
		link := w.Path(DirOutput, planted)
		_ = os.Remove(link)
		if err := os.Symlink(victim, link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if err := w.PlaceFile(filepath.Join(DirOutput, "deck.mp4"), src); err != nil {
			t.Fatalf("%s planted: PlaceFile: %v", planted, err)
		}
		if b, _ := os.ReadFile(victim); string(b) != "keep me" {
			t.Errorf("%s planted: the link's target was written: %q", planted, b)
		}
		fi, err := os.Lstat(w.Path(DirOutput, "deck.mp4"))
		if err != nil || !fi.Mode().IsRegular() {
			t.Fatalf("%s planted: the destination is not a regular file: %v %v", planted, fi, err)
		}
		if b, _ := os.ReadFile(w.Path(DirOutput, "deck.mp4")); string(b) != "RENDERED" {
			t.Errorf("%s planted: destination holds %q", planted, b)
		}
	}
}
