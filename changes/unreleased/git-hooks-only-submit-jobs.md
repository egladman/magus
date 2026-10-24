### Changed

- **Git hooks only submit jobs.** `magus init --vcs git` no longer installs the
  pre-merge-commit, pre-commit, post-commit, post-rewrite or post-applypatch settle
  hooks, which ran a regeneration inside the commit. It strips the magus-regenerate
  section an earlier install wrote and leaves any other content of those hooks. After a
  merge, run `magus vcs resolve`. `magus vcs resolve --hook` and the `magus.settle`
  deferral are gone, and `magus doctor` reports a hook that still carries the old section.
