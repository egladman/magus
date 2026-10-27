### Fixed

- **A `./` or `../` import resolves beside the entry file.** `magus buzz --check` run from
  a parent directory read such an import against the working directory first, so a path
  that climbed out of a worktree typed the main checkout's copy of the module.
