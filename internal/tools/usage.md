# video-studio-mcp — how to use this server

This server assembles a **narrated presentation video** (MP4) from a page
manifest: each page pairs a still **image** with its **audio**, and the pages
are concatenated in order. It is a **pure compositor** — it does not create
slides or synthesize speech. You (the agent) prepare the slides (render each
page to an image with your own tools) and the audio (e.g. via
**voice-studio-mcp**), place them in a workspace, and this server muxes and
concatenates them with ffmpeg. Tools return compact JSON (paths, counts,
durations) — never video bytes; the produced file is played by the human user
on this host.

## Workspace model (read this first)

All render state lives in a workspace: `<workspace_root>/<workspace_id>/`

```
<manifest>.jsonl   page manifest        (you write this)
images/…           page still images    (you place these)
audio/…            page audio files     (you place these)
output/            rendered mp4 + tmp    (server-written)
```

- `workspace_id`: `[a-zA-Z0-9_-]{1,64}`, one per deck / presentation.
- `workspace_root` (optional on the `master` tool): an **absolute path to a
  directory you prepared** — create it with your own file tools wherever you
  are allowed to write (e.g. inside the project directory), place the images,
  audio, and manifest under it, then pass the same value on the call. Omit it
  to use the server's default root (`~/.video-studio`), which requires the
  server and you to share an unrestricted filesystem view.
- Image/audio paths in the manifest are **relative to the workspace root**.
- The server never reads or writes outside the workspace (kernel-enforced;
  symlinks inside the workspace that point outside fail with
  `path_not_allowed`).

## Render flow

1. Render each presentation page to an image (PNG/JPG) and place it in the
   workspace (e.g. `images/p01.png`).
2. Synthesize each page's narration audio and place it in the workspace (e.g.
   `audio/p01.wav`) — voice-studio-mcp is the intended source.
3. Write the page manifest JSONL (one page per line, in presentation order).
4. `master` — muxes each page (image looped for its audio's duration) and
   concatenates the pages into `output/<name>.mp4`. Returns the path, page
   count, and total duration.
   - For a long deck, pass `async: true`: `master` returns a `job_id`
     immediately and renders in the background. Poll `check_job` with that
     `job_id` for progress (`state`, pages done/total) and, once `state` is
     `done`, the same result payload `master` returns synchronously.

## Page manifest JSONL (one page per line)

```jsonl
{"image":"images/p01.png","audio":"audio/p01.wav","title":"導入"}
{"image":"images/p02.png","audio":"audio/p02.wav","caption":"見出し","transition":"cut"}
```

Fields: `image` (required; workspace-relative still image), `audio`
(required; workspace-relative narration audio — its duration sets the page's
on-screen time), `title` (optional; the page's **chapter-marker** name —
defaults to `Page N`), `caption` (optional; shown via `master`'s `captions`
(burned-in) and/or `soft_captions` (toggleable track) — ignored when both are
off), `transition` (optional; how this page joins the **next** one — `cut`
default, or `fade`; ignored on the last page).

Blank lines and lines starting with `#` are ignored.

## Output

`output/<name>.mp4` — H.264 / AAC / yuv420p, 1920×1080 @ 30fps by default
(configurable). Each page is letter/pillar-boxed onto the canvas; total
duration is the sum of the page audio durations. Defaults are chosen for broad
playback compatibility (QuickTime, browsers, social platforms).

By default the MP4 carries one **chapter marker per page** (title from the
manifest `title` field, else `Page N`), so viewers can jump between slides in
players that support chapters. Pass `chapters: false` to `master` to disable.

The manifest `caption` can be shown two independent ways (both default off):

- `captions: true` — **burn it into the video** (always-on pixels, good for
  muted autoplay). Rendered with a bundled Japanese font and composited onto
  the frame; text wraps to the canvas width; styling from the `[caption]` config.
- `soft_captions: true` — embed it as a **toggleable closed-caption track**
  (`mov_text`), which the viewer turns on/off and the player renders (no font
  needed). Hidden by default in muted social autoplay.

Enable both for burned-in pixels plus a selectable track. The result reports
`captions_burned` and `soft_caption_cues` (pages that contributed to each).

**Fade transitions**: a page whose `transition` is `"fade"` fades out at its end
and the next page fades in at its start, dipping to the canvas background
colour. The fade happens **inside each page's own duration**, so total duration,
chapter markers, and caption timing are unaffected — but the narration keeps
playing while the image dims, so keep a little trailing silence in the audio if
that matters. `fade_seconds` (default 0.5, or `[video] fade_seconds`) sets the
length; it is clamped so at least half of every page stays at full brightness,
and `0` turns every boundary back into a cut. The last page's `transition` is
ignored — there is no next page. The result reports `fades_applied` (boundaries
rendered as a fade). Audio is never faded.

**Canvas override**: pass `width`/`height`/`fps` to override the output size for
one render (dimensions must be even and ≥16). Produce the same deck as `16:9`
(1920×1080), `9:16` vertical (1080×1920), or `1:1` (1080×1080) without changing
server config. Images are always letter/pillar-boxed to fit, so mixed source
sizes never distort.

**Intermediates**: the per-page segments and caption PNGs under `output/tmp`
are discarded after a successful render. Pass `keep_intermediates: true` to keep
them (debugging).

## Error recovery

| code | action |
|------|--------|
| invalid_manifest | fix every entry in details.errors (missing image/audio, unknown key/transition, empty manifest), retry |
| manifest_incomplete | place the missing files listed in details.missing into the workspace, then retry |
| ffmpeg_not_found | the user must install ffmpeg (ffprobe ships with it) |
| ffmpeg_failed | inspect details.stderr_tail; a bad image/audio input → fix that page, retry |
| probe_failed | the page audio is unreadable by ffprobe; re-supply that audio file |
| caption_failed | a caption could not be rendered; check the `[caption]` config (e.g. font_color/box_color) |
| path_not_allowed | use workspace-relative asset paths / a valid absolute workspace_root; symlinks out of the workspace are rejected |
| invalid_workspace_id | match [a-zA-Z0-9_-]{1,64} |
| job_not_found | the server restarted (async jobs are in-memory); re-run master (it re-renders from the same workspace) |
