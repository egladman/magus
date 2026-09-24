### Fixed

- **Clones of an hg, jj or Sapling repository share one state store.** Identity is read
  from each backend's config without running it. jj is covered when colocated with git.
