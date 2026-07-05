# ADR-0005: Per-call canvas override and intermediate cleanup

- Status: Accepted
- Date: 2026-07-06

This is a small, wide ADR bundling two low-risk output-control refinements added
together.

## Context

- **Multi-aspect output.** The canvas size lives in the server's `[video]`
  config, but an agent often needs the *same* deck in several shapes — 16:9 for
  a talk, 9:16 for Reels/Shorts/TikTok, 1:1 for a feed — within one session. An
  MCP client cannot easily rewrite server config mid-session.
- **Intermediate accumulation.** `master` writes per-page segments, caption
  PNGs, the concat list, and the chapter metadata under `output/tmp`, but never
  removed them, so `output/tmp` grew with every render.

## Decision

- **Per-call canvas override.** `master` accepts optional `width`, `height`, and
  `fps`. They override a copy of the server `VideoConfig` for that render only.
  The overridden config is re-validated with the newly-exported
  `config.VideoConfig.Validate()` (even dimensions ≥16, fps ≥1, …); a bad
  override fails fast with `invalid_arguments` before any ffmpeg runs. Images are
  already scaled-to-fit and padded, so any aspect ratio just works.
- **Intermediate cleanup.** After a *successful* concat, `master` removes
  `output/tmp` (best-effort; a cleanup error does not fail the render). On
  failure the tmp is left in place to aid debugging. `keep_intermediates: true`
  opts out of the cleanup.

## Consequences

- One deck → many deliverable shapes without server reconfiguration; the render
  result echoes the effective `width`/`height`/`fps`.
- `output/` holds only the finished master(s) by default; tests that need to
  inspect intermediates pass `keep_intermediates`/`KeepIntermediate`.
- `VideoConfig.Validate()` is now the single validation path for both config load
  and per-call overrides, so the rules cannot drift between them.
- No new dependencies; both changes are covered by unit tests and validated
  end-to-end (a 9:16 render reports 1080×1920 and `output/tmp` is gone).
