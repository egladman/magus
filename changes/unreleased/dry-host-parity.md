### Fixed

- **The playground and `--dry-run` check a script as the engine does.** `proc`, `http`,
  `vcs` and the other host modules are typed there too, so a body the engine refuses no
  longer passes a dry run.
