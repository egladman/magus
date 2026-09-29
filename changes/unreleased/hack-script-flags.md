### Changed

- **`hack/gha-queue.buzz` is now `hack/ci/merge-queue.buzz`.** hack/ci/ holds the steps
  only a workflow runs.
- **`hack/host-schemas.buzz` takes steps, not an environment variable.** Run
  `magus buzz hack/host-schemas.buzz -- verify | fetch`. It has no default step: a bare
  run prints the usage line and fails.

### Added

- **`hack/lint.buzz` lints named files or one rule.** `-- <path>...` reports only the
  findings at those files, from the rules that read them; a path no rule reads is not an
  error. `-- --rule <name>` runs one rule and refuses an unknown name, listing the rules.
  A bare run still lints the whole tree.
