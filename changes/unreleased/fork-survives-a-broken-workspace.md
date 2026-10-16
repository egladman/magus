### Fixed

- **`magus job fork` and `magus job apply` work when a magusfile does not load.** They
  refused before, so nobody could declare the job that fixes the break. They now write
  the row from the job store and name on stderr, with the load error, each check they
  skipped, such as model tier assignment. Apply's base checkpoint never reads the
  magusfiles.
