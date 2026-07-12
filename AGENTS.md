# AGENTS.md — video-studio-mcp

## Project summary

MCP stdio server that assembles a **narrated presentation video** (MP4) from a
page manifest: each page pairs a still image with its audio, and the pages are
concatenated in order via ffmpeg. Each page is shown for exactly the length of
its audio, so A/V sync is exact by construction. It is a **pure compositor** —
slide rendering and audio synthesis happen upstream (voice-studio-mcp supplies
the audio); this server only muxes and concatenates. Language-agnostic (it just
stitches images + audio). Skeleton ported from `voice-studio-mcp` /
`data-toolbox-mcp`. Dependencies: `cobra`, `BurntSushi/toml`, and
`golang.org/x/image` (caption text rendering) — ffmpeg + ffprobe are runtime
dependencies.

## Build & test

```sh
make build      # → dist/video-studio-mcp (auto-codesign on darwin)
make test       # go test ./... (hermetic: no ffmpeg needed — faked Runner)
make package    # build-all (4 platforms; darwin arm64 only) + zip/tar.gz + notarize darwin
```

Never `go build` directly — always `make build` (outputs to `dist/`).

## Structure

- `cmd/` — cobra commands: `serve` (default), `doctor`, `version`.
- `internal/jsonrpc/`, `internal/transport/`, `internal/mcpserver/`,
  `internal/toolerr/`, `internal/logging/` — MCP skeleton (ported verbatim
  from voice-studio-mcp / data-toolbox-mcp).
- `internal/config/` — sectioned TOML, strict decode (unknown keys fail);
  `[video]` = canvas/fps/crf/preset/pix_fmt/audio/background.
- `internal/workspace/` — one deck = one workspace; ALL server I/O goes through
  `os.Root`; `ResolveInside` + `VerifyRegular` block path traversal / symlink
  escape; the agent prepares the workplace (`workspace_root`).
- `internal/manifest/` — page manifest JSONL parser (`Page`); strict decode,
  per-line errors collected (max 20).
- `internal/master/` — the render pipeline: pure ffmpeg/ffprobe arg builders
  (`segmentArgs`, `concatArgs`, `probeArgs`, `concatList`) + `Runner` interface
  (faked in tests); `Build` orchestrates probe → per-page segment → concat.
- `internal/job/` — in-memory background-render jobs (one render per job);
  progress + result/error tracking; NOT persisted (`job_not_found` on restart).
- `internal/caption/` — renders a caption string to a transparent canvas-sized
  PNG (bundled M PLUS 1p via `golang.org/x/image/font/opentype`); word/char wrap,
  centered box; composited by ffmpeg `overlay`. Pure Go, hermetically tested.
- `internal/tools/` — MCP tools (`get_usage`, `master`, `check_job`);
  `usage.md` embedded and coherence-tested.

## Gotchas

- **stdout is sacred** — it carries JSON-RPC; all logs go to stderr/file via
  `log/slog`.
- **Two-phase render, not one** — each page becomes its own uniformly-encoded
  segment, then segments are joined by concat-demuxer stream copy (`-c copy`).
  All segments MUST share codec params (canvas, fps, pix_fmt, audio rate) or the
  copy concat produces a broken file — that uniformity is enforced in
  `segmentArgs`, driven entirely by `[video]` config.
- **Duration comes from ffprobe, not `-shortest`** — each page's segment length
  is `-t <probed audio duration>`; exact and reported in the result. (The final
  container may run a few ms longer due to AAC frame alignment; expected.)
- **ffmpeg cannot inherit `os.Root`** — image/audio/segment inputs are
  re-verified as real regular files (`VerifyRegular`, Lstat) immediately before
  they are handed to ffmpeg. The remaining verify-to-spawn race is accepted
  under the local single-user threat model.
- **Symlink vs missing** — `VerifyRegular` returns `path_not_allowed` for a
  symlink/non-regular entry (surfaced immediately) but a plain lstat error for a
  missing file (collected → `manifest_incomplete`). Do not collapse the two.
- **Captions/fade are accepted but not rendered** — Phase 1 honors `cut` only
  and ignores `caption`. Don't silently drop unknown transitions; reject them as
  `invalid_manifest`.
- **Chapters ride the concat step** — per-page chapter markers are an ffmetadata
  file (`;FFMETADATA1` + `[CHAPTER]` blocks) fed as the second concat input with
  `-map 0 -map_metadata 1`; boundaries come from the same probed durations used
  for `-t`, so no extra probing. Titles use the manifest `title` (→ `Page N`).
  Default on; `chapters: false` opts out.
- **Async jobs run under JobCtx, not the request ctx** — `master async:true`
  submits the render under `Deps.JobCtx` (the server-lifetime signal context),
  so it outlives the tool call but is cancelled on shutdown. Validation
  (manifest parse) still happens synchronously before submit; asset/ffmpeg
  errors surface via `check_job`. Jobs are in-memory only — do not add
  persistence casually; `job_not_found` → re-run master.
- **Two caption modes, independent flags** — `captions` (burn-in) and
  `soft_captions` (closed-caption track) are separate booleans, both default off;
  either or both can be on.
  - **Burn-in** renders in Go (`internal/caption`, M PLUS 1p) to a transparent PNG
    composited with the core `overlay` filter — NOT `drawtext` (this ffmpeg has no
    libfreetype/libass, and users' may not either). `segmentArgs` switches to a
    `filter_complex` (`[0:v]scale/pad[bg];[bg][2:v]overlay=0:0[v]` + explicit
    `-map [v] -map 1:a`) only for pages with a caption; the PNG is canvas-sized
    (positioning baked in) and server-written (no VerifyRegular).
  - **Soft** builds an SRT (`buildSRT`, per-page timed by the same cumulative
    durations as chapters) and muxes it as `mov_text` at the concat step.
    `concatArgs` takes both `metadataPath` (chapters) and `subtitlePath`; extra
    inputs get sequential indices after concat input 0, so the `-map` /
    `-map_metadata` indices are computed, not hard-coded. Empty captions → no
    track (`soft_caption_cues=0`).

## ADR cheat sheet

- **ADR-0001**: two-phase render — per-page uniform H.264/AAC segment (image
  looped to probed audio length, scaled + padded) → concat-demuxer stream copy.
- **ADR-0002**: per-page chapter markers via ffmetadata on the concat step;
  default on; title = manifest `title` else `Page N`.
- **ADR-0003**: async rendering — `master async:true` submits an in-memory
  background job under the server-lifetime context; `check_job` polls
  state/progress/result; not persisted (`job_not_found` → re-run).
- **ADR-0004**: burned-in captions via Go-rendered overlay (M PLUS 1p / OFL,
  reusing json-to-table's pattern) + ffmpeg `overlay` — chosen over drawtext
  (ffmpeg here lacks libfreetype) and over a soft `mov_text` track (burn-in
  survives muted autoplay). Default off.
- **ADR-0005**: per-call canvas override (`width`/`height`/`fps`, re-validated
  via `VideoConfig.Validate`) for multi-aspect output, and discard of
  `output/tmp` intermediates after a successful render (`keep_intermediates` to
  keep).
- **ADR-0006**: closed captions — `soft_captions` embeds a `mov_text` subtitle
  track (SRT built per-page, muxed at concat); additive to the burn-in `captions`
  flag; complementary (soft = toggleable/accessible, burn = muted-autoplay).

Full texts: [`docs/en/adr/`](docs/en/adr/) / [`docs/ja/adr/`](docs/ja/adr/).

## Design references

- [`docs/en/reference/architecture.md`](docs/en/reference/architecture.md) /
  [`docs/ja/reference/architecture.ja.md`](docs/ja/reference/architecture.ja.md)
  — module map, render pipeline, error model, testing strategy.
- [`docs/ja/video-studio-mcp-rfp.ja.md`](docs/ja/video-studio-mcp-rfp.ja.md) /
  [`docs/en/video-studio-mcp-rfp.md`](docs/en/video-studio-mcp-rfp.md) —
  approved RFP; canonical source for scope (pure compositor; captions +
  transitions = Phase 2; workflow skill = separate skills-series project).
