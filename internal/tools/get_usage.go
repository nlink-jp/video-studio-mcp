package tools

import (
	"context"
	_ "embed"
	"encoding/json"

	"github.com/nlink-jp/video-studio-mcp/internal/mcpserver"
)

// usageMarkdown is the client-neutral operating manual returned by get_usage:
// clients without the (future) bundled workflow skill should not have to
// operate this stateful, file-mediated server by trial and error. Coherence
// with the real tools/errors/schema is pinned by usage_test.go.
//
//go:embed usage.md
var usageMarkdown string

// Instructions is the short initialize-time hint that makes get_usage
// discoverable (surfaced via the MCP `instructions` field).
const Instructions = "video-studio-mcp assembles a narrated presentation video (MP4) from a page manifest: " +
	"each page pairs a still image with its audio, and the pages are concatenated in order. " +
	"It is a pure compositor — slide rendering and audio synthesis happen upstream " +
	"(e.g. voice-studio-mcp supplies the audio). Every call names work_dir: the absolute path of a " +
	"directory you can read back (your session or working directory). It is required and has no default, " +
	"and the workspace is <work_dir>/<workspace_id>/ — the same one the upstream media servers wrote into. " +
	"It is stateful and file-mediated: you place images, audio, and the manifest in that workspace; " +
	"outputs are returned as file paths. " +
	"Call the get_usage tool before your first render to learn the workspace model, the manifest schema, " +
	"and the recovery table."

func registerGetUsage(srv *mcpserver.Server, d *Deps) {
	srv.RegisterTool(mcpserver.Tool{
		Name: "get_usage",
		Description: "Return this server's operating manual (markdown): workspace model and work_dir, " +
			"the page manifest JSONL schema, the render flow, and the error recovery table. " +
			"Call it once before your first render.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	}, func(ctx context.Context, args json.RawMessage) (any, error) {
		var in struct{}
		if err := unmarshalStrict(args, &in); err != nil {
			return nil, err
		}
		return mcpserver.RawResult{
			Content: []mcpserver.ContentBlock{{Type: "text", Text: usageMarkdown}},
		}, nil
	})
}
