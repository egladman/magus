### Fixed

- **The merge queue's own labels no longer start a second validation.** The intent job
  counts a label only when it is a merge method's, a review only once it approves, and
  auto-merge only where no method label is on. An action it does not weigh fails the run.
