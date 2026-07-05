# RFP: video-studio-mcp

> Generated: 2026-07-05
> Status: Draft

## 1. Problem Statement

An MCP server that makes the **final compositing step** of turning presentation material into a single narrated video deterministic and reproducible. Slide authoring (Claude Code / Cowork) and audio synthesis (voice-studio-mcp) are assumed to be completed upstream; this tool's only input contract is **"a workspace directory where page images and page audio are laid out as pairs."** The `master` tool composites each page into a segment whose length exactly matches its audio, then concatenates all pages into one MP4. Because each segment's duration is derived from its audio length, video/audio synchronization problems cannot arise by construction. It depends on **no particular slide source format** (Marp / pptx / HTML), keeping it loosely coupled to upstream tools.

**Target users**: People who author material and scripts in Claude Code / Cowork (internal / individual). Anyone who wants to produce presentation recordings, product intros, internal sharing videos, or muted subtitle videos for social media without manual video editing.

**Scope (fixed)**: This tool is a **pure compositor** for "images + audio → video." It does not render slides to images or synthesize audio (those belong to upstream / a workflow skill).

## 2. Functional Specification

### Commands / API Surface

The primary surface is **MCP tools**. CLI subcommands are a secondary interface for local execution / testing (Go single binary + subcommands).

| MCP tool | Role |
|---|---|
| `get_usage` | Returns the workspace model, manifest schema, recovery table, and ffmpeg/font prerequisites (read first, same convention as voice-studio-mcp) |
| `master` | Reads the manifest, composites each page into an audio-length segment, concatenates them into one MP4, and returns its path |
| `check_job` | Progress check when long renders are made asynchronous (Phase 2) |

No `synthesize` equivalent (audio is voice-studio-mcp's responsibility). Compositing concentrates on `master`.

### Input / Output

**Input**:
- **Workspace directory**: The directory where the agent has placed materials (page images / page audio); it is treated as the workspace. File handoff is delegated to the agent's file-operation capability; this MCP carries no byte-transfer tool (same model as voice-studio-mcp).
- **Manifest JSONL**: One line = one page, explicitly carrying order, pairing, and per-page settings.

```jsonl
{ "image": "p01.png", "audio": "p01.wav", "caption": "…", "transition": "cut" }
{ "image": "p02.png", "audio": "p02.wav", "caption": "…", "transition": "fade" }
```

**Output**:
- `master.mp4` (H.264 / AAC / yuv420p)
- Intermediate segments can be kept or discarded

**Defaults**: 1920×1080, 16:9, hard cut, **captions off** (opt-in via manifest or config). Resolution / aspect / default transition are overridable via config or manifest.

### Configuration

- Config file (resolution, aspect, default transition, default font, keep-intermediates, etc.)
- Per-workspace manifest JSONL

### External Dependencies

- **ffmpeg** (the compositing engine): run as a child process, same treatment as voice-studio-mcp managing AivisSpeech Engine and data-toolbox-mcp managing Podman/DuckDB. On absence, a clear error plus install guidance is documented in `get_usage`.
- **CJK font (only when captions are used)**: Noto Sans JP (OFL) bundled as the default, overridable via `fontfile`.

## 3. Design Decisions

- **Why MCP**: Cowork's VM sandbox most likely cannot kick a local CLI directly; via MCP it can be called as a registered tool. → MCP is primary; CLI subcommands are secondary.
- **Why Go**: org standard; single binary + subcommands; the voice-studio-mcp / data-toolbox-mcp skeletons can be ported, and ffmpeg child-process management follows existing patterns.
- **Naming (video-studio-mcp)**: A pair with `voice-studio-mcp` (audio) ↔ `video-studio-mcp` (video). The ecosystem role split is expressed in the name, improving discoverability. Alternatives considered: `slidecast-studio-mcp` (most semantically precise but low term recognition) and `presentation-studio-mcp` (clear intent but slightly narrow); symmetry was prioritized.
- **Complements**: `voice-studio-mcp` (audio supply) / `multi-actor-narration` skill (upstream material→script→audio orchestration) / pptx・Marp (authoring). This tool is their **final compositing layer**.
- **Language neutrality advantage**: Unlike voice-studio-mcp (Japanese-only), this tool merely stitches images + audio, so it is **language-agnostic** and reusable for other audio sources and multilingual presentations.
- **Captions and fonts**: Caption burn-in depends on CJK font resolution for Japanese, and a signed/notarized distribution binary cannot assume system fonts are present. Following data-toolbox-mcp's precedent of bundling a CJK font for matplotlib, we **bundle Noto Sans JP (OFL) + `fontfile` override + a clear error when unresolved**, with OFL attribution.
- **Explicit out of scope**: Slide authoring / slide-to-image rendering (Marp・pptx→PNG) / audio synthesis / advanced video editing (multi-track mixing, BGM, elaborate animation). Captions stop at burn-in; no motion graphics.
- **Upstream workflow skill is out of scope for this RFP**: after the MCP is complete and its real usability is observed, it will be filed separately under skills-series.

## 4. Development Plan

### Phase 1: Core

- Skeleton port (voice-studio-mcp / data-toolbox-mcp): Go single binary + subcommands + MCP (stdio)
- Manifest JSONL parse + validation (image/audio existence, page order, strict JSON decode)
- ffmpeg command construction (looped image + audio → audio-length segment) + concat → `master.mp4`
- MCP tools `get_usage` / `master`
- Hard cut only, no captions, default 1920×1080
- **Per-page chapter markers** (ffmetadata; default on; title → `Page N`; rides the concat step. ADR-0002)
- **Tests**: manifest parsing, pairing validation, **ffmpeg argument construction (pure function, execution mocked)**, recovery paths
- → *independently reviewable* (heart of the compositing pipeline)

### Phase 2: Features

- **Async rendering `check_job` (implemented 2026-07-06, ADR-0003)** — `master async:true` submits a job; `check_job` polls progress/result.
- Captions (`caption`) — **approach changed: instead of burn-in + bundled CJK font, use a closed-caption (`mov_text` soft subtitle track)**. Reason: the real ffmpeg here has no `drawtext`/`subtitles` (libfreetype/libass), and that cannot be assumed on user machines either. A soft subtitle track needs only core muxing, which **removes the CJK-font bundle and the OFL dependency**. To be finalized in ADR-0004 when implemented.
- Fade and other transitions (xfade), resolution / aspect overrides
- Keep/discard intermediate segments
- → each feature independently reviewable

### Phase 3: Release

- README.md / README.ja.md / CHANGELOG.md / AGENTS.md, `get_usage` doc completeness
- OFL attribution for the bundled font (`licenses`)
- Multi-platform build; darwin signed + notarized
- Update umbrella (util-series) submodule pointer, add to org profile, run `check-org.sh`

**Independently reviewable boundaries**: Phase 1 (compositing pipeline alone), Phase 2 (captions / transitions per feature).

## 5. Required API Scopes / Permissions

**None** — no external services or credentials. ffmpeg is a local child process only.

## 6. Series Placement

Series: **util-series**
Reason: Belongs to util-series' MCP-server family alongside voice-studio-mcp / data-toolbox-mcp / ask-llm-mcp. Its material-transforming (images + audio → video), pipe-like nature also fits util-series.

## 7. External Platform Constraints

- **MCP client registration**: Used as an MCP server registered in Cowork / Claude Code. The workspace directory must be **reachable by the MCP process** (same filesystem where the agent places materials).
- **ffmpeg prerequisite**: ffmpeg must exist on the host (child-process execution). On absence, a clear error plus install steps in `get_usage`.
- **Codec compatibility**: Defaults to H.264 / AAC / **yuv420p** for broad playback compatibility. Stream parameters are unified across all segments for concat.
- **Caption fonts**: Captions require CJK font resolution. Default is bundled Noto Sans JP, overridable via `fontfile`.

---

## Discussion Log

- **Genesis**: Started from the idea of authoring material + per-page talk scripts in Claude Code / Cowork, generating per-page audio with voice-studio-mcp, and auto-producing one presentation video by pairing each page image with its audio.
- **Scope (case A fixed)**: Limited to a pure "images + audio → video" compositor. Slide rendering and audio synthesis belong upstream / to a workflow skill, prioritizing minimal responsibility and testability (the same cut as voice-studio-mcp focusing on audio synthesis).
- **Why MCP**: Adopted MCP as the primary interface because Cowork's VM sandbox most likely cannot kick a local CLI.
- **Pairing (case A fixed)**: A **manifest JSONL** rather than filename convention — symmetric with voice-studio's script JSONL, carrying captions/transitions per page with explicit ordering.
- **Captions (default off fixed)**: Standard feature but opt-in with default off.
- **Workspace / file handoff**: Treat the agent-provided material directory as the workspace and delegate file handoff to the agent's file-operation capability (same model as voice-studio-mcp). No byte-transfer tool.
- **Naming**: Chose **video-studio-mcp** for symmetry with `voice-studio-mcp`. `slidecast-studio-mcp` / `presentation-studio-mcp` were also considered.
- **Caption font issue (case A fixed)**: Given CJK font resolution risk, bundle Noto Sans JP (OFL) + `fontfile` override + error when unresolved, following data-toolbox-mcp's CJK-font-bundling precedent. OFL attribution included; scoped to Phase 2 together with captions.
- **Upstream workflow skill**: Out of scope for this RFP; to be filed separately under skills-series after the MCP is complete.
