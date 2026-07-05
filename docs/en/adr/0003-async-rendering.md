# ADR-0003: Async rendering via in-memory jobs

- Status: Accepted
- Date: 2026-07-06

## Context

`master` runs a full ffmpeg pipeline (probe every page, encode one segment per
page, concat). For a large deck this can take long enough to block the MCP
tool call past a client's timeout, and the agent gets no feedback while it
waits.

## Decision

Add an **opt-in async mode**. `master` takes `async` (default `false`):

- **`async: false` (default).** Unchanged Phase 1 behavior: render synchronously,
  return the full result. Small decks stay a single round-trip.
- **`async: true`.** Submit the render as an in-memory background job and return
  `{job_id, state: "running", pages}` immediately. The new `check_job` tool
  reports `state` (`running`/`done`/`failed`), coarse `progress` (phase + pages
  done/total), and — once `done` — the same result payload the sync path
  returns (or, on `failed`, the structured error).

Design points:

- **Validation stays synchronous.** The manifest is read and parsed in the tool
  handler before submitting, so `invalid_manifest` returns immediately. Only the
  heavy ffmpeg work is backgrounded; asset/ffmpeg failures
  (`manifest_incomplete`, `ffmpeg_failed`, …) surface via `check_job`'s error.
- **Jobs run under the server-lifetime context** (`Deps.JobCtx`), not the
  per-request context — otherwise the render (and its ffmpeg child) would be
  cancelled the instant the `master` call returns. On server shutdown (SIGINT)
  the context is cancelled, aborting in-flight renders.
- **In-memory, not persisted.** A finished-job history is bounded and evicted.
  After a restart `check_job` returns `job_not_found` with guidance to re-run
  `master` (safe: it re-renders from the same agent-prepared workspace). This
  mirrors `voice-studio-mcp`'s job model and avoids a persistence layer whose
  only job would be to survive a crash.
- **One render per job.** Unlike `voice-studio-mcp`'s per-line item jobs, a video
  render is a sequential pipeline, so the job wraps the whole `master.Build`; the
  `internal/job` manager is correspondingly simpler (a generic `RunFunc` + a
  progress callback, decoupled from the `master` package).

## Consequences

- Long renders no longer risk a client timeout; the agent can show progress.
- Two response shapes for `master` (sync = result object; async = `{job_id,…}`).
  Clients branch on the presence of `job_id`; the `get_usage` manual documents
  both.
- Progress is coarse (per-page, plus a phase label). Finer progress (ffmpeg's
  own frame counter) is possible later by parsing ffmpeg `-progress`, but was
  not worth the parsing surface for Phase 2.
- No new dependencies; `internal/job` is pure Go and fully hermetically tested.
