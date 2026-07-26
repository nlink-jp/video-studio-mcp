# CLAUDE.md — video-studio-mcp

**Organization rules (mandatory): https://github.com/nlink-jp/.github/blob/main/CONVENTIONS.md**

## Project overview

MCP stdio server that assembles a narrated presentation video (MP4) from a page
manifest: each page pairs a still image with its audio, concatenated in order
via ffmpeg. A **pure compositor** — slide rendering and audio synthesis are
upstream (voice-studio-mcp supplies the audio); this server does the mechanical
mux + concat. Paired with `voice-studio-mcp` (audio ↔ video). Skeleton ported
from `voice-studio-mcp` / `data-toolbox-mcp`.

## Non-negotiable rules

- **Tests are mandatory** — write them with the implementation.
- **Never `go build` directly** — always `make build` (outputs to `dist/`).
- **Docs in sync** — update `README.md` and `README.ja.md` together.
- **Small, typed commits** — `feat:`, `fix:`, `test:`, `chore:`, `docs:`, `refactor:`, `security:`.
- **stdout is sacred** — the MCP transport runs over stdout; logs MUST go to stderr.
- **Hermetic tests** — `go test ./...` must pass without ffmpeg installed
  (faked `Runner`); real-ffmpeg validation is a manual/opt-in check.

## Build & test

```sh
make build      # → dist/video-studio-mcp (auto-codesign on darwin)
make test       # go test ./...
make package    # build-all + zip + notarize darwin
```

## Key design points (see AGENTS.md for the full list)

- Two-phase render: per-page uniform H.264/AAC segment → concat-demuxer copy.
- Page duration = ffprobe-measured audio length (`-t`), not `-shortest`.
- All segments share codec params so concat can stream-copy.
- Agent-prepared workspaces; all I/O via `os.Root`; ffmpeg inputs re-verified
  pre-spawn.
- Captions ship in both forms (burned-in overlay + `mov_text` soft track).
- `transition: "fade"` is a **within-page dip**, never a cross-page `xfade` —
  overlapping pages would break the Σ-audio duration and the copy-concat
  (ADR-0007).

## Design references

- `docs/ja/video-studio-mcp-rfp.ja.md` / `docs/en/video-studio-mcp-rfp.md`
  — approved RFP; canonical source for scope decisions.
