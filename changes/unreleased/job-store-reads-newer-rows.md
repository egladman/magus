### Changed

- **An older magus reads a job store a newer one wrote.** A newer row no longer locks
  older binaries out of the store: its unknown fields and version survive a rewrite, and
  only a row requiring something this build lacks turns read-only, named in `magus ls
  jobs`. Adding a field no longer bumps the schema version.
