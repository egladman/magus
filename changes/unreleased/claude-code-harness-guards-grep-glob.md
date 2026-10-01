### Changed

- **The Claude Code harness guards its Grep and Glob tools.** `magus describe harness
  claude-code` now wires a `Grep|Glob` PreToolUse entry, so a host search is judged as
  the `rg` or `find` line it stands for. Run the printed merge command to pick it up.
