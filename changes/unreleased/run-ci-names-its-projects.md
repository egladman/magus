### Changed

- **The workspace guard policy denies a ci run that skips projects the change reaches.**
  `magus run ci .`, a bare `magus run ci` from the workspace root, and `magus affected ci`
  with a project are denied: `.` reads as everything but is the root project alone, and a
  project after `affected ci` narrows the gate. Each names `magus affected ci`, which runs
  ci on every project the diff reaches, the set pull request CI runs.
- **It advises on a ci run that only adds projects.** `magus run ci /` runs projects the
  change cannot reach, and `magus affected ci /` adds nothing. `magus run ci <project>` and
  every other target keep their projects.
