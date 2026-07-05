# video-studio-mcp

An MCP stdio server that assembles a **narrated presentation video** (MP4)
from a page manifest: each page pairs a still **image** with its **audio**, and
the pages are concatenated in order via ffmpeg. Each page is shown for exactly
the length of its audio, so video/audio synchronization is exact by
construction.

`video-studio-mcp` is a **pure compositor**. It does not create slides or
synthesize speech — you (the agent) render each page to an image with your own
tools and produce the audio upstream (e.g. with
[voice-studio-mcp](https://github.com/nlink-jp/voice-studio-mcp)), place the
assets in a workspace, and this server muxes and concatenates them.

```
voice-studio-mcp  ─▶  page audio ─┐
                                  ├─▶  video-studio-mcp (master) ─▶  deck.mp4
slides ─▶ per-page images ────────┘
```

> **Status:** Phase 1 scaffold. Tools: `get_usage`, `master`. Captions and
> transitions are Phase 2 (see the RFP).

## Why an MCP server (not a CLI)

The intended client is Claude Code / Cowork. A Cowork VM sandbox typically
cannot launch a local CLI, but it *can* call a registered MCP tool — so the
MCP interface is the product. A `serve` subcommand is the entry point; the CLI
side (`doctor`, `version`) is for local diagnostics.

## Requirements

- **ffmpeg** and **ffprobe** on `PATH` (`brew install ffmpeg`). ffprobe ships
  with ffmpeg and is used to measure each page's audio duration.
- Go 1.25+ to build.

## Install / build

```sh
make build      # → dist/video-studio-mcp (auto-codesign on darwin)
make test       # go test ./...  (hermetic: no ffmpeg required)
make package    # cross-compile 5 platforms + zip + notarize darwin
```

Verify the environment:

```sh
dist/video-studio-mcp doctor
```

## Register with an MCP client

```json
{
  "mcpServers": {
    "video-studio": {
      "command": "/absolute/path/to/video-studio-mcp",
      "args": ["serve"]
    }
  }
}
```

The workspace directory must be reachable by the server process (the same
filesystem where the agent places the images, audio, and manifest).

## Workspace model

One workspace = one deck: `<workspace_root>/<workspace_id>/`

```
<manifest>.jsonl   page manifest        (you write this)
images/…           page still images    (you place these)
audio/…            page audio files     (you place these)
output/            rendered mp4 + tmp    (server-written)
```

`workspace_root` is an absolute path to a directory you prepared (create it and
drop the assets in with your own file tools), passed on the `master` call. Omit
it to use the server default (`~/.video-studio`). Asset paths in the manifest
are relative to the workspace root. The server never reads or writes outside
the workspace (kernel-enforced via `os.Root`; symlinks pointing out are
rejected with `path_not_allowed`).

## Tools

| Tool | Purpose |
|------|---------|
| `get_usage` | Return the operating manual (workspace model, manifest schema, recovery table). Call once before rendering. |
| `master` | Build one MP4 from a page manifest. Args: `workspace_id`, `manifest_path`, optional `workspace_root`, `output_name`, `chapters` (default true), `async` (default false). |
| `check_job` | Poll an async render: `state`, page progress, and — when `done` — the same result `master` returns synchronously. |

### Async rendering

For a long deck, pass `async: true` to `master`: it returns a `job_id`
immediately and renders in the background. Poll `check_job` with that `job_id`
for `state` (`running`/`done`/`failed`) and page progress; when `done`, the
status carries the full result. Jobs are in-memory and do not survive a server
restart (an unknown `job_id` returns `job_not_found` — just re-run `master`).

## Page manifest (JSONL, one page per line)

```jsonl
{"image":"images/p01.png","audio":"audio/p01.wav","title":"はじめに"}
{"image":"images/p02.png","audio":"audio/p02.wav","caption":"見出し","transition":"cut"}
```

- `image` (required) — workspace-relative still image (PNG/JPG).
- `audio` (required) — workspace-relative narration audio; its duration sets the
  page's on-screen time.
- `title` (optional) — the page's **chapter-marker** name (default `Page N`).
- `caption` (optional) — **Phase 2**: subtitle burn-in. Accepted but not yet
  rendered.
- `transition` (optional) — `cut` (default). `fade` is accepted but rendered as
  a hard cut in Phase 1.

Blank lines and `#` comment lines are ignored.

## Output

`output/<name>.mp4` — H.264 / AAC / yuv420p, **1920×1080 @ 30fps** by default
(configurable in `[video]`). Each page is scaled to fit and padded
(letter/pillar-boxed) onto the canvas; total duration is the sum of the page
audio durations. Defaults target broad playback compatibility (QuickTime,
browsers, social platforms).

By default the MP4 carries **one chapter marker per page** (title from the
manifest `title`, else `Page N`), so viewers can jump between slides in players
that support chapters. Pass `chapters: false` to disable.

## Configuration

Copy [`config.example.toml`](config.example.toml) to
`~/.config/video-studio-mcp/config.toml`. Every key is optional; the shown
values are the defaults. Canvas size, fps, x264 `crf`/`preset`, audio bitrate,
and the pad `background` color are all configurable.

## Documentation

- [Architecture](docs/en/reference/architecture.md) — module map, render
  pipeline, error model, testing strategy.
- [ADR-0001: two-phase render pipeline](docs/en/adr/0001-render-pipeline.md).
- [RFP](docs/en/video-studio-mcp-rfp.md) — approved scope; canonical source for
  design decisions.

## License

See the repository license. Bundled fonts (Phase 2, for caption burn-in) will
carry their own OFL attribution.
