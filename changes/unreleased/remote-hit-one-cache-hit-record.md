### Fixed

- **A remote hit is one `cache.hit` record, counted once its replay succeeds.** A local
  replay that fails tries the remote tier before rebuilding.
