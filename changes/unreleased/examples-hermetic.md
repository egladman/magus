### Fixed

- **Generated docs examples read the same wherever they are regenerated.** The
  examples generator runs magus in `testkit.Environ`, so no `MAGUS_*` variable, cache or
  state dir it inherits leaks run history into the captured `magus explain` output.
