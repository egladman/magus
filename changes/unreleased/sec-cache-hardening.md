### Security

- **Breaking: no job holding a secret or a write token restores an Actions cache.** A
  merge queue hook can read the runner's runtime token and plant cache entries in the
  default branch's scope, so trusted jobs now install cold. `setup-magus` restores run
  history only with `restore-history: 'true'`. A conventions test enforces both.
