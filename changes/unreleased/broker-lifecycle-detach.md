### Changed

- **Breaking: `--detach` runs the command in the background itself and needs no server.**
  The run becomes its own session, appends to a log under
  `$XDG_STATE_HOME/magus/detached/` and holds its own broker connection; magus prints its
  pid and log path. The `run.detach` record carries `pid` and `log`. MGS5004 is retired.
