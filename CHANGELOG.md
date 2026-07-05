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
