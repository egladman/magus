### Added

- **`vcs\ref`, `vcs\changedFiles` and `vcs\base` take an optional `dir`.** Each reads
  the repository holding that directory, with whichever VCS it uses, instead of the one
  holding the cwd. A `dir` that does not exist raises.
