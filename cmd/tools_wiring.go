package cmd

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/nlink-jp/video-studio-mcp/internal/config"
	"github.com/nlink-jp/video-studio-mcp/internal/master"
	"github.com/nlink-jp/video-studio-mcp/internal/tools"
	"github.com/nlink-jp/video-studio-mcp/internal/workdir"
	"github.com/nlink-jp/video-studio-mcp/internal/workspace"
)

// newToolDeps assembles what every tool shares. It is the only place
// tools.Deps is built, which is what makes the work-directory resolver below
// impossible for a tool added later to forget: no tool constructs a Resolver
// of its own, they all read Deps.WorkDir.
//
// ctx is the server-lifetime context: async renders outlive the tool call
// that started them but must stop on shutdown.
func newToolDeps(ctx context.Context, cfg *config.Config, logger *slog.Logger) *tools.Deps {
	return &tools.Deps{
		Cfg:     cfg,
		WS:      workspace.NewManager(),
		WorkDir: workDirResolver(),
		Runner:  master.ExecRunner{},
		JobCtx:  ctx,
		Logger:  logger,
	}
}

// workDirResolver builds the per-call work-directory resolver, denying this
// server's own directories.
//
// A work directory is the caller's, not ours (organization ADR-021 §4: "not a
// system location … and not the server's own config or state directory" →
// `work_dir_denied`). Without the denial a caller could name our config
// directory as its workspace root and have ffmpeg write segments, concat lists
// and the rendered MP4 in among our own configuration, on a model's say-so.
func workDirResolver() workdir.Resolver {
	return workdir.Resolver{Denied: serverOwnedDirs()}
}

// serverOwnedDirs lists this server's own config and state directories.
//
// There is one: the config directory. This server keeps no state on disk — it
// is a pure compositor, every byte it produces goes under the caller's
// `work_dir`, and the `~/.video-studio` default root that used to exist was
// deleted when ADR-021 was adopted. If a state directory is ever
// reintroduced it belongs here.
func serverOwnedDirs() []string {
	dir := configDir()
	if dir == "" {
		return nil
	}
	return []string{dir}
}

// configDir is where this server keeps its own config.toml.
//
// resolveConfig searches it and workDirResolver denies it, through this one
// expression: spelling the path twice is how a denial drifts away from the
// location it was meant to protect. Empty when the home directory cannot be
// determined.
func configDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "video-studio-mcp")
}
