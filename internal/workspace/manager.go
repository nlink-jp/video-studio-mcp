// Package workspace manages per-deck state directories.
//
// One workspace = one presentation. The agent places the page images, the
// page audio, and the manifest JSONL inside it; the server writes only under
// the output/ subdirectory. Layout:
//
//	<work_dir>/<id>/
//	├── <manifest>.jsonl   agent-authored page manifest
//	├── <images/audio>     agent-placed assets (any relative layout)
//	└── output/            rendered video + tmp segments (server-written)
//
// The workspace root is the agent-prepared directory every call passes as
// work_dir ("the server works in the workplace the agent prepared"); the server
// keeps no default of its own (organization ADR-021). Because that root is
// agent-writable, every server I/O inside a workspace goes through
// os.Root so symlinks planted in the workspace cannot make the server read or
// write outside it (kernel-enforced containment). An os.Root resolves its own
// path normally, so it cannot vouch for the base directory it is anchored on:
// the base is additionally verified by real path (see makeWorkspaceDir).
package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/nlink-jp/video-studio-mcp/internal/toolerr"
)

// DirOutput is the server-written subdirectory holding rendered video and the
// intermediate per-page segments.
const DirOutput = "output"

// Workspace is a validated, materialized per-deck directory.
type Workspace struct {
	ID      string
	BaseDir string
}

// Path joins parts under the workspace base directory. Use it for DISPLAY and
// for handing paths to external processes after VerifyRegular; all server-side
// file I/O must go through the os.Root-backed helpers below.
func (w *Workspace) Path(parts ...string) string {
	return filepath.Join(append([]string{w.BaseDir}, parts...)...)
}

// ResolveInside lexically validates an agent-supplied relative path and
// returns it cleaned (workspace-relative). It exists for early, friendly
// path_not_allowed errors; the enforcement boundary is os.Root.
func (w *Workspace) ResolveInside(rel string) (string, error) {
	if rel == "" {
		return "", toolerr.New(toolerr.CodePathNotAllowed, "path must not be empty")
	}
	if filepath.IsAbs(rel) {
		return "", toolerr.Newf(toolerr.CodePathNotAllowed,
			"path %q must be relative to the workspace root", rel)
	}
	cleaned := filepath.Clean(rel)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", toolerr.Newf(toolerr.CodePathNotAllowed,
			"path %q escapes the workspace root", rel)
	}
	return cleaned, nil
}

// openRoot opens the kernel-enforced containment anchor for this workspace.
func (w *Workspace) openRoot() (*os.Root, error) {
	r, err := os.OpenRoot(w.BaseDir)
	if err != nil {
		return nil, toolerr.Newf(toolerr.CodeWorkspaceFailed, "open workspace root: %v", err)
	}
	return r, nil
}

// rootErr converts os.Root escape errors into path_not_allowed so agents
// get the same stable code as the lexical pre-check.
func (w *Workspace) rootErr(op, rel string, err error) error {
	if err == nil {
		return nil
	}
	var pe *fs.PathError
	if errors.As(err, &pe) && strings.Contains(pe.Err.Error(), "escapes") {
		return toolerr.Newf(toolerr.CodePathNotAllowed,
			"%s %q: path escapes the workspace root (symlink?)", op, rel)
	}
	// Name the path that was looked at. A root-relative error says only
	// "openat narration.wav: no such file or directory", and the workspace is a
	// level below the work directory the caller named — not where an agent
	// naturally puts a file. The scribes and image-forge shipped the same bare
	// sentence, and a real agent (2026-09-14) answered it by inventing a
	// directory and spending four rounds recovering; the fix reached them and
	// not the two servers that share a workspace with each other. It stays an
	// fs.ErrNotExist for errors.Is.
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%s %q: not found — looked for %s. Paths are relative to the workspace, "+
			"which is <work_dir>/<workspace_id>/: write the file there with your own file tools "+
			"and pass the name it has inside the workspace: %w", op, rel, w.Path(rel), fs.ErrNotExist)
	}
	return err
}

// ReadFile reads a workspace-relative file with symlink containment.
func (w *Workspace) ReadFile(rel string) ([]byte, error) {
	r, err := w.openRoot()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	b, err := r.ReadFile(rel)
	return b, w.rootErr("read", rel, err)
}

// WriteFileAtomic writes a workspace-relative file via temp+rename, fully
// inside the containment root.
func (w *Workspace) WriteFileAtomic(rel string, data []byte) error {
	r, err := w.openRoot()
	if err != nil {
		return err
	}
	defer r.Close()
	if dir := filepath.Dir(rel); dir != "." {
		if err := r.MkdirAll(dir, 0o755); err != nil {
			// A path component replaced by a symlink (or file) surfaces as
			// ErrExist here because os.Root refuses to traverse it.
			if errors.Is(err, fs.ErrExist) {
				return toolerr.Newf(toolerr.CodePathNotAllowed,
					"mkdir %q: a path component is not a real directory (symlink?)", dir)
			}
			return w.rootErr("mkdir", dir, err)
		}
	}
	tmp := rel + ".tmp"
	if err := r.WriteFile(tmp, data, 0o644); err != nil {
		return w.rootErr("write", tmp, err)
	}
	if err := r.Rename(tmp, rel); err != nil {
		return w.rootErr("rename", rel, err)
	}
	return nil
}

// Stat stats a workspace-relative path with symlink containment.
func (w *Workspace) Stat(rel string) (fs.FileInfo, error) {
	r, err := w.openRoot()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	fi, err := r.Stat(rel)
	return fi, w.rootErr("stat", rel, err)
}

// MkdirAll creates a workspace-relative directory tree.
func (w *Workspace) MkdirAll(rel string) error {
	r, err := w.openRoot()
	if err != nil {
		return err
	}
	defer r.Close()
	return w.rootErr("mkdir", rel, r.MkdirAll(rel, 0o755))
}

// RemoveAll removes a workspace-relative tree.
func (w *Workspace) RemoveAll(rel string) error {
	r, err := w.openRoot()
	if err != nil {
		return err
	}
	defer r.Close()
	return w.rootErr("remove", rel, r.RemoveAll(rel))
}

// VerifyRegular confirms rel is a regular file (not a symlink) inside the
// workspace. Call it immediately before handing w.Path(rel) to an external
// process (ffmpeg) that cannot inherit os.Root. The remaining check-to-spawn
// race is accepted under the local single-user threat model.
//
// A missing file returns a non-path_not_allowed error (the underlying lstat
// error); a symlink or other non-regular entry returns path_not_allowed so
// callers can distinguish "not there yet" from "refused for safety".
func (w *Workspace) VerifyRegular(rel string) error {
	r, err := w.openRoot()
	if err != nil {
		return err
	}
	defer r.Close()
	fi, err := r.Lstat(rel)
	if err != nil {
		return w.rootErr("lstat", rel, err)
	}
	if !fi.Mode().IsRegular() {
		return toolerr.Newf(toolerr.CodePathNotAllowed,
			"%q is not a regular file (mode %s)", rel, fi.Mode())
	}
	return nil
}

// Manager materializes workspaces under the work directory a call names. It
// holds no default root of its own: a directory the caller cannot read back
// turns a successful call into a path to nothing (organization ADR-021;
// ADR-0008).
type Manager struct {
	check func(dir string) error
}

// NewManager returns a Manager. check judges <work_dir>/<workspace_id> — the
// directory actually used — before it is made or used: validating work_dir
// alone let work_dir=~/.config with workspace_id=gh land in ~/.config/gh. It
// is workdir.Resolver.CheckBeneath in the server; a Manager without one
// refuses every workspace.
func NewManager(check func(dir string) error) *Manager { return &Manager{check: check} }

// EnsureUnder materializes <workDir>/<id> and its subdirectories (idempotent).
// workDir must be an absolute path to an existing directory the caller can read
// back; workdir.Resolver.Resolve is what establishes that, and this method
// assumes it has already run.
func (m *Manager) EnsureUnder(workDir, id string) (*Workspace, error) {
	if !filepath.IsAbs(workDir) {
		return nil, toolerr.Newf(toolerr.CodeWorkDirInvalid,
			"work_dir %q must be an absolute path", workDir)
	}
	root := filepath.Clean(workDir)
	if m == nil || m.check == nil {
		return nil, toolerr.New(toolerr.CodeWorkDirDenied,
			"this server's workspace check was not set up (workspace.NewManager)")
	}
	if err := m.check(filepath.Join(root, id)); err != nil {
		return nil, err
	}
	return m.ensureUnder(root, id)
}

func (m *Manager) ensureUnder(root, id string) (*Workspace, error) {
	if err := ValidateID(id); err != nil {
		return nil, err
	}
	if err := makeWorkspaceDir(root, id); err != nil {
		return nil, err
	}
	w := &Workspace{ID: id, BaseDir: filepath.Join(root, id)}
	if err := w.MkdirAll(DirOutput); err != nil {
		return nil, err
	}
	return w, nil
}

// makeWorkspaceDir creates <workDir>/<id> and refuses a workspace whose
// directory is not really there. work_dir is the one path the caller vouched
// for; <id> beneath it may be a link, planted by any other tool with write
// access to work_dir. os.Root contains the operations performed *within* a
// root but resolves the root path itself normally, so a link at <id> would
// anchor every later read and write on the link's target while every result
// still reported success. The directory is made through an os.Root on work_dir,
// which refuses a path that leaves it, and what was made is then compared with
// what was asked for by real path, because the workspace path is later handed
// to ffmpeg, which resolves it outside any root.
func makeWorkspaceDir(workDir, id string) error {
	root, err := os.OpenRoot(workDir)
	if err != nil {
		return toolerr.Newf(toolerr.CodeWorkspaceFailed, "open work_dir: %v", err)
	}
	defer func() { _ = root.Close() }()
	// Mkdir, not MkdirAll: the work directory itself is the caller's and must
	// already exist, so a missing parent is a caller mistake worth hearing
	// about rather than a tree to conjure up.
	if err := root.Mkdir(id, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return toolerr.Newf(toolerr.CodeWorkspaceFailed, "create workspace dir: %v", err)
	}
	realWorkDir, err := filepath.EvalSymlinks(workDir)
	if err != nil {
		return toolerr.Newf(toolerr.CodeWorkspaceFailed, "resolve work_dir: %v", err)
	}
	got, err := filepath.EvalSymlinks(filepath.Join(workDir, id))
	if err != nil {
		return toolerr.Newf(toolerr.CodeWorkspaceFailed, "resolve workspace dir: %v", err)
	}
	if got != filepath.Join(realWorkDir, id) {
		return toolerr.Newf(toolerr.CodePathNotAllowed,
			"refused: workspace %q is a link to %s, not a directory under work_dir", id, got)
	}
	return nil
}
