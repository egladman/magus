### Changed

- **Claude Code hooks are plain `magus buzz` commands run from the session root.** Each
  entry runs `magus buzz -C "$CLAUDE_PROJECT_DIR"` on its shipped template, with no shell
  around it, and PATH's magus re-execs into that root's `./magus`. The new
  `magus-session.buzz` puts the root first on PATH. Merge `magus describe harness
  claude-code` to drop an older shell wrapper.
