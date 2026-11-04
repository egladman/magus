### Fixed

- **`git cherry-pick` is held to a job's write paths.** The guard asks git for every file
  the picked commits write. A revision it cannot list is refused.
