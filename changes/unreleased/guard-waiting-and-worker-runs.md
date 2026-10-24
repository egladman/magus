### Fixed

- **A worker can wait the way the guard tells it to.** `magus status --wait` piped into any
  magus call passes sleep-poll, a stop passes while a bounded run is backgrounded, and a
  read-only run over the row's own projects passes worker-check-only. Verdict refs from a
  linked worktree resolve, since each verdict is stored in the primary checkout too.
