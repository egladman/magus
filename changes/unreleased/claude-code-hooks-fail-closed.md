### Changed

- **Claude Code hooks are plain `magus buzz` commands run from the session root.** Each
  printed entry runs `magus buzz -C "$CLAUDE_PROJECT_DIR"` on its shipped template, with
  no shell around it; PATH's magus re-execs into that root's `./magus` when it has one.
  The new `magus-session.buzz` puts the root first on the session's PATH. A config merged
  before this still wraps each entry in a shell that falls back to PATH's magus, as this
  repository's own `.claude/settings.json` does; merge what `magus describe harness
  claude-code` prints to drop it.
