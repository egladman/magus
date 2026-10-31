### Changed

- **Breaking: `duration_ms` on an agent command row is the tool's run time, and the guard's own time moves to `guard_ms`.** A host's post-call hook now records the time its tool ran: Claude Code from `duration_ms` and Cursor from `duration`, through `magus shell --tool-ms`. Codex and OpenCode send no duration, so they record none.

### Added

- `magus shell --tool-ms N` records how long the tool named on stdin ran, without judging anything.
