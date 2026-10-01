### Added

- **`hack/dev/merge-job-branches.buzz` merges worker branches into one integration branch.**
  It is the inverse of split-into-branches: it takes `--jobs`, `--branches`, or a
  `--stack` that split wrote. It orders branches by each job's depends_on and by branch
  ancestry, merges them in a temporary worktree, lets the merge driver and `magus vcs
  resolve` settle generated files, and stops at a source conflict, naming the files, both
  sides and their commits, with nothing written. `--apply --into <branch>` regenerates
  once, commits that output on its own, creates the branch, and prints the push command.
  `--verify` runs every merged job's check and check goals in the merged tree. Both
  scripts share `hack/magusfile/branch-stack.buzz`, a typed stack record.
- **`magus job wait --integration` grades a job's check goals in another tree.** The
  `--stdin` result names runs recorded in the caller's checkout, such as a merged
  integration tree. The grade is recorded as the job's `integration`, beside its state,
  which does not move, and an ended job is graded too.
