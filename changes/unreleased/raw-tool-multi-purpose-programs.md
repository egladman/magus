### Fixed

- **The agent guard no longer refuses every `node` command.** A spell now names the args
  that select an op from its program's other uses, through the new optional
  `mgs_getModeArgs()`, and `typescript` declares `--test` for `node-test`. `node --test` is
  refused while `node -e` and `node script.mjs` run. Single-purpose tools such as
  `govulncheck` stay refused in every spelling.
