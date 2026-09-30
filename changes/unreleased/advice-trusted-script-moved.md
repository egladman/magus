### Added

- **The advice action flags a moved script a trusted job runs.** The new
  `trusted-script-moved` advisor comments when a pull request renames or removes a file
  that a workflow job runs after checking out the default branch, and says whether the
  job breaks on the pull request or after the merge.
