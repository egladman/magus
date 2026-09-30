### Added

- **The advice action flags a moved script a trusted job runs.** The new
  `trusted-script-moved` advisor comments when a pull request renames or deletes a file
  that a workflow job runs after checking out the default branch: a move breaks that job
  until the merge, and a delete the workflow still names breaks it after.
