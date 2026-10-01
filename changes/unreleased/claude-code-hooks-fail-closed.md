### Changed

- **Claude Code hooks are plain `magus buzz` commands run from the session root.** Each
  printed entry runs `magus buzz -C "$CLAUDE_PROJECT_DIR"` on its shipped template, with
  no shell around it; PATH's magus re-execs into that root's `./magus` when it has one.
  The new `magus-session.buzz` puts the root first on the session's PATH.
