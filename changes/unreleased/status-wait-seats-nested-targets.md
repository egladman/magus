### Fixed

- **`magus status --wait` reserves a seat wide enough for the targets a composer lends.**
  A skip_cache composer such as `ci` runs its same-project `ctx.needs` members as steps
  with claims of their own, but the wait weighed only the composer's. It reserved one
  slot for `magus affected ci`, then the root `test` asked for four.
