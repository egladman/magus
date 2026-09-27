### Fixed

- **A worker can fork a child of its own job.** The guard refused every `magus job fork`
  and `op=fork` of another row under a lease, although the job store was built to grade
  exactly that. A new row naming the caller's lease as `parent` (a flag, a `--stdin`
  record in a heredoc, or the `parent` param) now reaches the store, which refuses a
  child reaching past its parent's paths. Where the checkout's store does not write as
  that lease, only a `--read-only` child declaring no paths, check or gates passes.
  `magus job wait` on the caller's own descendant passes the guard too.
