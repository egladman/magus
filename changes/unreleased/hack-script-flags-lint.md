### Added

- **`hack/lint/run.buzz` lints named files or one rule.** `-- <path>...` reports only the
  findings at those files, from the rules that read them; a path no rule reads is not an
  error. `-- --rule <name>` runs one rule and refuses an unknown name, listing the rules.
  A bare run still lints the whole tree.
