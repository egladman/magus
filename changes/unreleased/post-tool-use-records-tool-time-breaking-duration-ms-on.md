### Changed

- **Breaking: `duration_ms` on an agent command row is the tool's run time, and the guard's
  own time moves to `guard_ms`.** A host's post-call hook now records the time its tool ran:
  Claude Code from `duration_ms` and Cursor from `duration`, through `magus shell --duration`.
  Codex and OpenCode send no duration, so they record none.
