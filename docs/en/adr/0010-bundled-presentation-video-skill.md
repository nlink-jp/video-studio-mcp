# ADR-0010: Bundle a presentation-video skill and ship it on every release

- Status: Accepted
- Date: 2026-11-04

## Context

The RFP left the "upstream workflow skill" to a separate skills-series project.
Meanwhile mcp-tactics carried this server's craft in `references/media.md`.
Organization ADR-027 changed both: mcp-tactics v0.13.0 dropped the production
servers, and a tool's own craft now ships as a skill bundled with the tool
(nlink-jp/video-studio-mcp#1). Nothing pointed an agent at that guidance any
more; `get_usage` is the call contract and does not cover how to make a good
video.

ADR-027 adds a skill only when the tool has craft beyond its `get_usage`.
Almost everything `media.md` said about this server is already in `get_usage`.
What is not is the work around the call, which is where a narrated video
succeeds or fails:

- settling the destination first, because it fixes the canvas and the kind of
  captions;
- rendering page images at the canvas aspect, and keeping text off diffusion
  output;
- producing **one audio file per page** with voice-studio. Its `master` turns
  one script into one file, so a deck needs one script per page. Line ids must
  be unique across all of them, because every script writes the same
  `wav/<id>.wav`: a repeated id silently masters one page with another's voice.
  The last line's pause becomes the gap before the next page;
- writing the manifest's `caption` as a page headline rather than a transcript,
  and keeping `fade` for section boundaries;
- reading the result counts against what was asked, and handing the review to
  the user, who can watch it.

## Decision

1. **Bundle `presentation-video/`** at the repository root, in the shape of
   ADR-027 Decision 2. It drives the whole chain — images, per-page audio,
   manifest, `master` — and leaves dialogue writing and casting to voice-studio's
   `multi-actor-narration` skill instead of restating it.
2. **`make test` runs the organization's skill validator**
   (`tests/validate-skill.sh`, ADR-006) after `go test ./...`.
3. **`internal/tools/skill_test.go` holds the skill to the server** in both
   directions:
   - every registered tool is named;
   - every backticked identifier is a registered tool, an input schema
     property, a `toolerr` code or a json tag of the server's own types;
   - the retired `workspace_root` spellings and `~/.video-studio` stay absent;
   - the manifest example parses with `internal/manifest`.

   Names owned by voice-studio cannot be derived here. They are listed with
   their owner, and the list must match what the skill uses.
4. **The skill ships as `presentation-video-<version>.zip` on every release**,
   with the skill folder at the zip's root. `make package` builds it, and
   `verify-release` refuses a release when the zip is missing, holds anything
   but the tracked files of the skill, or differs from them. voice-studio
   reached two releases with its skill and seventeen without, because nothing
   required it (voice-studio-mcp ADR-0015).

## Consequences

- An agent asked for a narrated video has the order of work and the voice-studio
  hand-off in one place, and the README tells each caller how to install it.
- A rename of this server's tools, arguments, codes or manifest fields fails the
  build until the skill follows. A rename in voice-studio does not; that list
  is maintained by hand.
- The skill and the server install separately: Homebrew and the binary zips
  carry the server only.
