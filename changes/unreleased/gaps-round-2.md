### Changed

- **Breaking: `forkFacts().finishedBranches` lists `FinishedBranch` objects**
  (`branch`, `job`, `commits`), not rendered strings. The fork limit drops a re-fork's
  own branch by its job id.
- **The MGS3036 hint lists a target's function claim beside its settings claim.**
- **A worker may rebase its own job branch onto its base or a ref its row names**:
  `origin/main`, its checkpoint, a `depends_on` branch or a branch it integrates. Any
  other rebase stays refused.
- **A branch whose push recorded an upstream reads as one the remote has**, so the
  branch-name rule judges a push to an open pull request as an update.
- **A file-scoped rw run is held to the files it was handed**, not every file its
  update globs reach.
