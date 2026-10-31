### Fixed

- **A read-only subagent keeps its discovery tools, and a read is never refused.** The
  Claude Code scout denies the host's write tools instead of allowlisting four, so it keeps
  `ToolSearch`, `Skill` and MCP. Codex no longer writes a `sandbox_mode` it ignores.
  `architecture-unbriefed` advises a read of the workspace and refuses only a write.
