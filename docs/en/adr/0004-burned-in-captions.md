# ADR-0004: Burned-in captions via Go-rendered overlay

- Status: Accepted
- Date: 2026-07-06

## Context

Captions are valuable for this tool because the narration audio is often
Japanese and the videos are watched in **muted social autoplay**, where a
soft/closed-caption track is hidden by default. The original RFP assumed
caption **burn-in via ffmpeg `drawtext`** with a bundled CJK font.

Environment reality changed the plan: the ffmpeg on the development machine is
built **without libfreetype/libass**, so neither `drawtext` nor `subtitles`
exists — and that cannot be assumed present on user machines either. Available
filters include the core `overlay`, `scale`, `pad`, `concat`.

Separately, a sibling util-series tool, **json-to-table**, already bundles the
**M PLUS 1p** font (SIL OFL 1.1, ~1.75 MB) and renders text to a PNG with
`golang.org/x/image/font/opentype` (`opentype.Parse` → `NewFace` →
`font.Drawer`/`MeasureString`).

## Decision

Render captions **in Go**, not in ffmpeg. Caption burn-in becomes an opt-in
`captions` flag (default off) on `master`:

1. For each page with a non-empty `caption`, render a **canvas-sized transparent
   PNG** with the wrapped caption text in a centered, semi-opaque box near the
   bottom, using the bundled **M PLUS 1p** font via `golang.org/x/image`
   (`internal/caption`). All positioning is baked into the PNG.
2. Composite it onto the page's video with ffmpeg's core **`overlay`** filter:
   `segmentArgs` switches to `-filter_complex "[0:v]scale/pad/…[bg];[bg][2:v]overlay=0:0[v]"`
   with explicit `-map [v] -map 1:a`, and reverts to the plain `-vf` path when a
   page has no caption.

The font is bundled by reusing json-to-table's M PLUS 1p (with a corrected
`FONTS_LICENSE` naming the actual font) and README OFL attribution.

## Consequences

- **Works on any ffmpeg with `overlay`** (a core filter) — no libfreetype
  dependency, portable across user environments. This was the deciding factor.
- **Full layout control in Go** — wrapping (per-rune, Japanese-aware), box,
  colors, and margins are computed in `internal/caption` and configured under
  `[caption]`, instead of wrestling with `drawtext` escaping/expressions.
- **Always-on** — burned pixels survive muted social autoplay and platforms that
  strip soft subtitle tracks. The trade-off (not toggleable, not selectable
  text) is acceptable for the target use case; a soft `mov_text` track can be
  added later as a complementary toggle.
- **New dependency** `golang.org/x/image`, and a ~1.75 MB font embedded in the
  binary. Both are modest and match an established in-org pattern.
- **Hermetic tests** — caption rendering is pure Go (validated by decoding the
  PNG and checking for ink); the overlay wiring is checked at the `segmentArgs`
  level and end-to-end on real ffmpeg (white caption pixels confirmed in the
  output frame).
- The caption PNG is **server-written** under `output/tmp/`, so it is handed to
  ffmpeg without the `VerifyRegular` pre-spawn check applied to agent assets.
