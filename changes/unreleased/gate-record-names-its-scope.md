### Fixed

- **A gate record names its scope.** `magus run ci libs/cmdline` passing no longer reads as
  the branch gate: the guard refused `magus affected ci` as a repeat and accepted a push
  while the last `affected ci` had failed. A record is `affected` when it covered every
  project the branch affects against its default base, and `project` otherwise. Only an
  `affected` pass covers a push or makes a later `affected ci` a repeat; a failure of either
  scope keeps the branch red. A record written before scopes reads as `project`, and the
  push refusal says so.
