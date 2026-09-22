package tools

import (
	"context"
	"encoding/json"
	"errors"
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
  "required": ["work_dir", "workspace_id", "manifest_path"],
  "properties": {
    "workspace_id": {"type": "string", "description": "One deck per workspace; [a-zA-Z0-9_-]{1,64}"},
    "work_dir": {"type": "string", "description": "Absolute path to a directory you can read back \u2014 your session or working directory. The workspace is <work_dir>/<workspace_id>/ and the rendered video lands under it, so a directory you cannot open leaves you holding a path to nothing. It must already exist, and nothing here expands ~ or resolves a relative path. Pass the same work_dir the images and audio were produced under."},
    "manifest_path": {"type": "string", "description": "Page manifest JSONL path relative to the workspace root"},
    "output_name": {"type": "string", "description": "Output basename without extension (default: manifest file name)"},
    "chapters": {"type": "boolean", "description": "Emit one per-page chapter marker in the MP4 (default true); page title comes from the manifest \"title\" field, else \"Page N\""},
    "captions": {"type": "boolean", "description": "Burn each page's manifest \"caption\" into the video, always-on (default false). Styling is controlled by the server's [caption] config."},
    "soft_captions": {"type": "boolean", "description": "Embed each page's manifest \"caption\" as a toggleable closed-caption track (mov_text soft subtitles) instead of/in addition to burning them in (default false). Player-rendered, no font needed."},
    "width": {"type": "integer", "description": "Override the output canvas width (even, >=16) for this render; e.g. 1080 with height 1920 for 9:16 vertical. Default: server [video] config."},
    "height": {"type": "integer", "description": "Override the output canvas height (even, >=16) for this render. Default: server [video] config."},
    "fps": {"type": "integer", "description": "Override the output frame rate (>=1) for this render. Default: server [video] config."},
    "fade_seconds": {"type": "number", "description": "Length of one fade at a page boundary whose manifest \"transition\" is \"fade\" (default: server [video] config, 0.5). The fade dips to the canvas background inside each page's own duration, so total duration and chapter timing are unaffected; it is clamped so at least half of every page stays at full brightness. 0 disables fading (every boundary becomes a cut)."},
    "keep_intermediates": {"type": "boolean", "description": "Keep the per-page segments and caption PNGs under output/tmp after a successful render (default false = discard)."},
    "async": {"type": "boolean", "description": "Render in the background and return a job_id immediately (default false); poll check_job for progress and the result. Use for long decks that would otherwise block the call."}
  },
  "additionalProperties": false
}`),
	}, func(ctx context.Context, args json.RawMessage) (any, error) {
		in := struct {
			WorkspaceID       string   `json:"workspace_id"`
			WorkDir           string   `json:"work_dir"`
			ManifestPath      string   `json:"manifest_path"`
			OutputName        string   `json:"output_name"`
			Chapters          *bool    `json:"chapters"`
			Captions          *bool    `json:"captions"`
			SoftCaptions      *bool    `json:"soft_captions"`
			Width             *int     `json:"width"`
			Height            *int     `json:"height"`
			FPS               *int     `json:"fps"`
			FadeSeconds       *float64 `json:"fade_seconds"`
			KeepIntermediates *bool    `json:"keep_intermediates"`
			Async             *bool    `json:"async"`
		}{}
		if err := unmarshalStrict(args, &in); err != nil {
			return nil, err
		}
		chapters := true
		if in.Chapters != nil {
			chapters = *in.Chapters
		}
		captions := in.Captions != nil && *in.Captions
		softCaptions := in.SoftCaptions != nil && *in.SoftCaptions
		keepIntermediates := in.KeepIntermediates != nil && *in.KeepIntermediates
		async := in.Async != nil && *in.Async

		// Per-call canvas / fade override on top of the server [video] config.
		video := d.Cfg.Video
		if in.Width != nil {
			video.Width = *in.Width
		}
		if in.Height != nil {
			video.Height = *in.Height
		}
		if in.FPS != nil {
			video.FPS = *in.FPS
		}
		if in.FadeSeconds != nil {
			video.FadeSeconds = *in.FadeSeconds
		}
		if err := video.Validate(); err != nil {
			return nil, toolerr.Newf(toolerr.CodeInvalidArguments, "invalid render override: %v", err)
		}
		if in.WorkspaceID == "" {
			return nil, toolerr.New(toolerr.CodeMissingArgument, "workspace_id is required")
		}
		if in.ManifestPath == "" {
			return nil, toolerr.New(toolerr.CodeMissingArgument, "manifest_path is required")
		}
		if in.OutputName != "" && (strings.ContainsAny(in.OutputName, `/\`) || strings.Contains(in.OutputName, "..")) {
			return nil, toolerr.Newf(toolerr.CodeInvalidArguments, "output_name must be a plain file name, got %q", in.OutputName)
		}

		workDir, err := d.WorkDir.Resolve(ctx, in.WorkDir)
		if err != nil {
			return nil, err
		}
		ws, err := d.WS.EnsureUnder(workDir, in.WorkspaceID)
		if err != nil {
			return nil, err
		}
		manRel, err := ws.ResolveInside(in.ManifestPath)
		if err != nil {
			return nil, err
		}
		data, err := ws.ReadFile(manRel)
		if err != nil {
			// A refusal is returned as it is: wrapped as invalid_manifest it
			// would read like a missing manifest.
			if errors.Is(err, toolerr.New(toolerr.CodePathNotAllowed, "")) {
				return nil, err
			}
			return nil, toolerr.Newf(toolerr.CodeInvalidManifest, "read manifest %q: %v", in.ManifestPath, err)
		}
		pages, err := manifest.Parse(data)
		if err != nil {
			return nil, err
		}
		// Every image and audio the manifest names is judged here, before
		// the render looks for any of them or hands one to ffmpeg — on the
		// call, so an async render is refused before a job exists.
		if err := master.CheckNames(ws, pages); err != nil {
			return nil, err
		}
		for _, p := range pages {
			for _, name := range []string{p.Image, p.Audio} {
				rel, err := ws.ResolveInside(name)
				if err != nil {
					return nil, err
				}
				if err := ws.Judge(rel); err != nil {
					return nil, err
				}
			}
		}

		stem := strings.TrimSuffix(filepath.Base(in.ManifestPath), filepath.Ext(in.ManifestPath))
		m := &master.Master{Runner: d.Runner, Cfg: video, Caption: d.Cfg.Caption}
		opts := master.Options{
			OutputName:       in.OutputName,
			Chapters:         chapters,
			Captions:         captions,
			SoftCaptions:     softCaptions,
			KeepIntermediate: keepIntermediates,
		}

		if async {
			total := len(pages)
			jobID := d.Jobs.Submit(d.JobCtx, total, func(ctx context.Context, report func(job.Progress)) (any, error) {
				o := opts
				o.OnProgress = func(phase string, done, total int) {
					report(job.Progress{Phase: phase, Done: done, Total: total})
				}
				return m.Build(ctx, ws, stem, pages, o)
			})
			return map[string]any{"job_id": jobID, "state": job.StateRunning, "pages": total}, nil
		}

		return m.Build(ctx, ws, stem, pages, opts)
	})
}
