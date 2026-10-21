### Changed

- **Breaking: every guard rule with a remedy denies by default.** The rules that refuse
  a recoverable call, such as `stage-all`, `raw-tool` and `read-navigation`, and the
  advisories whose text names a command, such as `capture-filter`, `graph-pipe` and
  `dependency-update`, refuse and serve that command. A rule with no reachable remedy is
  a notice, which never refuses, and `magus\guard.builtins` refuses `deny` for one. Set
  any other rule to `advise` or `off` by name in the root magusfile.
