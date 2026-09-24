### Added

- **A command rule sees where a line runs and which binary judges it.** The request
  carries `dir` and `workspace`, each command its `path` when the line names the program
  by one, and `magus\guard.binary()` returns the hook binary's `path` and the `stamp` its
  build linked in.
