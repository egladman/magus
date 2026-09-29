### Removed

- **BREAKING: the POSIX sh hook glue is gone; every host wires the Buzz glue.**
  `magus-command.sh`, `magus-path.sh`, `magus-observe.sh`, `magus-checkpoint.sh`,
  `magus-rehydrate.sh`, `cursor-hook.sh` and the three `magus-session-load-*.sh`
  adapters are deleted. Wire `magus buzz -s <file>.buzz -- --agent-name <host>`
  instead, as `magus describe harness` prints; nothing needs `jq` or a POSIX shell.
  The Codex harness moves to the Buzz glue too, and `magus-rehydrate.buzz` takes
  `--format json` and `--rules <file>` after `--` in place of `REHYDRATE_FORMAT` and
  `REHYDRATE_RULES`. The session-load adapters are `magus-session-load-*.buzz`, run
  the same way with `-- --stdout`. Guard templates are version 19.
