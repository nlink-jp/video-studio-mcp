package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/nlink-jp/video-studio-mcp/internal/job"
	"github.com/nlink-jp/video-studio-mcp/internal/manifest"
	"github.com/nlink-jp/video-studio-mcp/internal/master"
	"github.com/nlink-jp/video-studio-mcp/internal/mcpserver"
	"github.com/nlink-jp/video-studio-mcp/internal/toolerr"
)

func registerMaster(srv *mcpserver.Server, d *Deps) {
	srv.RegisterTool(mcpserver.Tool{
		Name: "master",
		Description: "Build one narrated presentation video (MP4) from a page manifest (JSONL). Each page's still " +
			"image is muxed with its audio for exactly the audio's duration; the pages are concatenated in manifest " +
			"order. Returns the output path, page count, and total duration. Fails with manifest_incomplete if a " +
			"referenced image or audio file is missing from the workspace, or invalid_manifest if the manifest is malformed.",
		InputSchema: json.RawMessage(`{
  "type": "object",
  "required": ["workspace_id", "manifest_path"],
  "properties": {
    "workspace_id": {"type": "string", "description": "One deck per workspace; [a-zA-Z0-9_-]{1,64}"},
    "workspace_root": {"type": "string", "description": "Absolute path to an agent-prepared workspace root directory (create it first with your own file tools); omit to use the server-configured default (~/.video-studio)"},
    "manifest_path": {"type": "string", "description": "Page manifest JSONL path relative to the workspace root"},
    "output_name": {"type": "string", "description": "Output basename without extension (default: manifest file name)"},
    "chapters": {"type": "boolean", "description": "Emit one per-page chapter marker in the MP4 (default true); page title comes from the manifest \"title\" field, else \"Page N\""},
    "captions": {"type": "boolean", "description": "Burn each page's manifest \"caption\" into the video, always-on (default false). Styling is controlled by the server's [caption] config."},
    "async": {"type": "boolean", "description": "Render in the background and return a job_id immediately (default false); poll check_job for progress and the result. Use for long decks that would otherwise block the call."}
  },
  "additionalProperties": false
}`),
	}, func(ctx context.Context, args json.RawMessage) (any, error) {
		in := struct {
			WorkspaceID   string `json:"workspace_id"`
			WorkspaceRoot string `json:"workspace_root"`
			ManifestPath  string `json:"manifest_path"`
			OutputName    string `json:"output_name"`
			Chapters      *bool  `json:"chapters"`
			Captions      *bool  `json:"captions"`
			Async         *bool  `json:"async"`
		}{}
		if err := unmarshalStrict(args, &in); err != nil {
			return nil, err
		}
		chapters := true
		if in.Chapters != nil {
			chapters = *in.Chapters
		}
		captions := in.Captions != nil && *in.Captions
		async := in.Async != nil && *in.Async
		if in.WorkspaceID == "" {
			return nil, toolerr.New(toolerr.CodeMissingArgument, "workspace_id is required")
		}
		if in.ManifestPath == "" {
			return nil, toolerr.New(toolerr.CodeMissingArgument, "manifest_path is required")
		}
		if in.OutputName != "" && (strings.ContainsAny(in.OutputName, `/\`) || strings.Contains(in.OutputName, "..")) {
			return nil, toolerr.Newf(toolerr.CodeInvalidArguments, "output_name must be a plain file name, got %q", in.OutputName)
		}

		ws, err := d.WS.EnsureIn(in.WorkspaceRoot, in.WorkspaceID)
		if err != nil {
			return nil, err
		}
		manRel, err := ws.ResolveInside(in.ManifestPath)
		if err != nil {
			return nil, err
		}
		data, err := ws.ReadFile(manRel)
		if err != nil {
			return nil, toolerr.Newf(toolerr.CodeInvalidManifest, "read manifest %q: %v", in.ManifestPath, err)
		}
		pages, err := manifest.Parse(data)
		if err != nil {
			return nil, err
		}

		stem := strings.TrimSuffix(filepath.Base(in.ManifestPath), filepath.Ext(in.ManifestPath))
		m := &master.Master{Runner: d.Runner, Cfg: d.Cfg.Video, Caption: d.Cfg.Caption}

		if async {
			total := len(pages)
			jobID := d.Jobs.Submit(d.JobCtx, total, func(ctx context.Context, report func(job.Progress)) (any, error) {
				return m.Build(ctx, ws, stem, pages, master.Options{
					OutputName: in.OutputName,
					Chapters:   chapters,
					Captions:   captions,
					OnProgress: func(phase string, done, total int) {
						report(job.Progress{Phase: phase, Done: done, Total: total})
					},
				})
			})
			return map[string]any{"job_id": jobID, "state": job.StateRunning, "pages": total}, nil
		}

		return m.Build(ctx, ws, stem, pages, master.Options{OutputName: in.OutputName, Chapters: chapters, Captions: captions})
	})
}
