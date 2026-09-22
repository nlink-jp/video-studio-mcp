# ADR-0009: Leave path judgement to nlink-jp/pathguard — keep no copy

- Status: Accepted
- Date: 2026-09-22

## Context

Since ADR-0008, `work_dir` validation lived in `internal/workdir`, a copy of voice-scribe's (the
reference implementation of organization ADR-021); seven other servers held the same copy. Every copy
compared places **by name**. APFS is case-insensitive by default, so spellings such as `~/.SSH` and
`/USR/local` named the same places and passed the checks. When the home directory could not be
determined, the credential-location check passed everything.

The organization moved this judgement into one module (`nlink-jp/pathguard`, lib-series). It compares
places by file identity and by names folded the way the disk folds them, and it catches a place that
does not exist yet through the identity of its parent. It holds one list, the same as gem-agent's and
lagent's.

## Decision

- Depend on `github.com/nlink-jp/pathguard` v0.1.0. No code from outside this organization comes
  with it.
- `internal/workdir` becomes a **thin adapter**. It keeps only:
  - taking the request's `_meta` from the context and passing it to `pathguard/workdir`'s `Resolve`,
  - moving that `*workdir.Error` onto `toolerr` with the same code, message and details,
  - `NewResolver(serverDirs...)` — passing this server's own directories (today only its config
    directory, `~/.config/video-studio-mcp`) as protected places (`pathguard.ServerDir`), and its one
    sentence for `work_dir_required` as `RequiredHint`. An empty path refuses every call rather than
    protecting nothing (the config directory is undetermined only when the home directory is, and
    then pathguard refuses everything anyway).
- It keeps no `Sensitive`. Every file argument of this server (`manifest_path`, and each manifest page's image and audio) is
  workspace-relative and contained by `os.Root`; nothing outside the work directory is read.
- The call sites (`Resolve`, `Validate`) do not change. What changes is the one line that builds the
  resolver (`workDirResolver` in `cmd/tools_wiring.go`) and the tests that built it as a zero value.
- The tests of the judgement itself are in pathguard. What stays here are the adapter's tests (taking
  `_meta`, carrying the error across, the protected place, a zero value refusing) and the existing
  contract and wiring tests.

## Consequences

The `work_dir` check changes (the CHANGELOG says so):

- **Refused now**: the real places under your home from the runtimes' list (`~/.kube`,
  `~/.config/gh`, `~/.azure`, `~/.terraform.d`, `~/.gemini`, `~/.config/mcp-bridge`, `~/.netrc`,
  `~/.npmrc`, `~/.pypirc`, `~/.git-credentials`, `~/.vault-token`, `~/.docker/config.json`,
  `~/.claude.json`, `~/.bash_history`, `~/.zsh_history`); every spelling of any floor place — case
  variants, links, firmlinks; wherever a link directly inside one of those directories points; when
  `$HOME` names another directory than the account's home, both; Linux `/etc`.
- **An unknown home refuses every `work_dir`.** It used to pass them.
- `work_dir_denied` carries `reason` in its `details`.
- One check costs about 2 ms (measured in pathguard) — nothing next to a render.

With no copy here, a fix to the judgement is a pathguard release and a one-line dependency update.

## Amendment (2026-09-22): judge the directory actually used

Only `work_dir` was checked, so `work_dir=~/.config` with `workspace_id=gh` made the workspace
`~/.config/gh` and the video and its segments were written into it. The hole dates from ADR-0008;
image-forge's independent review found it. `workspace.NewManager(check)` takes the judgement as a
required argument, and `EnsureUnder` judges `<work_dir>/<workspace_id>` with
`workdir.Resolver.CheckBeneath` (pathguard v0.2.0) before making or using it; `newToolDeps` wires it.
A Manager without one refuses every workspace. pathguard v0.2.0 also refuses a path holding a NUL
byte.

## Amendment (2026-09-22, v0.6.1): judge every file the workspace reads before reading it

`manifest_path` and the images and audio the manifest names are workspace-relative and handled
through the `os.Root`, but the floor was not applied to them. A workspace passes `CheckBeneath` and can
still contain places on the floor (a `.env`, this server's config directory, the target of a link in
`~/.ssh` when the workspace is in that sync folder). Such a file was read as the manifest, its first
bytes coming back in the parse error (`invalid character 'S'`), or, named as a page, accepted and
handed to ffmpeg; and the answer differed from the "not found" / `manifest_incomplete` for a missing
one. It is the existence class the independent reviews of slack-mcp-extender and chrome-pilot-mcp
found, measured here with the home directory redirected to a temporary one (9 of 15 pairs; the 6
planted links out of the workspace were refused by the `os.Root` whether or not their targets
existed).

- The workspace judges every read — `ReadFile`, `Stat`, `VerifyRegular` — before it reads or looks
  (`Workspace.Judge`, internal/workspace/manager.go), by pathguard's Local policy with this server's
  own directories. `workspace.NewManager(check, floor)` takes the floor as a required argument
  (`workdir.Resolver.LocalPath`; a Manager without one refuses every workspace), and a Workspace built
  without one refuses every read. The master tool also calls `Judge` on every page on the call itself,
  so an async render is refused before a job exists. pathguard follows the links on the path itself;
  the refusal names the path only as given, and a manifest refusal is returned as `path_not_allowed`,
  not wrapped as `invalid_manifest`. voice-studio-mcp, which shares the workspace, does the same.
- The independent review of a first version (which judged at the tool) found two ways around it at the
  ffmpeg boundary, both older than this change. Pages were verified once, before rendering, and opened
  by ffmpeg one by one afterwards: a page swapped for a link during the render (minutes, when async)
  was handed over. Now every image and audio is verified again, with the judgement, immediately before
  ffprobe or ffmpeg opens it. And ffmpeg reads `%d` in an image path as a numbered sequence: a regular
  file named `p%d.png` passed every check while ffmpeg would open `p0.png`, `p1.png`, … unjudged. A page
  whose path holds `%` — its name, or the workspace's own path through a `work_dir` holding one — is now
  refused (`master.CheckNames`), on the call and again in `Build`. ffmpeg globs only after an unescaped
  `%`, so brackets, braces and the other glob characters stay ordinary.
- Errors come in a different order for some ordinary inputs: a page the floor refuses, one that escapes
  the workspace lexically, and one whose path holds `%` are answered on the call, before a job is
  created and before `ffmpeg_not_found`; a page that is a link is still found by `Build`.
- `TestExistenceIsNotRevealed` calls `master` with the same path as the manifest, an image, an audio,
  page 2's image and an async image, while a file is there and after it is removed, compares the whole
  answer and checks that none of the file's contents comes back. `TestEveryReadIsJudgedBeforeItLooks`
  (internal/workspace) pins each read and a Manager or Workspace without a floor;
  `TestAPageNameThatIsAPatternIsRefused`, `TestAWorkspacePathThatIsAPatternIsRefused`,
  `TestAPageSwappedDuringTheRenderIsNotHandedToFFmpeg` (an image and an audio) and
  `TestAnAudioSwappedDuringTheProbesIsNotHandedToFFprobe` (internal/master) the ffmpeg boundary, and
  `TestTheServersWorkspacesJudgeEveryRead` (cmd) the server's own wiring. Sixteen mutations (no floor,
  each read unjudged, a Manager without a floor accepted, the floor not handed to the workspace, the
  wiring's floor a no-op, the pages unjudged on the call, no re-verify before ffmpeg or ffprobe or of
  the audio, pattern names allowed, the audio or the workspace path unchecked, the names unchecked on
  the call, the manifest refusal wrapped) all fail by assertion.
- Known limits in pathguard, recorded for its next release:
  - `work_dir` is validated by pathguard/workdir in the order organization ADR-022 §4 sets (not found
    before denied), so a `work_dir` naming a credential directory is answered by whether it exists.
  - A link target with a non-ASCII name spelled in another Unicode normalisation is found by identity
    only while it exists (pathguard does not normalise). A hard link made elsewhere is refused only when
    it is to a file that is itself a place on the floor (`~/.netrc`, `~/.docker/config.json`, …), and
    only while it exists; one to a file inside a credential directory (`~/.ssh/id_rsa`) or to a `.env`
    is not refused at all — a directory is compared by its own identity, not by its files'.
- The judgement and the open are two steps. Every page is verified again immediately before ffprobe
  or ffmpeg opens it, so a page swapped for a link during a render (minutes, when async) is refused.
- Not closed here — ffmpeg is an interpreter, and these need its input arguments changed and measured
  against a real ffmpeg (recorded for a follow-up across the media servers): ffmpeg picks an input's
  format from its contents, so a page file holding a concat list or a playlist can make it open files
  nobody judged; the workspace directory itself can be swapped for a link during a render (its real
  path is checked once, when the workspace is made — pathguard still refuses a place on the floor, so
  this breaks containment, not the floor); and the lists the server writes for ffmpeg (concat,
  chapters, captions) are not verified again before the spawn.

## Amendment (2026-09-22, v0.6.2): what ffmpeg writes and reads back moves out of the workspace; inputs have their format pinned

The "not closed here" items above were taken up after measuring them with a real ffmpeg (9.0.2, fake files in a
scratch directory).

- **Measured:** ffmpeg followed a page whose contents were an ffconcat list (named `.wav`, `file 'k'`, k an
  in-workspace link leading outside), and the outside file's audio went into the output. The independent review then
  measured more: a caller rewriting the workspace's concat list during a render made the outside file the output in
  10 of 10 runs (ffmpeg's startup is tens of milliseconds, easy to aim for — the earlier "needs precise timing" was
  wrong); a segment replaced by an ffconcat list was followed by the final concat; and a link planted at a segment's
  or the output's name had its target overwritten by ffmpeg. The precondition is a caller that can keep writing into
  the workspace during a render (a code runner with the workspace mounted, say).
- **Fixed at the cause:** everything ffmpeg writes and reads back — caption PNGs, segments, the concat list, chapters,
  subtitles, and the master until it is placed — lives in a private directory made per render under the user's cache
  directory (`~/Library/Caches/video-studio-mcp/render` on macOS). Not `$TMPDIR`: gem-agent's and lagent's write lanes
  can write `$TMPDIR` and `/private/tmp`, and a process running as the same user lists them, so an unguessable name is
  no barrier (the independent review measured an overwrite from a sandboxed writer). Neither lane can write the cache
  directory (the read lane only reads it). What a server killed mid-render left is cleared by the next render once it
  is a day old. Only the master is placed, through the workspace's `os.Root`, under a temporary
  name and then renamed, so a link at its name is replaced rather than followed and a component leading out is
  refused. `keep_intermediates` copies the intermediates into `output/tmp` the same way (on success and on failure,
  including when the master cannot be placed).
- **Inputs pinned:** only the page inputs are read from the workspace. An image is read as `image2` with no pattern (a
  `%` is a name too). Each image is decoded in Go at the first check, and one that does not read as PNG or JPEG — a
  GIF named `.png`, a truncated PNG, bytes that are no image — is refused with `invalid_manifest` before ffmpeg runs:
  handed an image it cannot decode, ffmpeg failed on every looped frame and never ended, its stderr growing without
  bound (measured). ffmpeg's decoder comes from that decode too (by extension, a JPEG named `.png` never finished).
  Formats other than the PNG/JPG the README named (BMP and the rest ffmpeg read by extension) are now refused. Audio
  and ffprobe take a whitelist of audio formats only (wav, mp3, mov/mp4/m4a, flac, ogg, aac,
  matroska/webm, aiff, caf, w64, au, ac3, asf — neither a concat list nor a playlist); only the `file` protocol. With
  the server's own argument shape, the accepted formats render and the crafted file is refused as "not on whitelist".
  A path holding a control character is not written into the concat list.
- **Accepted residuals** (the operator's rule: an overall risk assessment rather than perfection): a page input
  swapped for a link to outside between its judgement and ffmpeg's open can be read — but with the format pinned only
  an image or audio that parses as that format is, never a credential file. In the same gap, an image that passed the
  decode swapped for a corrupt one makes ffmpeg run without end — but only the caller's own render stalls. The cache
  directory is writable by a process of the same user running outside the sandbox (the operator lane, say) — which is
  what the user can do anyway. pathguard is at v0.3.0 (no behaviour change for this server).

## References

- Organization ADR-021 (the work-dir contract of the file-mediated MCP servers)
- ADR-0008 (work-dir contract): the closed list of checks — whose implementation this replaces
- nlink-jp/pathguard's RFP (`docs/en/pathguard-rfp.md`)
