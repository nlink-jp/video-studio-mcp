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

> **Status:** Released. Tools: `get_usage`, `master`, `check_job`. Captions ship
> in both forms — burned-in (`captions`) and a closed-caption track
> (`soft_captions`) — and `transition: "fade"` dips between pages. A true
> cross-page dissolve is out of scope (it would break the exact-duration
> guarantee; see [ADR-0007](docs/en/adr/0007-fade-transitions.md)). See
> [CHANGELOG.md](CHANGELOG.md) for the current version.

## Why an MCP server (not a CLI)

The intended client is Claude Code / Cowork. A Cowork VM sandbox typically
cannot launch a local CLI, but it *can* call a registered MCP tool — so the
MCP interface is the product. A `serve` subcommand is the entry point; the CLI
side (`doctor`, `version`) is for local diagnostics. The version is also
available as `--version`, which prints the same string as `version`.

## Requirements

- **ffmpeg** and **ffprobe** on `PATH` (`brew install ffmpeg`). ffprobe ships
  with ffmpeg and is used to measure each page's audio duration.
- Go 1.25+ to build.

## Install / build

```sh
make build      # → dist/video-studio-mcp (auto-codesign on darwin)
make test       # go test ./...  (hermetic: no ffmpeg required)
make package    # cross-compile 4 platforms (darwin arm64 only) + zip/tar.gz + notarize darwin
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

One workspace = one deck: `<work_dir>/<workspace_id>/`

```
<manifest>.jsonl   page manifest        (you write this)
images/…           page still images    (you place these)
audio/…            page audio files     (you place these)
output/            rendered mp4 + tmp    (server-written)
```

`work_dir` is an absolute path to a directory you prepared (create it and
drop the assets in with your own file tools), passed on the `master` call. It
is required and has no default: a directory the server picked is one you may
not be able to open, which would make the returned path useless. Asset paths in
the manifest are relative to the workspace root. The server never reads or writes outside
the workspace (kernel-enforced via `os.Root`; symlinks pointing out are
rejected with `path_not_allowed`). The workspace directory itself is checked
before any I/O: if `<work_dir>/<id>` is a symlink rather than a real directory,
the call is refused and names what the id resolved to, instead of running the
whole render against the link's target.

## Tools

| Tool | Purpose |
|------|---------|
| `get_usage` | Return the operating manual (workspace model, manifest schema, recovery table). Call once before rendering. |
| `master` | Build one MP4 from a page manifest. Args: `workspace_id`, `manifest_path`, optional `work_dir`, `output_name`, `chapters` (default true), `captions` / `soft_captions` (default false), `width`/`height`/`fps` (canvas override), `fade_seconds` (default 0.5), `keep_intermediates` (default false), `async` (default false). |
| `check_job` | Poll an async render: `state`, page progress, and — when `done` — the same result `master` returns synchronously. |

### Output size / aspect

The canvas defaults to the server's `[video]` config (1920×1080). Override it
per render with `width`/`height`/`fps` (even dimensions ≥16) to produce the same
deck as **16:9** (1920×1080), **9:16** vertical (1080×1920), or **1:1**
(1080×1080) — useful for social distribution. Images are always
letter/pillar-boxed to fit. The per-page intermediates under `output/tmp` are
discarded after a successful render (`keep_intermediates: true` to keep them).

### Captions

The manifest `caption` can be shown two independent ways (both default off):

- **Burned-in** (`captions: true`) — always-on subtitle pixels, visible even in
  muted social autoplay. Rendered in Go with a bundled Japanese font (M PLUS 1p)
  and composited via ffmpeg's core `overlay` filter, so it works even on an
  ffmpeg built without `drawtext`/libfreetype. Styling — font size, colors, box,
  margins — is set in the server's `[caption]` config.
- **Closed captions** (`soft_captions: true`) — a toggleable `mov_text` subtitle
  track the viewer turns on/off; player-rendered, no font needed. Hidden by
  default in muted autoplay, but selectable and accessible on demand.

Enable both for burned-in pixels plus a selectable track.

### Transitions

A page whose `transition` is `"fade"` fades out at its end, and the next page
fades in at its start, dipping to the canvas `background` colour. Both fades
happen **inside each page's own duration** — pages never overlap — so total
duration stays the sum of the audio durations and chapter/caption timing is
untouched. The flip side: the narration keeps playing while the image dims, so
leave a little trailing silence in the audio when that matters.

`fade_seconds` (per call, or `[video] fade_seconds`; default `0.5`) sets the
length. It is clamped so at least half of every page stays at full brightness —
a 0.5 s fade on a 0.8 s page becomes 0.2 s on each side — and a fade shorter
than one frame is dropped. `fade_seconds: 0` renders every boundary as a cut.
The last page's `transition` is ignored: there is no next page. `master` reports
`fades_applied`.

Audio is never faded, and there is no cross-page dissolve — an `xfade` would
overlap the pages and break the exact-duration guarantee
([ADR-0007](docs/en/adr/0007-fade-transitions.md)).

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
- `caption` (optional) — subtitle text **burned into the video** when `master`
  is called with `captions: true` (ignored otherwise).
- `transition` (optional) — how this page joins the **next** one: `cut`
  (default) or `fade`. Ignored on the last page (no next page to join).

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

## Fonts

This tool bundles the **M PLUS 1p** font
([internal/caption/fonts/MPLUS1p-Regular.ttf](internal/caption/fonts/MPLUS1p-Regular.ttf))
for burned-in captions, licensed under the SIL Open Font License, Version 1.1.
We are grateful to the M+ FONTS Project for this excellent font. See
[FONTS_LICENSE](FONTS_LICENSE).

## License

MIT — see [LICENSE](LICENSE). The bundled M PLUS 1p font is under the SIL Open
Font License 1.1 (see [Fonts](#fonts) and [FONTS_LICENSE](FONTS_LICENSE)).
