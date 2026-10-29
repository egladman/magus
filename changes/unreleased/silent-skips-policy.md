### Fixed

- **A workspace guard rule that raises holds back the calls that land a change.** While a
  `magus\guard.command` rule raises, a push, `gh pr create`, `gh pr merge`, a state-writing
  magus verb or a commit is denied, naming the error; every other call still runs with the
  note. A magusfile that does not load now denies those verbs too when no policy is on
  record in the cache, since nothing says the workspace has no rule.
