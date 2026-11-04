---
name: presentation-video
description: Turn a slide deck, a document or a theme into one narrated presentation video (MP4) — page images, narration for each page, then video-studio's master. Use for narrated slides, explainer and training videos, recorded briefings, vertical clips for social feeds, and for adding narration to an existing deck. Also for ナレーション付き動画・プレゼン動画・スライド動画・解説動画・研修動画・資料を動画にする・スライドに音声を付ける・縦型動画. Japanese narration comes from voice-studio; narration in another language must be supplied as audio files. Needs the video-studio MCP server.
argument-hint: "<deck, document or theme> [destination: meeting | web | vertical | square]"
---

# presentation-video

A narrated presentation video is three pieces of work, and video-studio does
only the last one:

1. **Page images** — one still per page (your rendering, or image-forge for an
   illustration)
2. **Page audio** — one narration file per page (voice-studio for Japanese)
3. **Assembly** — video-studio's `master` puts each image on screen for exactly
   as long as its audio, then joins the pages

All of the judgement is in the first two. video-studio is a pure compositor: it
never renders a slide, never synthesizes speech and never decides how long a
page lasts. **Each page lasts exactly as long as its audio** — do not compute
durations, and do not try to stretch a page; change its narration instead.

Call `get_usage` on video-studio (and on voice-studio and image-forge, when you
use them) before the first call. It is the contract — arguments, the manifest
schema, limits and the full error table. This skill is the order of work and
the choices that make the video good; it does not repeat the contract.

## 0. Settle with the user before making anything

Ask, or infer from the request and say what you inferred:

| Question | Why it matters |
|---|---|
| Where will it be watched? | Sets the canvas and the captions (table below) |
| Narration language | voice-studio speaks **Japanese only**. For any other language the user supplies the audio, or you raise translating the narration to Japanese — never translate silently |
| One narrator, or a conversation? | A conversation (host and guest, a panel) uses the `multi-actor-narration` skill's formats for writing and casting; the page split below still applies |
| Will it be published? | Voice models have their own terms; a published video carries their credits (step 3) |

| Destination | Canvas (`width` × `height`) | Captions |
|---|---|---|
| Meeting, presentation, landscape web player | 1920 × 1080 (the default) | `soft_captions` — a track the viewer can turn on |
| Vertical social feed (muted autoplay) | 1080 × 1920 | `captions` — burned in; a muted feed never shows a track |
| Square feed | 1080 × 1080 | `captions` |

`captions` and `soft_captions` are independent; turn both on when the video goes
to both kinds of place. Both show the manifest's `caption` for the whole page —
they are page captions, not timed subtitles of the narration.

## 1. One workspace for the whole chain

Pass the **same `work_dir` and the same `workspace_id`** to every server. Each
server writes into `<work_dir>/<workspace_id>/`, so the manifest can name what
the others produced without copying anything:

```
<work_dir>/<workspace_id>/
├── images/p01.png …      page images you render
├── output/               image-forge's PNGs, and video-studio's MP4
├── casting.toml          voice-studio: one table for every page
├── pages/p01.jsonl …     voice-studio: one script per page
├── wav/  master/         voice-studio: line audio, and one mastered file per page
└── video.jsonl           the page manifest
```

`work_dir` is required by every one of these servers and has no default: pass
the absolute path of a directory you can read back, such as your working
directory.

## 2. Page images

- **Render at the canvas size and aspect.** A page of another shape is
  letter- or pillar-boxed, never stretched — correct, but it wastes the screen.
  A 16:9 deck becomes a vertical video only after re-laying the pages out.
- **From an existing deck or PDF**, export each page to PNG with your own tools.
  The text must be readable at the size the video will be watched at; a page
  dense enough to read on paper is often too dense on a phone.
- **image-forge** is for an illustration or a background, not for a page that
  carries text: a diffusion model does not draw reliable lettering. Compose the
  text over its image yourself.
- PNG or JPEG only. Anything else, whatever its name, is refused as
  `invalid_manifest`.

## 3. Page audio

**Write the narration per page.** Say what the page cannot: the reasoning, the
connection to the next page, what to look at. Reading the slide's text aloud is
the most common way to make a dull video. Keep each page's narration about one
idea; a page that needs much longer than its neighbours usually wants to be two
pages.

### With voice-studio (Japanese)

Follow voice-studio's own order — choose voices with `list_speakers`, register
readings with `register_dictionary` **before** synthesis, synthesize, retake
single lines with `synthesize_line` — with these additions for video:

- **One script per page** (`pages/p01.jsonl`, `pages/p02.jsonl`, …), sharing one
  `casting.toml`. voice-studio's `master` turns one script into one file, and the
  manifest needs one file per page.
- **Line ids must be unique across every page script in the workspace**, not
  only within a file. Every script writes to the same `wav/<id>.wav`, so page 2's
  line 1 overwrites page 1's line 1 and page 1 is then mastered with page 2's
  voice. Number by page — `101, 102, …` on page 1, `201, …` on page 2.
- **End each page with silence**: the last line's `pause_after_ms` (for example
  600–1000 ms) is the breath before the next page. A page that will fade out
  needs more than the fade: the image dims while the narration is still
  playing otherwise.
- **Master each page** with voice-studio's `master`, `format: "mp3"` and
  `output_name` set to the page (`p01`), giving `master/p01.mp3`. Every page is
  normalised to the same loudness target, so levels match from page to page.
- **Credits**: voice-studio's `master` reports `credits_path` and
  `unverified_models`. For a published video, put the credits on a last page or
  in the video's description, and tell the user about any unverified model
  before publishing.

To change one page later: edit that page's script, synthesize it again (only the
changed lines are re-synthesized — the cache), master that page again, then run
video-studio's `master` again.

### With audio from elsewhere

Name each file in the manifest as it is: WAV, MP3, M4A, FLAC, Ogg and the other
formats `get_usage` lists are read directly. Leave the trailing silence in the
file itself.

## 4. The page manifest

One line per page, in order:

```jsonl
{"image":"images/p01.png","audio":"master/p01.mp3","title":"はじめに","caption":"なぜ今、要約 AI なのか"}
{"image":"images/p02.png","audio":"master/p02.mp3","title":"三つの論点","caption":"機密・正確さ・人の確認","transition":"fade"}
{"image":"images/p03.png","audio":"master/p03.mp3","title":"まとめ"}
```

- **`title`** names the page's chapter marker. Use the slide's own short title;
  without it the chapter is `Page N`.
- **`caption`** is on screen for the whole page, so make it a headline or the one
  point of the page — never the narration's transcript. A long caption wraps and
  covers the slide.
- **`transition`** is how the page joins the **next** one: a hard cut by default,
  which is what presentations conventionally use. Keep `fade` for the boundaries
  between sections, so it still means something; the last page's transition is
  ignored.

## 5. Assemble

Call video-studio's `master` with `work_dir`, `workspace_id`, `manifest_path`
(`video.jsonl`), the canvas from step 0, and the caption switches. For more than
a few pages, pass `async: true` and poll `check_job` with the `job_id` until the
job is done.

Then read the result against what you asked for: `pages` is the page count,
`duration_seconds` is the sum of the page audio, `chapters`, `captions_burned`,
`soft_caption_cues` and `fades_applied` count what was actually rendered. A
count of zero where you asked for captions means the pages carry no `caption`.

## 6. Hand it over

You cannot watch the result. Give the user the MP4 path (`master_path`), the
duration and page count, and what to check: that each page's narration matches
its image, the readings of names and terms, the captions. Fix a page by
repeating its steps and calling `master` again; untouched pages cost nothing to
re-render.

## When something fails

`get_usage` holds the full table. The ones that come from the hand-off between
servers:

| Code | What usually happened |
|---|---|
| `manifest_incomplete` | A file the manifest names is not in the workspace: a page not mastered yet, a different `workspace_id` or `work_dir` on another server, or a typo in a path. `details.missing` lists them |
| `invalid_manifest` | An image that is not PNG/JPEG, an unknown field or transition, or an empty manifest; `details.errors` lists every one |
| `probe_failed` | A page's audio cannot be read; master or supply it again |
| `job_not_found` | The server restarted during an async render; call `master` again |
| `work_dir_required` | `work_dir` was not passed; every server in the chain needs it |
