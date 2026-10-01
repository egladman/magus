### Fixed

- **`vcs\cmd` ran version control unguarded under a lease.** It now asks `lease-vcs`
  before it runs, and refuses outright when a lease is acting and no guard is linked in.
