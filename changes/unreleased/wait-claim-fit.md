### Fixed

- **`status --wait` holds for what a piped `affected` run claims.** It reserved one slot
  for `magus affected ci` while the run claims one per affected project, so the run
  started and then queued. A command it cannot size still waits for one slot, and now
  says why.
