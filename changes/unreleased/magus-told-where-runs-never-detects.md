### Changed

- **Breaking: magus is told where it runs; it never detects it.** The CI provider spells
  stop checking `GITHUB_ACTIONS`/`GITLAB_CI`; wire them under a setting the workflow sets.
  `TestNoEnvironmentSniffing` enforces it.
