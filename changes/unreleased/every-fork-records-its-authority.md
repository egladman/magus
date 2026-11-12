### Changed

- **Every fork records the revision its worker is judged from.** The job store writes
  `authority` on each row it forks: the root checkout's committed revision, or a worker's
  child's parent's authority. The transport names the forker; a declared `checkpoint`
  never sets it. Only the root moves it later. The field is optional, so the store needs
  no migration.
- **Breaking: a leased call whose row records no authority is refused its work.** The
  `lease-authority` rule denies state-changing lines and file writes under such a live
  row, and passes the reads and job verbs `lease-undeclared` passes. A row an older magus
  forked has none: end the job and fork it again.
- **A Go program embedding magus brings its own job rows.** `magus.WithJobReader` passes
  a `types.JobReader` that every lease rule reads rows through, in place of the job store.
  The lease on the call's context names the row.
  [ADR 0045](https://eli.gladman.cc/magus/decisions/0045-a-worker-is-judged-by-its-checkpoint/).
