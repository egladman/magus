### Added

- **A worker claims its own job when magus proves the path belongs to it.** A
  `magus job apply` record carrying only `id` and `claim` adds each path with its proof:
  `refs:<symbol>`, `test:<file>`, `output:<project>:<target>` or `released`. The store
  re-proves it and records it on `claims`; the new `lease-claim` guard rule serves that
  apply line as the refusal's next step instead of sending the worker away. A job caps its
  claims with `max_claims` (3 when unset; a fork asking for more than 5 is advised to
  keep it small), and `jobs.max_claims` in `magus.yaml` (default 10) is the ceiling: a
  fork or a claim past it is refused, naming the value and where it came from.
