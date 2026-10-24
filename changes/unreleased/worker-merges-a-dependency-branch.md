### Changed

- **A worker may merge the branch of a job it depends on.** The guard already let a stacked
  worker merge its base; it now also accepts the branch named by a job in its row's
  `depends_on`, or that branch's `origin/` form, with the same flag limits.
