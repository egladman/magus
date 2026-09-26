### Fixed

- **A workspace command rule's deny holds on a line the guard asks about.** A built-in
  ask, such as an ungated push, skipped `magus\guard.command`, so approving the push
  also ran what the workspace denies on the same line, such as a chained
  `gh pr merge --admin`.
