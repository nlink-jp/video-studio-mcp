# ADR-0001: Two-phase render pipeline (per-page segment → concat-copy)

- Status: Accepted
- Date: 2026-07-05

## Context

The `master` tool must turn N pages — each a still image plus a narration audio
file of arbitrary length — into one MP4 in which each page is shown for exactly
the length of its audio. Two ffmpeg strategies exist:

1. **Single filtergraph** — feed all 2N inputs into one `ffmpeg` invocation and
   build a `concat` filter over per-page image+audio sub-streams.
2. **Two-phase** — encode one segment per page, then join the segments.

## Decision

Use the **two-phase** pipeline:

1. **Per-page segment.** For each page, `ffmpeg -loop 1 -i image -i audio -t
   <probed duration> -vf "scale=W:H:force_original_aspect_ratio=decrease,
   pad=W:H:(ow-iw)/2:(oh-ih)/2:<bg>,setsar=1,fps=F" -r F -c:v libx264 -crf …
   -tune stillimage -pix_fmt yuv420p -c:a aac -b:a … -ar …` → a uniformly
   encoded segment.
2. **Concat.** Write a concat-demuxer list and join with `-c copy`
   (stream copy, no re-encode).

Page duration is measured with **ffprobe** (`format=duration`) and passed as
`-t`, rather than relying on `-shortest`. This makes the segment length exact
and lets the tool report an accurate total. Each page is scaled to fit and
**padded** (letter/pillar-boxed) onto the canvas so mixed aspect ratios never
distort.

## Consequences

- **Uniformity is mandatory.** Stream-copy concat only works if every segment
  shares canvas size, fps, pixel format, and audio codec/rate. All of these are
  fixed by `[video]` config and applied identically in `segmentArgs`; they are
  not per-page. This is the load-bearing invariant of the whole pipeline.
- **Simple and linear.** Work is O(pages); each page is independent, which also
  makes a future async/progress (`check_job`) or parallel-encode extension
  straightforward.
- **Testable.** The arg builders are pure functions unit-tested directly; the
  `Runner` interface lets tests fake ffmpeg/ffprobe, keeping `go test ./...`
  hermetic. Real ffmpeg is validated manually.
- **Container may run a few ms long.** AAC priming/frame alignment can make the
  final duration slightly exceed the audio sum; acceptable for narration.
- **Captions/transitions are out of scope here.** `caption` burn-in and
  `fade` transitions (Phase 2) will extend `segmentArgs` / add a cross-page
  filter pass; caption burn-in additionally requires bundling a CJK font
  (Noto Sans JP / OFL). Phase 1 renders every page as a hard cut with no
  caption.

  *Superseded in part:* captions shipped via ADR-0004 / ADR-0006, and fade
  transitions via ADR-0007 — which found that a **cross-page** filter pass is
  exactly what must not happen (it would break this ADR's stream-copy concat and
  the exact-duration guarantee), and implemented the fade inside each page's own
  segment instead.
