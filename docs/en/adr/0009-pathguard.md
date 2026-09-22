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

## References

- Organization ADR-021 (the work-dir contract of the file-mediated MCP servers)
- ADR-0008 (work-dir contract): the closed list of checks — whose implementation this replaces
- nlink-jp/pathguard's RFP (`docs/en/pathguard-rfp.md`)
