# Architecture — video-studio-mcp

## Purpose

Turn a **page manifest** (still image + audio per page) into one **narrated
presentation video** (MP4). The server is a pure compositor: it neither renders
slides nor synthesizes speech. The agent prepares the assets; the server muxes
and concatenates them with ffmpeg.

## Module map

```
main.go                     → cmd.Execute
cmd/                        cobra: serve (default) / doctor / version
internal/
  jsonrpc/                  JSON-RPC 2.0 message types + standard codes
  transport/                newline-delimited stdio framing (1 MiB/line)
  mcpserver/                initialize / tools/list / tools/call routing;
                            RawResult sentinel for rich content; in-process Call
  logging/                  slog to stderr (+ optional rotated file)
  toolerr/                  structured {code,message,details} tool errors
  config/                   sectioned TOML, strict decode, ~ expansion
  workspace/                os.Root-contained per-deck dirs; agent-prepared roots
  manifest/                 page manifest JSONL parser (Page)
  master/                   the render pipeline (ffmpeg/ffprobe)
  tools/                    MCP tools: get_usage, master (+ embedded usage.md)
```

## Render pipeline (`internal/master`)

`Master.Build` is the whole pipeline. Given a validated `[]manifest.Page`:

1. **Preflight** — `exec.LookPath` ffmpeg and ffprobe → `ffmpeg_not_found`.
2. **Resolve + verify assets** — for each page, `ResolveInside` (lexical) then
   `VerifyRegular` (Lstat via `os.Root`). A symlink/non-regular entry →
   `path_not_allowed` (surfaced immediately); a missing file is collected and
   reported together as `manifest_incomplete`.
3. **Probe durations** — ffprobe `format=duration` per page audio; the sum is
   the reported total.
4. **Render segments** — one segment per page: still image looped, muxed with
   the audio, cut at `-t <duration>`, scaled to fit + padded onto the canvas,
   encoded to H.264/AAC/yuv420p with parameters from `[video]`. Every segment
   is encoded identically.
5. **Concat** — write the concat-demuxer list (quoting-safe), re-verify each
   segment as a regular file pre-spawn, then join by stream copy (`-c copy`).

The arg builders (`segmentArgs`, `concatArgs`, `probeArgs`, `concatList`) are
pure functions and are unit-tested directly. `Runner` abstracts command
execution so tests fake ffmpeg/ffprobe entirely.

Why two phases instead of one filtergraph: a per-page segment + concat-copy is
simple, linear in pages, and robust; a single N-input filtergraph grows complex
and re-encodes everything. Stream-copy concat requires codec-parameter
uniformity, which is exactly what step 4 guarantees.

## Workspace containment

One workspace = one deck under `<work_dir>/<id>/`. The root is the
**agent-prepared** absolute path every call passes as `work_dir`; the server
has no default of its own (organization ADR-021). Because that root is agent-writable, every
server-side file operation goes through `os.Root`, so a symlink planted inside
the workspace cannot make the server read or write outside it. A root, however,
resolves *its own* path normally, so the base directory is not protected by the
root anchored on it: `makeWorkspaceDir` creates `<id>` through an `os.Root` on
`work_dir` and then compares the base's real path against `<real work_dir>/<id>`,
refusing a workspace that turns out to be a link. The comparison is the
load-bearing half, because the path is handed to ffmpeg, which resolves it
outside any root. ffmpeg cannot inherit `os.Root`, so inputs are re-verified
with Lstat immediately before the spawn and the output path is cleared through
the root so a link left there cannot be followed; the verify-to-spawn race is
accepted under the local single-user threat model. It is not the only gap:
the workspace directory swapped for a link mid-render, and the formats ffmpeg
picks from a file's contents, are recorded in ADR-0009 and not closed here.

## Error model

Tool errors are `toolerr.Error{code, message, details}`. `errors.Is` matches by
`code`, so sentinels compose regardless of message. Codes: `invalid_arguments`,
`missing_argument`, `invalid_workspace_id`, `path_not_allowed`,
`invalid_manifest`, `manifest_incomplete`, `probe_failed`, `ffmpeg_failed`,
`ffmpeg_not_found`, `workspace_failed`. The recovery table in
`internal/tools/usage.md` is coherence-tested against these.

## Testing strategy

- **Hermetic** — `go test ./...` needs no ffmpeg. `master` tests use a faked
  `Runner` that returns a canned duration for ffprobe and materializes the
  output file for ffmpeg.
- **Pure-function** — arg builders and the manifest parser are tested directly.
- **In-process tool tests** — `mcpserver.Server.Call` invokes a tool by name,
  so `tools` tests exercise the real handler path (including work_dir and
  symlink rejection) without stdio framing.
- **Real ffmpeg** — validated manually on darwin (differently-sized slides
  letter/pillar-boxed onto 1920×1080; 1s + 2s audio → ~3s MP4).
