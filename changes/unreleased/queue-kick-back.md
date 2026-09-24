### Added

- **The merge queue labels pull requests** `merge-queue: queued` while it holds them
  and `merge-queue: rejected` when it kicks one back.
- **A kick-back comment reproduces the failure.** Each kick-back is a new comment with
  the run's link and the command that failed, runnable as written.
- **`--scratch-env NAME=DIR` on `magus queue validate` and `magus queue apply`** points
  a variable at a directory inside each candidate's scratch space, so cache isolation
  lives on the queue's flags rather than on the hook's command line. It may repeat.
