### Fixed

- **`git apply` and `patch` are held to a job's write paths.** The guard reads the patch
  a line names, on a redirect, in a here-document or through a `cat`, and grades each file
  it writes. A worker's patch from any other pipe, an expanded word or a file the line
  itself creates is refused, since nothing shows where it writes.
- **A real deny stays a deny when another phase ran out of time.** `job fork` defers a
  check's grade only when the guard's deny is about the clock.
- **A job's checkout stays guarded past repeated guard timeouts.** A bound worker's
  command stays denied, and so does a subagent's line that reaches a live job's checkout
  by `cd` or by path. A checkout `job exec` took counts as one its fork made.
- **What only a person may do never degrades on a guard timeout.** A ledger write and
  `job grant` meet their own rules, and a `magus run` not shown clear of `human_only` is
  denied, including when the read of the target policies is what ran out of time.
- **Filing a release request no longer reads the whole session store.**
