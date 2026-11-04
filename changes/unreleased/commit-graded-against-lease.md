### Fixed

- **A leased commit is graded path by path.** A worker's `git commit` carrying a file outside
  its lease is refused with the paths to unstage, since a generator run writes past the
  write paths unseen. `job wait` refuses an empty result, and an integration row holds the
  outputs its merge regenerates.
