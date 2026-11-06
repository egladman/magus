### Fixed

- **A squash merge reads as merged after main edits its files.** `MissingCommits` now
  counts a branch as held when one commit on the base carries every net changed line of
  the branch, so the brief, `ls jobs` and the stop rule stop listing it. A commit with
  only part of the lines still reads as missing.
