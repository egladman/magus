### Added

- **`merge-job-branches.buzz --apply` and `--verify` land and check the merge.** `--apply
  --into <branch>` regenerates once, commits that output on its own, creates the branch,
  and prints the push command. `--verify` runs every merged job's check and check goals in
  the merged tree. It and split-into-branches share `hack/magusfile/branch-stack.buzz`, a
  typed stack record.
