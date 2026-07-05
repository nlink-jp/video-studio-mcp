# AGENTS.md — video-studio-mcp

## Project summary

MCP stdio server that assembles a **narrated presentation video** (MP4) from a
page manifest: each page pairs a still image with its audio, and the pages are
concatenated in order via ffmpeg. Each page is shown for exactly the length of
its audio, so A/V sync is exact by construction. It is a **pure compositor** —
slide rendering and audio synthesis happen upstream (voice-studio-mcp supplies
the audio); this server only muxes and concatenates. Language-agnostic (it just
stitches images + audio). Skeleton ported from `voice-studio-mcp` /
`data-toolbox-mcp`. Dependencies: `cobra`, `BurntSushi/toml` only (ffmpeg +
ffprobe are runtime dependencies).

## Build & test

```sh
make build      # → dist/video-studio-mcp (auto-codesign on darwin)
make test       # go test ./... (hermetic: no ffmpeg needed — faked Runner)
make package    # build-all (5 platforms) + zip + notarize darwin
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
- `internal/tools/` — MCP tools (`get_usage`, `master`); `usage.md` embedded and
  coherence-tested.

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

## ADR cheat sheet

- **ADR-0001**: two-phase render — per-page uniform H.264/AAC segment (image
  looped to probed audio length, scaled + padded) → concat-demuxer stream copy.
- **ADR-0002**: per-page chapter markers via ffmetadata on the concat step;
  default on; title = manifest `title` else `Page N`.

Full texts: [`docs/en/adr/`](docs/en/adr/) / [`docs/ja/adr/`](docs/ja/adr/).

## Design references

- [`docs/en/reference/architecture.md`](docs/en/reference/architecture.md) /
  [`docs/ja/reference/architecture.ja.md`](docs/ja/reference/architecture.ja.md)
  — module map, render pipeline, error model, testing strategy.
- [`docs/ja/video-studio-mcp-rfp.ja.md`](docs/ja/video-studio-mcp-rfp.ja.md) /
  [`docs/en/video-studio-mcp-rfp.md`](docs/en/video-studio-mcp-rfp.md) —
  approved RFP; canonical source for scope (pure compositor; captions +
  transitions = Phase 2; workflow skill = separate skills-series project).
