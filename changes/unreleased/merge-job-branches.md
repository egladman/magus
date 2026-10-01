### Added

- **`hack/dev/merge-job-branches.buzz` merges worker branches into one integration
  branch.** It takes `--jobs`, `--branches`, or a `--stack` that split wrote, orders
  branches by each job's depends_on and ancestry, merges them in a temporary worktree,
  lets `magus vcs resolve` settle generated files, and stops at a source conflict, naming
  the files, both sides and their commits, with nothing written.
