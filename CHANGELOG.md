# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/), and the project adheres to
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Fixed

- **`--version` now works.** The binary only implemented a `version`
  subcommand, so `video-studio-mcp --version` failed with "unknown flag" — and
  the shared org homebrew formula template tests exactly that invocation, so
  `brew test video-studio-mcp` failed. `rootCmd.Version` is now set, which
  makes cobra provide the flag. The `version` subcommand is unchanged, and both
  spellings print the identical string (bare version, no "<name> version "
  prefix).

## [0.4.0] - 2026-07-26

### Added

- **Fade transitions** (ADR-0007). A page whose manifest `transition` is
  `"fade"` now fades out at its end while the next page fades in at its start,
  dipping to the canvas `background` colour — the field is no longer accepted
  and then ignored. Both fades render **inside each page's own duration**, so
  the total is still the sum of the audio durations and the chapter / caption
  timelines are untouched; the final concat remains a stream copy.
- `fade_seconds` — new `[video]` config key and per-call `master` argument
  (default `0.5`). Clamped so at least half of every page stays at full
  brightness (one fade ≤ duration/2, two ≤ duration/4 each); a fade shorter than
  one frame is dropped; `0` renders every boundary as a cut.
- `master` result now reports `fades_applied` (page boundaries rendered as a
  fade).

### Notes

- The last page's `transition` is ignored — it describes the boundary *into the
  next page*, and there is none. A trailing fade-out is an outro, deliberately
  left to a future option.
- Audio is never faded: narration must not be clipped, and the audio length is
  what defines a page's duration.
- A true cross-page dissolve (`xfade`) remains out of scope — it would overlap
  pages and break the exact-duration guarantee. See ADR-0007 for the analysis.

## [0.3.0] - 2026-07-12

### Removed

- **darwin/amd64 (Intel) pre-built binary.** macOS releases now ship
  **arm64 only**, per the org-wide policy (darwin is Apple-Silicon only; no
  universal binaries). Intel Mac users can build from source.

### Changed

- **Linux release archives are now `.tar.gz`** (darwin/windows remain `.zip`),
  per `nlink-jp/.github` CONVENTIONS.md §Release Archive Standard. Archives
  still bundle `LICENSE` + `README.md` + `FONTS_LICENSE` alongside the binary.
- **darwin code-signature identifier** is now the canonical `video-studio-mcp`
  (was `video-studio-mcp-darwin-arm64`), set via `codesign -i` so it stays
  stable after the archived binary is renamed to its canonical name.
- **Dropped the `-s -w` linker strip flags**, aligning `LDFLAGS` with the
  org-standard form; also avoids a false-positive antivirus quarantine of
  the stripped Windows binary during cross-build.

No change to the binary's behaviour — a packaging / build-config release.

## [0.2.1] - 2026-07-06

### Added

- MIT `LICENSE` file (the repository shipped without one). The `README` license
  sections now point to it.

### Changed

- Release zips bundle `LICENSE` and `FONTS_LICENSE` alongside the binary and
  README, so the embedded M PLUS 1p font's SIL OFL license travels with the
  distributed binary.

## [0.2.0] - 2026-07-06

### Added

- **Closed captions** (soft subtitle track): `master` takes `soft_captions: true`
  to embed each page's manifest `caption` as a toggleable `mov_text` track
  (per-page timed cues built as SRT, muxed at the concat step). Player-rendered,
  no font needed — complements burned-in `captions` (which stays visible in muted
  autoplay); enable either or both. Result reports `soft_caption_cues`. ADR-0006.

## [0.1.0] - 2026-07-06

First release. Presentation-video compositor MCP server: page manifest
(image + audio per page) → one narrated MP4, with per-page chapters, optional
burned-in captions, per-call canvas override, and async rendering.

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
