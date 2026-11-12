### Changed

- **Breaking: `magus job fork --fix` records who runs a gate instead of adding
  its goal (MGS3045).** With `ci.remote_gate` set, or under an integration job,
  a gate gets a deviation naming that owner, and an output outside the write
  paths is permitted for the integrator. Lint keeps its goal.
- **A fork validates once.** `--dry-run` lists exactly what the fork refuses,
  and the fork writes the plan it judged unless a live job changed meanwhile.
