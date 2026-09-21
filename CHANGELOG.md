# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/), and the project adheres to
[Semantic Versioning](https://semver.org/).

## [0.5.5] - 2026-09-21

### Security

- **A symlink planted where the workspace goes no longer redirects the whole
  render outside `work_dir`.** The server opens each workspace as a containment
  root, but a root resolves its own path normally: if something had already put
  a symlink at `<work_dir>/<workspace_id>` — another tool with write access to
  `work_dir`, or code in a sandbox — every read and write, including the
  finished MP4, landed in the link's target while every result reported success
  and printed a path under `work_dir`. The workspace directory is now created
  through a root on `work_dir` and then checked by real path; a workspace that
  turns out to be a link is refused, naming the id and what it resolved to.
- **A symlink at the output path no longer gets its target overwritten by the
  render.** ffmpeg opens the final MP4 itself and cannot inherit the containment
  root, so a link left at `output/<name>.mp4` was followed and whatever it
  pointed at was overwritten with the video. The path is now cleared through the
  workspace root before ffmpeg is spawned, which removes the link and not its
  target, so the render always creates the file fresh.

## [0.5.4] - 2026-09-21

### Fixed

- **A file that is not in the workspace is reported with the path that was
  looked at.** The error used to read `openat narration.wav: no such file or
  directory` — a name relative to a directory the message did not mention. The
  workspace is a level below the `work_dir` the caller names, which is not where
  an agent naturally puts a file; the same bare sentence in voice-scribe sent a
  real agent off inventing a directory (2026-09-14). That fix reached the
  scribes and image-forge and not this server, which shares a workspace with
  voice-studio-mcp. Every workspace file operation now names the absolute path and says
  what the name is relative to.

### Documentation

- The architecture reference (en and ja) and a package comment still described a
  server default, `~/.video-studio`; there has been none since the work_dir
  contract.

## [0.5.3] - 2026-09-14

### Added

- `TestEveryRequiredNameIsDeclared` — a schema that lists a name in `required`
  without declaring it in `properties` makes a strict client refuse the whole
  tool list (Vertex AI: "schema at top-level requires unspecified property").
  data-toolbox-mcp shipped exactly that and broke a session outright; the
  existing contract test checked declared ⇒ required only, so the fleet is
  pinned in both directions now.

## [0.5.2] - 2026-09-14

### Fixed

- **The initialize `instructions` field never mentioned `work_dir`.** It is the
  first thing the model reads about this server — before any tool list — and it
  still described "a workspace directory you prepare" while every tool required
  an argument it did not name. It now states the contract: `work_dir` is the
  absolute path of a directory you can read back, required, with no default.

### Added

- `TestInstructionsNameTheWorkDirContract` — the schema and description tests
  walked `tools/list`; nothing walked what `initialize` returns (ADR-0008).

## [0.5.1] - 2026-09-13

### Fixed

- **The `~/.video-studio` root was announced as gone in 0.5.0 but was still
  half there**: `[workspace] workspace_dir` still decoded into a field no tool
  read, `doctor` still created the directory, the startup log still reported
  it, and `config.example.toml` still offered it as "the default root". The key
  is gone, and a config still carrying it now fails to load naming `work_dir`
  as the replacement.
- README and README.ja still told the caller to omit `work_dir` to get the
  server default. It is required and has no default.

### Added

- A contract test walking every registered tool: no retired name in a schema or
  a description, and `work_dir` required wherever it is declared. ADR-0008
  asked for this test and the release shipped without it.

## [0.5.0] - 2026-09-13

### Changed

- **Breaking: `workspace_root` is now `work_dir`, and `master` requires it.** It
  means the absolute path of a directory the caller can read back; the workspace
  is `<work_dir>/<workspace_id>/`, and it is the same directory the upstream
  media servers wrote into. A call still sending `workspace_root` (or
  `workspaceRoot` / `workspace_dir`) is refused with `work_dir_required` naming
  the replacement. See [ADR-0008](docs/en/adr/0008-work-dir-contract.md);
  organization ADR-021.
- **Breaking: the `~/.video-studio` default root is gone.** Omitting the argument
  used to write there, which no calling agent can open.
- A runtime may supply the directory instead of the model: the server reads
  `_meta["jp.nlink/work_dir"]` when the argument is absent. The argument wins.

### Added

- `work_dir_required`, `work_dir_invalid`, `work_dir_not_found`,
  `work_dir_not_writable`, `work_dir_denied` — five codes that say which part of
  the contract failed.

## [0.4.2] - 2026-08-31

### Changed

- The `workspace_root` argument now says plainly that the caller should pass a
  root it can read back: every result is returned as a path under that root, so
  a workspace the caller cannot open leaves it holding a path to nothing. Text
  only — the behaviour is unchanged.

## [0.4.1] - 2026-07-26

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
