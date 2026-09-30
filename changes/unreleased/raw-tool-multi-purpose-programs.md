### Fixed

- **The agent guard no longer refuses every `node` command.** The `typescript` spell's
  `node-test` op renders `node` with flags only, so the raw-tool rule read node as a
  single-purpose program and refused `node -e` and `node script.mjs` along with
  `node --test`. Spells now declare the args that select an op from a program's other
  uses through the new optional `mgs_getModeArgs() > {str: [str]}` export; `typescript`
  declares `--test` for `node-test`, and only a `node` carrying `--test` ahead of its
  script is refused. `govulncheck` and `shellcheck` declare none and stay refused in
  every spelling.
- **The `lease-undeclared` rule's catalog entry names a tombstoned binding**, which it
  already refused.
