### Added

- **A merge's kept generated files regenerate after it finishes.** The merge driver records
  the owed target in the git dir, and `post-merge`, `post-rewrite` and `post-commit` submit
  a `regenerate-owed` job that runs each once, deepest project first, and stages the
  result; it prints the amend command and never amends. `magus doctor` reports an unsettled
  record (`owed-regeneration`).
