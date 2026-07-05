# ADR-0006: Closed captions as a soft mov_text track

- Status: Accepted
- Date: 2026-07-06

## Context

v0.1.0 shipped **burned-in** captions (ADR-0004): always-on pixels, ideal for
muted social autoplay. But burned-in text is not toggleable, not selectable, and
not accessible to screen readers or translation. A **closed-caption** (soft
subtitle) track covers the complementary need: the viewer turns it on/off and
the player renders it — and, unlike burn-in, it needs no font on our side.

The ffmpeg on hand lacks libfreetype/libass, but the MP4 soft-subtitle codec
**`mov_text`** is a core encoder (confirmed present), so a soft track needs only
muxing — no text rendering.

## Decision

Add a soft closed-caption track, gated by a new **`soft_captions`** flag on
`master` (default off), **independent of** the existing `captions` (burn-in)
flag. Either or both may be enabled — this is an additive, backward-compatible
change to the v0.1.0 API (no repurposing of `captions`).

- **Build.** `buildSRT` renders an SRT from the pages that carry a `caption`,
  timed by the same cumulative per-page durations used for chapters (a page
  without a caption advances the clock but emits no cue). The SRT is written to
  `output/tmp/captions.srt`.
- **Mux.** `concatArgs` now takes both a `metadataPath` (chapters) and a
  `subtitlePath`. Extra inputs receive sequential indices after the concat input
  (0), and the `-map` / `-map_metadata` / `-c:s mov_text` arguments are computed
  from those indices rather than hard-coded — so chapters and captions compose
  correctly in one concat pass with `-c copy`.
- **Empty case.** If no page has a caption, no SRT is written and no subtitle
  input is added (`soft_caption_cues = 0`).

## Consequences

- **Complementary caption strategies.** `captions` = always-visible pixels
  (muted autoplay); `soft_captions` = toggleable, selectable, accessible text.
  Both draw from the same manifest `caption` field; the result reports
  `captions_burned` and `soft_caption_cues` separately.
- **No new dependency, works on the weak ffmpeg.** `mov_text` is core; the SRT is
  plain text built in Go.
- **`concatArgs` generalized.** Its signature grew a `subtitlePath` parameter and
  its input-indexing is now dynamic; the chapters-only path is unchanged in
  behavior (metadata becomes input 1 when there is no subtitle).
- **Timing shares the chapter timeline.** Same sub-frame AAC/fps quantization
  caveat as chapters; imperceptible for captions.
- Validated end-to-end: a two-page deck yields an MP4 with a `mov_text` stream
  whose extracted SRT matches the manifest captions and timing, alongside the
  chapter markers.
