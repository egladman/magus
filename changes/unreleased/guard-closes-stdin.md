### Changed

- **The guard runs an agent's shell commands with stdin at end-of-file.** On Claude Code
  it prefixes an allowed command with `exec </dev/null;`, so a stray stdin reader no
  longer waits forever. A heredoc, pipe or `<` still feeds their command.
  `magus shell --rewrites-input` declares a wiring that can carry the rewrite. Codex and
  Cursor are not rewritten.
