// Package tools implements the MCP tools exposed by video-studio-mcp.
//
// Every tool returns a compact JSON summary (paths, counts, durations) —
// never video bytes — to keep token cost low for LLM clients. The rendered
// file is played by the human user on this host.
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"

	"github.com/nlink-jp/video-studio-mcp/internal/config"
	"github.com/nlink-jp/video-studio-mcp/internal/job"
	"github.com/nlink-jp/video-studio-mcp/internal/master"
	"github.com/nlink-jp/video-studio-mcp/internal/mcpserver"
	"github.com/nlink-jp/video-studio-mcp/internal/toolerr"
	"github.com/nlink-jp/video-studio-mcp/internal/workspace"
)

// Deps carries the shared dependencies of all tools.
type Deps struct {
	Cfg *config.Config
	WS  *workspace.Manager
	// Runner executes ffmpeg/ffprobe for the master tool (fake in tests).
	Runner master.Runner
	// Jobs tracks background (async) renders.
	Jobs *job.Manager
	// JobCtx is the server-lifetime context async renders run under; tying them
	// to the per-request ctx would abort the render the moment the tool call
	// returns.
	JobCtx context.Context
	Logger *slog.Logger
}

// Register attaches all tools to the MCP server.
func Register(srv *mcpserver.Server, d *Deps) {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Runner == nil {
		d.Runner = master.ExecRunner{}
	}
	if d.Jobs == nil {
		d.Jobs = job.NewManager()
	}
	if d.JobCtx == nil {
		d.JobCtx = context.Background()
	}
	registerGetUsage(srv, d)
	registerMaster(srv, d)
	registerCheckJob(srv, d)
}

// unmarshalStrict decodes tool arguments, rejecting unknown fields so agent
// typos surface as invalid_arguments instead of being silently ignored.
func unmarshalStrict(args json.RawMessage, into any) error {
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return toolerr.Newf(toolerr.CodeInvalidArguments, "invalid arguments: %v", err)
	}
	return nil
}
