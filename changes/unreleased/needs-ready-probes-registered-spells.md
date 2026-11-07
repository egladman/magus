### Added

- **`needs_ready` probes through any spell the workspace registers.** The project need
  not bind the spell, so its source globs stay out of the project's cache keys. A tool no
  registered spell can probe is a load error.
