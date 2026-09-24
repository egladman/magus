### Fixed

- **Generated docs examples read the same wherever they are regenerated.** The
  examples generator no longer inherits `MAGUS_*` variables or the cache and state
  dirs, so a merge queue candidate's shared cache stops leaking run history into the
  captured `magus explain` output.
