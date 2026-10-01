### Changed

- **Claude Code hooks are plain `magus buzz` commands run from the session root.** Each
  printed entry runs `"$CLAUDE_PROJECT_DIR/magus" buzz -C "$CLAUDE_PROJECT_DIR"` on its
  shipped template, with no shell around it, and the new `magus-session.buzz` puts that
  root first on the session's PATH. A checkout with no `./magus` runs its hooks open.
