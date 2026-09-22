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
make verify-release  # gate: .notarized marker + freshness (run before upload)
```

Never `go build` directly — always `make build` (outputs to `dist/`).

## Structure

- `cmd/` — cobra commands: `serve` (default), `doctor`, `version` (also
  reachable as the `--version` flag, which the org homebrew formula tests).
- `internal/jsonrpc/`, `internal/transport/`, `internal/mcpserver/`,
  `internal/toolerr/`, `internal/logging/` — MCP skeleton (ported verbatim
  from voice-studio-mcp / data-toolbox-mcp).
- `internal/config/` — sectioned TOML, strict decode (unknown keys fail);
  `[video]` = canvas/fps/crf/preset/pix_fmt/audio/background.
- `internal/workspace/` — one deck = one workspace; ALL server I/O goes through
  `os.Root`; `ResolveInside` + `VerifyRegular` block path traversal / symlink
  escape; the agent prepares the workplace (`work_dir`).
- `internal/manifest/` — page manifest JSONL parser (`Page`); strict decode,
  per-line errors collected (max 20).
- `internal/master/` — the render pipeline: pure ffmpeg/ffprobe arg builders
  (`segmentArgs`, `concatArgs`, `probeArgs`, `concatList`) + `Runner` interface
  (faked in tests); `Build` orchestrates probe → per-page segment → concat.
  Every input a caller can supply is opened with its format pinned
  (`imageInput`: image2, no pattern; `audioInput`/`probeArgs`: the
  `audioFormats` whitelist, never concat or HLS; `-protocol_whitelist file`) —
  ffmpeg picks a format from the contents, and a page holding an ffconcat list
  was followed outside the workspace (ADR-0009, v0.6.2). A new input goes
  through these helpers. **Nothing ffmpeg writes or reads back lives in the
  workspace**: `Build` makes a private directory per render (`privateDir`,
  under the user cache directory — **not `$TMPDIR`**, which gem-agent's and
  lagent's write lanes can write; tests replace `privateRoot` in `TestMain`)
  for captions, segments, lists and the master, and places only the master
  through the workspace root (`Workspace.PlaceFile`); a caller writing into the
  workspace during a render took the output 10 of 10 times when the concat list
  lived there. `keep_intermediates` copies are deferred, so they run on every
  way out. Page images are decoded in Go at step 1 (`imageDecoder`: PNG or
  JPEG, else `invalid_manifest`) — ffmpeg handed an undecodable image never
  ends — and the decoded format names ffmpeg's decoder. Test fixtures must be
  real PNGs (`encoded` / `pngImage`).
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
  re-verified as real regular files (`VerifyRegular`, Lstat, with the floor's
  judgement) immediately before they are handed to ffprobe or ffmpeg; page
  images and audio were verified only once, before the render, until v0.6.1.
  The verify-to-spawn race is accepted under the local single-user threat
  model; it is not the only gap — the workspace directory swapped mid-render
  and the formats ffmpeg picks from a file's contents are recorded, not
  closed (ADR-0009, amendment v0.6.1).
- **The workspace base is verified by real path, because the path is handed to
  ffmpeg, which resolves it outside any root** — `os.Root` contains operations
  *within* a root but resolves the root path itself normally, so a link planted
  at `<work_dir>/<id>` would anchor every read and write on its target while
  reporting success. `makeWorkspaceDir` creates the directory through an
  `os.Root` on `work_dir` **and** compares `filepath.EvalSymlinks` of the base
  against `<real work_dir>/<id>`; a mismatch is refused. The comparison is the
  load-bearing half — the root-based mkdir alone cannot help a path that later
  leaves the process.
- **The final MP4 is placed, never written by ffmpeg, in the workspace** —
  ffmpeg opens its output path itself and would follow a symlink planted at
  `output/<name>.mp4`, overwriting the link's target. It writes into the
  private directory instead, and `Workspace.PlaceFile` copies the result in
  through the root: an entry planted at `<name>.tmp` is unlinked, the
  temporary is created `O_EXCL`, then renamed over the name (a link there is
  replaced, not followed).
- **The workspace judges every read.** `Workspace.ReadFile`, `Stat` and
  `VerifyRegular` call `Judge` first — pathguard's Local policy with this
  server's own directories, the `floor` that `workspace.NewManager(check,
  floor)` requires (`workdir.Resolver.LocalPath`; nil refuses every workspace,
  and a Workspace literal without one refuses every read). The master tool
  also calls `Judge` on every page before a job exists. A workspace passes
  `CheckBeneath` but can still contain a `.env`, this server's config
  directory or the file a link in `~/.ssh` leads to (ADR-0009, amendment
  v0.6.1); voice-studio-mcp, which shares the workspace, does the same. Do not
  add a read that bypasses these, and do not judge at call sites instead. A
  page path holding `%` — its name or the workspace path — is refused
  (`master.CheckNames`, on the call and in `Build`): ffmpeg expands it into
  other files; other glob characters are ordinary. `TestEveryReadIsJudgedBeforeItLooks`,
  `TestExistenceIsNotRevealed` and the three ffmpeg-boundary tests in
  internal/master and `TestTheServersWorkspacesJudgeEveryRead` (cmd) pin it;
  sixteen mutations are caught by assertion. ffmpeg-level gaps (format from
  contents, the workspace swapped mid-render) are recorded, not closed (ADR-0009). Writes
  are not judged.
- **Symlink vs missing** — `VerifyRegular` returns `path_not_allowed` for a
  symlink/non-regular entry (surfaced immediately) but a plain lstat error for a
  missing file (collected → `manifest_incomplete`). Do not collapse the two.
- **A fade never crosses a page boundary** — both halves of a `"fade"`
  transition are rendered *inside* the neighbouring pages' own segments
  (`fadeDurations` → `segmentArgs`). Never reach for `xfade`/`acrossfade`: they
  overlap pages, which breaks the Σ-audio duration, the chapter/SRT timelines,
  and the stream-copy concat (ADR-0007). Unknown transitions are still rejected
  as `invalid_manifest` — don't silently drop them.
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
- **The path judgement is nlink-jp/pathguard's, not this repository's.**
  `internal/workdir` only takes `_meta` from the context and carries
  pathguard's errors onto `toolerr` (ADR-0009). Do not add a location list or a
  name comparison here; a fix to the judgement is a pathguard release and a
  dependency bump.
- **`workdir.Resolver` is built in exactly one place** — `workDirResolver()`
  in `cmd/tools_wiring.go`, reached only through `newToolDeps`, with
  `workdir.NewResolver(serverOwnedDirs()...)`. Those are this server's own
  directories (organization ADR-021 §4), which today is
  `configDir()` = `~/.config/video-studio-mcp`; `resolveConfig`
  searches that same expression so the denial cannot drift from the location.
  There is no state directory — this is a pure compositor and every byte it
  produces goes under the caller's `work_dir`. Add one and it belongs in
  `serverOwnedDirs()`. A zero `workdir.Resolver{}` refuses every call, and so
  does an empty server directory — tests build one with
  `workdir.NewResolver(t.TempDir())`.
- **The workspace directory is judged too.** `workspace.NewManager(check)`
  takes `workdir.Resolver.CheckBeneath`, and `EnsureUnder` judges
  `<work_dir>/<workspace_id>` before making it — `work_dir=~/.config` with
  `workspace_id=gh` is `~/.config/gh`. `newToolDeps` wires both; a Manager
  without a check refuses every workspace.

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
  via `VideoConfig.Validate`) for multi-aspect output, and discard of the
  intermediates (`keep_intermediates` copies them into `output/tmp`; since
  v0.6.2 they are made outside the workspace, ADR-0009).
- **ADR-0006**: closed captions — `soft_captions` embeds a `mov_text` subtitle
  track (SRT built per-page, muxed at concat); additive to the burn-in `captions`
  flag; complementary (soft = toggleable/accessible, burn = muted-autoplay).
- **ADR-0007**: fade transitions as a **within-page dip**, not a cross-page
  `xfade` — `transition:"fade"` fades out at the end of its page and in at the
  start of the next, inside each page's own duration, so Σ-audio duration,
  chapter/caption timing, and the stream-copy concat all survive. `fade_seconds`
  (config + per call, default 0.5) clamped so half of every page stays at full
  brightness; sub-frame fades dropped; last page's transition ignored; audio
  never faded; reported as `fades_applied`.

Full texts: [`docs/en/adr/`](docs/en/adr/) / [`docs/ja/adr/`](docs/ja/adr/).

## Design references

- [`docs/en/reference/architecture.md`](docs/en/reference/architecture.md) /
  [`docs/ja/reference/architecture.ja.md`](docs/ja/reference/architecture.ja.md)
  — module map, render pipeline, error model, testing strategy.
- [`docs/ja/video-studio-mcp-rfp.ja.md`](docs/ja/video-studio-mcp-rfp.ja.md) /
  [`docs/en/video-studio-mcp-rfp.md`](docs/en/video-studio-mcp-rfp.md) —
  approved RFP; canonical source for scope (pure compositor; workflow skill =
  separate skills-series project). Its "captions + transitions = Phase 2" note
  is now shipped — ADR-0004/0006 (captions) and ADR-0007 (fade).
