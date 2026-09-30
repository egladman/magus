### Changed

- **The read rules judge every whole read.** `read-navigation` maps Go files from their
  own parse, so a stale index still gets the map. It judges every file a `cat` prints,
  maps Buzz files, and leaves SKILL.md, AGENTS.md and CLAUDE.md whole. Claude Code and
  Codex route the `Read` tool through the command guard; re-merge what `magus describe
  harness` prints.
