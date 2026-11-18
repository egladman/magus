### Changed

- **The workspace guard policy denies a ci run that skips projects the change reaches.**
  `magus run ci .` and a bare `magus run ci` from the workspace root are denied: `.` reads
  as everything but is the root project alone. Each names `magus affected ci`, which runs
  ci on every project the diff reaches, the set pull request CI runs.
- **It advises on a ci run that only adds projects.** `magus run ci --all` runs projects the
  change cannot reach. `magus run ci <project>` and every other target keep their projects.
