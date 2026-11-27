### Changed

- **A finished branch is integrated once its content reached main, however it merged.**
  The finished-branch limit and `magus ls jobs` ask no ancestry, and count the remote
  default branch (git's `origin/main`) as well as the checkout's revision. A remote
  whose default branch does not read is named in the answer. Answers move to
  `reached.jsonl`.
- **Breaking: `magus job prune <job>...` ends only the jobs named and their
  descendants.** A named job it would not end refuses the prune. A job ending only with
  its ancestor is named with that ancestor: `magus job prune done done/c`.
- **Breaking: a rejected `magus job wait` whose rework cannot re-claim its paths ends the
  row as `fail`, its rejection recorded.** The row stayed exited, and its checkout could
  not be removed.
