### Changed

- **Two holders that keep results are named for what they hold.** The guard's holder
  of opened workspaces is `guard.OpenedWorkspaces` and the job package's holder of
  graded verdicts is `job.KeptVerdicts`. Neither ends in `Cache` or `Once`, so the
  `benchbeside` rule no longer asks for a benchmark beside them. The rule stops
  matching a third suffix.
