### Added

- **`hack/dev/release.buzz` is an interactive release.** It lists the releasable modules
  with their versions, asks which to release and whether each is a patch, minor or major,
  shows the `release` dry run's checks as a table, asks once, then runs `release:cd`. Every
  choice is a flag and a missing one fails naming it; it never pushes.
