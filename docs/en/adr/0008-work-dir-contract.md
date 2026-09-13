# ADR-0008: The work directory is a per-call `work_dir`, with no default root

- **Status**: Accepted (2026-09-13)

## Context

This applies organization ADR-021 (the work-directory contract for file-mediated
MCP servers) here. `master` took an optional `workspace_root` and wrote under
`~/.video-studio` when it was omitted — a directory no calling agent's file tools
can open, so the mux succeeded and the MP4 path it returned did not.

This server is a pure compositor: it reads images (from image-forge) and audio
(from voice-studio) **where the upstream servers put them**. Sharing one
`work_dir` with them is the premise, which is another reason the destination
belongs to the caller rather than to this server.

## Decision

1. **The argument is `work_dir`, required by `master`** — the absolute path of a
   directory the caller can read back. The workspace is
   `<work_dir>/<workspace_id>/`.
2. **Resolution is argument → `_meta["jp.nlink/work_dir"]` → error**, with no
   default root and no default-root operations on the manager.
3. **Validation is a closed list** (absolute, no `~`, no `..`, exists and is a
   directory, writable, not a system or credential location), with five
   `work_dir_*` codes. The directory is not created.
4. Manifest image/audio paths stay workspace-relative, with `os.Root` containment
   unchanged.
5. **A media chain shares one `work_dir`** (image-forge → voice-studio → here).

## Consequences

- **Breaking.** A call sending `workspace_root` is refused with the new name.
- `~/.video-studio` stops being used; anything already there is left alone.
- `internal/workdir` is byte-identical to the copies in the rest of the fleet.

## References

- Organization ADR-021, voice-scribe ADR-0010 (reference implementation),
  pcap-analyzer-mcp ADR-0008, voice-studio-mcp ADR-0013 (the paired server)
