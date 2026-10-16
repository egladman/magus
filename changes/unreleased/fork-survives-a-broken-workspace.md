### Fixed

- **`magus job fork` and `magus job apply` work when a magusfile will not load.** They
  refused before, so nobody could declare the job that fixes the break. They now write
  the row from the job store and name on stderr, with the load error, each check they
  could not run: no model tier is assigned and no tier limit checked, and the outputs
  regenerated outside the write paths, the ci targets no goal runs and an overlong check
  are not listed. Apply's base checkpoint reads `magus.yaml` and the repository, never the
  magusfiles.
