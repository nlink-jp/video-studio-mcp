# ADR-0007: Fade transitions as a within-page dip, not a cross-page xfade

- Status: Accepted
- Date: 2026-07-26

## Context

`transition` has been part of the manifest schema since v0.1.0, and `fade` has
been an accepted value — but only for forward compatibility: every page boundary
rendered as a hard cut (ADR-0001 deferred non-cut transitions to "Phase 2").
The manifest accepted a field it then silently ignored, which is exactly the
failure mode `DisallowUnknownFields` exists to prevent elsewhere in this server.

The obvious implementation is ffmpeg's **`xfade`** filter, which dissolves page
A into page B. It is the wrong tool here, and the reason is the pipeline's
central invariant.

`xfade` **overlaps** the two pages by the transition length `d`. The overlap has
three consequences, each of which breaks something this server promises:

1. **Total duration stops being the sum of the audio durations.** An `n`-page
   deck with `k` fades runs `Σ audio − k·d`. "Each page is shown for exactly the
   length of its audio, so A/V sync is exact by construction" (README, ADR-0001)
   would no longer be true — and the narration of page B would start playing
   while page A is still on screen.
2. **The chapter and closed-caption timelines break.** `pageChapters` and
   `buildSRT` both derive their timeline from accumulated per-page audio
   durations. Every marker after the first fade would drift by the accumulated
   overlap.
3. **The concat can no longer stream-copy.** ADR-0001's two-phase design encodes
   each page to identical parameters precisely so the join is `-c copy`. A
   cross-page filter graph forces a re-encode of the whole timeline, and the
   segment/concat split loses its purpose.

## Decision

Render `transition: "fade"` as a **dip to the canvas background colour inside
each page's own duration**: page A fades out over its last `d`, page B fades in
over its first `d`. No overlap, no cross-segment filter graph.

- **Semantics.** `transition` on page *i* describes the boundary *into* page
  *i+1*. `fade` there means a fade-out at the end of segment *i* **and** a
  fade-in at the start of segment *i+1*. On the **last** page it is ignored —
  there is no next boundary. Ending the video on a fade to black is an outro,
  a different feature, and overloading `transition` to mean both would make the
  field's meaning depend on its position.
- **Where.** Both fades are appended to the existing per-page filter chain in
  `segmentArgs`, *after* the caption overlay, so a burned-in caption fades with
  its page instead of floating over a darkening image.
- **Colour.** The fade dips to `[video] background` — the same colour as the
  letter/pillar-box padding — so a non-black canvas stays coherent.
- **Length.** `[video] fade_seconds` (default `0.5`), overridable per render
  with `master`'s `fade_seconds`. Clamped per page so that **at least half of
  every page stays at full brightness**: with `n` fades on a page (1 or 2), each
  gets `min(fade_seconds, duration/2/n)`. A fade that ends up shorter than one
  frame (`1/fps`) is dropped — a sub-frame fade is not a transition.
- **Video only.** The audio is never faded. Narration must not be clipped, and
  the audio stream is what defines the page's length.
- **Reporting.** The result carries `fades_applied`: the number of boundaries
  rendered as a fade, in the same spirit as `captions_burned` and
  `soft_caption_cues`.

## Consequences

- **Every invariant survives.** Page duration is still the probed audio length,
  total duration is still `Σ audio`, chapters and captions keep their timeline,
  and the final concat is still `-c copy`. The change is confined to
  `segmentArgs` and the per-page loop that calls it.
- **The fade eats into the page's own narration time.** With a 0.5 s fade, the
  last half-second of a page's audio plays over a darkening image and the first
  half-second of the next plays over a brightening one. That is the price of
  refusing to overlap, and it is the right trade: a visual dip is cosmetic,
  while a broken audio timeline is a defect. Decks that care should carry
  trailing silence — which narration produced by voice-studio-mcp typically
  does — or set a shorter `fade_seconds`.
- **A real dissolve remains impossible without re-encoding.** If a cross-page
  dissolve is ever wanted, it needs its own ADR, a `crossfade` value distinct
  from `fade`, an explicit statement of what happens to the audio timeline, and
  a third render phase. Nothing here forecloses it.
- **`fade` is no longer a lie.** The manifest field now does what it says, and
  ADR-0001's "Phase 2" note is superseded.

## Alternatives considered

| Alternative | Why not |
|---|---|
| `xfade` between segments | Breaks total duration, chapter/caption timelines, and the copy-concat (see Context) |
| Fade video *and* audio | Clips narration at both ends of every faded page; the audio length is the page's contract |
| Reject `transition: "fade"` as unimplemented | Honest, and was the alternative on the table — but the field is in the published schema and the feature is cheap once the dip-vs-overlap question is settled |
| Fade to black always, ignoring `background` | Visibly wrong on a white or branded canvas |
| A trailing fade on the last page | Overloads `transition` with a position-dependent second meaning; belongs to a future outro option |
