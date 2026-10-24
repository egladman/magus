### Fixed

- **`magus job apply` keeps the soft paths and integrator waivers the fork derived.** A
  record that omits them no longer hardens soft test files or drops a waiver; one that
  names `soft_paths` decides. A holder may also `rm` or `mv` a file its own checkout
  created once a narrowing drops it from the write paths.
