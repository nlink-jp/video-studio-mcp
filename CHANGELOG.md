# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/), and the project adheres to
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- Phase 1 scaffold ported from `voice-studio-mcp` / `data-toolbox-mcp`:
  MCP stdio server skeleton (`jsonrpc`, `transport`, `mcpserver`, `logging`,
  `toolerr`), sectioned TOML config with strict decode, and `os.Root`-contained
  agent-prepared workspaces.
- `master` tool — builds one presentation video (MP4) from a page manifest:
  probes each page's audio duration, renders one H.264/AAC segment per page
  (still image looped for the audio's length, scaled + padded onto the canvas),
  and concatenates the segments by stream copy.
- Per-page **chapter markers** in the MP4 (default on; `chapters: false` to
  disable). Chapter title comes from the manifest `title` field, else `Page N`;
  boundaries follow the per-page durations (ffmetadata via the concat step).
- **Async rendering** (Phase 2): `master` takes `async: true` to render in the
  background and return a `job_id`; the new `check_job` tool reports state and
  page progress and, when done, the full result. Jobs are in-memory
  (`internal/job`), not persisted; `job_not_found` guides recovery (re-run
  master). ADR-0003.
- **Per-call canvas override** (Phase 2): `master` takes `width`/`height`/`fps`
  to override the output size for one render (even dims ≥16), so the same deck
  can be produced as 16:9 / 9:16 / 1:1 without touching server config. Invalid
  overrides fail fast with `invalid_arguments`. ADR-0005.
- **Intermediate cleanup** (Phase 2): per-page segments and caption PNGs under
  `output/tmp` are now discarded after a successful render; `keep_intermediates:
  true` retains them for debugging. (Previously `output/tmp` accumulated.)
- **Burned-in captions** (Phase 2): `master` takes `captions: true` to burn each
  page's manifest `caption` into the video (always-on; visible in muted
  autoplay). Rendered in Go with the bundled **M PLUS 1p** font (SIL OFL 1.1,
  `internal/caption`) to a transparent overlay and composited with ffmpeg's core
  `overlay` — no `drawtext`/libfreetype required. Styling via `[caption]` config;
  default off. ADR-0004.
- `get_usage` tool — embedded operating manual (`internal/tools/usage.md`),
  coherence-tested against the real tool/error/schema surface.
- Page manifest JSONL parser (`internal/manifest`) with strict decode and
  collected per-line validation errors.
- `doctor` subcommand — checks config, ffmpeg, ffprobe, and the workspace dir.
- Hermetic tests (no ffmpeg required) via a faked `Runner`; validated end to
  end against real ffmpeg on darwin.

### Notes

- Captions (`caption` field) and non-cut transitions (`transition: fade`) are
  accepted by the manifest but deferred to Phase 2 — captions require bundling a
  CJK font (Noto Sans JP / OFL). See the RFP.
