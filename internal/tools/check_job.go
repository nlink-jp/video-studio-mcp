package tools

import (
	"context"
	"encoding/json"

	"github.com/nlink-jp/video-studio-mcp/internal/mcpserver"
	"github.com/nlink-jp/video-studio-mcp/internal/toolerr"
)

func registerCheckJob(srv *mcpserver.Server, d *Deps) {
	srv.RegisterTool(mcpserver.Tool{
		Name: "check_job",
		Description: "Check the progress of an async master render: state (running/done/failed), page progress, " +
			"and — when done — the same result master returns synchronously. Jobs do not survive a server restart; " +
			"if the job_id is unknown, re-run master (it re-renders from the same workspace).",
		InputSchema: json.RawMessage(`{
  "type": "object",
  "required": ["job_id"],
  "properties": {
    "job_id": {"type": "string"}
  },
  "additionalProperties": false
}`),
	}, func(ctx context.Context, args json.RawMessage) (any, error) {
		var in struct {
			JobID string `json:"job_id"`
		}
		if err := unmarshalStrict(args, &in); err != nil {
			return nil, err
		}
		if in.JobID == "" {
			return nil, toolerr.New(toolerr.CodeMissingArgument, "job_id is required")
		}
		return d.Jobs.Get(in.JobID)
	})
}
