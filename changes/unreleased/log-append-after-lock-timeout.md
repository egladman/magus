### Fixed

- **The concurrent log test no longer starves its appenders.** Its compactor retook the
  lock in a tight loop, so on a loaded machine an append waited past the 10s bound and
  failed. That append wrote nothing, and the test misread the gap. Compaction now runs
  once per append, and the test expects exactly the appends that reported success.
