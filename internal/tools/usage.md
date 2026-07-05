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
defaults to `Page N`), `caption` (optional; **burned into the video** when
`master` is called with `captions: true` — always-on subtitle text, styled by
the server's `[caption]` config; ignored when `captions` is off), `transition`
(optional; `cut` default — `fade` is accepted but **rendered as a hard cut**).

Blank lines and lines starting with `#` are ignored.

## Output

`output/<name>.mp4` — H.264 / AAC / yuv420p, 1920×1080 @ 30fps by default
(configurable). Each page is letter/pillar-boxed onto the canvas; total
duration is the sum of the page audio durations. Defaults are chosen for broad
playback compatibility (QuickTime, browsers, social platforms).

By default the MP4 carries one **chapter marker per page** (title from the
manifest `title` field, else `Page N`), so viewers can jump between slides in
players that support chapters. Pass `chapters: false` to `master` to disable.

Pass `captions: true` to **burn each page's `caption` into the video**
(always-on, good for muted autoplay). Captions are rendered with a bundled
Japanese font and composited onto the frame; text wraps to the canvas width.
Styling (font size, colors, box, margins) is set in the server's `[caption]`
config. Default off.

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
