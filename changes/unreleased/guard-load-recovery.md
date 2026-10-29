### Fixed

- **A tree that stops loading has a way back.** The guard refuses a commit while the
  working tree's magusfile does not load, naming the regenerate run. While nothing loads,
  `git reset --keep <checkpoint>` to a job's recorded checkpoint passes, and so does a
  shell write outside every checkout.
