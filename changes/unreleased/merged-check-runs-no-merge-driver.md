### Fixed

- **Reading whether a job is merged has no side effects.** `magus\workspace().unmerged` ran a
  merge, whose killed `merge=magus` driver left `.merge_file_*` files that stopped
  `bootstrap-checkout`. The read now merges nothing: `MissingCommits` compares ancestry,
  file content and author-and-subject copies. Reverted work reads as held, and a rewritten
  squash the branch later edited reads as missing.
