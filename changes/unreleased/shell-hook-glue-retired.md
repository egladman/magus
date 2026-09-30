### Removed

- **Breaking: the POSIX sh hook glue is gone; every host wires the Buzz glue.** The
  `magus-*.sh` hooks, `cursor-hook.sh` and the `magus-session-load-*.sh` adapters are
  deleted. Wire `magus buzz -s <file>.buzz -- --agent-name <host>` as `magus describe
  harness` prints. `magus-rehydrate.buzz` takes `--format json` and `--rules <file>` in
  place of `REHYDRATE_FORMAT` and `REHYDRATE_RULES`. Guard templates are version 19.
