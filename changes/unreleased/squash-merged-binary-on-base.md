### Changed

- **A squash-merged `./magus` is no longer called foreign.** The off-base write rule
  judges a build whose commit is off the base branch by its stamped sources: when none
  differs from the base's, the build counts as on base.
